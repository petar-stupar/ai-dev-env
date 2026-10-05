package namespace

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/petar-stupar/ai-dev-env/internal/config"
	"github.com/petar-stupar/ai-dev-env/internal/stacks"
)

// remount moves a namespace with a container (Stopped or Running) to a new
// mount set: busy check, stop, commit, optional flatten, recreate, and on
// failure a rollback to the previous mounts. The plan's B5 steps 2-8.
func (m *Manager) remount(ctx context.Context, ns string, st *config.State, res *stacks.Resolution, next []config.Mount, phase Phase, o Options) error {
	name := ContainerName(ns)
	if err := ensureSudoPassword(st); err != nil {
		return err
	}
	// restart brings a container that was running back up when a step fails
	// before it has been removed.
	restart := func(err error) error {
		if phase != Running {
			return err
		}
		if serr := m.Docker.Start(context.WithoutCancel(ctx), name); serr != nil {
			return fmt.Errorf("%w; restarting the container also failed: %v", err, brief(serr))
		}
		m.printf("container restarted with the previous mounts")
		return err
	}

	// 2. Warn about busy work.
	if phase == Running {
		procs, err := m.Docker.Processes(ctx, name)
		if err != nil {
			// Not knowing is not the same as idle: ask.
			m.warnf("could not list the processes in %s: %v", name, brief(err))
			if !o.Yes && !m.confirm("could not check for running work; remounting restarts the container. Continue?") {
				return fmt.Errorf("remount of namespace %s cancelled; nothing changed", ns)
			}
		}
		if busy := busyProcesses(procs); len(busy) > 0 {
			m.printf("these processes in %s will be stopped:", name)
			for _, p := range busy {
				m.printf("  %6d  %s  %s", p.PID, (time.Duration(p.Elapsed) * time.Second).String(), p.Args)
			}
			if !o.Yes && !m.confirm("remounting restarts the container; continue?") {
				return fmt.Errorf("remount of namespace %s cancelled; nothing changed", ns)
			}
		}

		// 3. Stop.
		t := time.Now()
		if err := m.Docker.Stop(ctx, name, StopTimeout); err != nil {
			return fmt.Errorf("remount of namespace %s: stop the container: %w", ns, brief(err))
		}
		m.printf("stopped in %s", since(t))
	}

	// 4. Commit; the snapshot ID is saved at once.
	t := time.Now()
	id, err := m.Docker.Commit(ctx, name, ImageSnap(ns), true)
	if err != nil {
		return restart(fmt.Errorf("remount of namespace %s: commit the container: %w; mounts unchanged", ns, brief(err)))
	}
	prevSnap := st.Snapshot
	st.Snapshot = id
	if err := m.Store.Save(ns, st); err != nil {
		return restart(err)
	}
	m.printf("committed in %s", since(t))

	// 5. Flatten when the snapshot has too many layers.
	m.maybeFlatten(ctx, ns, st)

	// 6. Recreate. From here on a cancelled context would strand the
	// namespace without a container, so it is ignored.
	wctx := context.WithoutCancel(ctx)
	t = time.Now()
	if err := m.Docker.Remove(wctx, name, false); err != nil {
		return restart(fmt.Errorf("remount of namespace %s: remove the container: %w; the work is saved in %s", ns, brief(err), ImageSnap(ns)))
	}
	if _, err := m.Docker.RunContainer(wctx, m.runSpec(ns, st, res, ImageSnap(ns), next)); err != nil {
		// 7. Roll back to the previous mounts.
		_ = m.Docker.Remove(wctx, name, true)
		old := st.AppliedMounts
		if _, rerr := m.Docker.RunContainer(wctx, m.runSpec(ns, st, res, ImageSnap(ns), old)); rerr != nil {
			_ = m.Docker.Remove(wctx, name, true)
			st.AppliedMounts = nil
			if serr := m.Store.Save(ns, st); serr != nil {
				m.warnf("save state: %v", serr)
			}
			fmt.Fprintf(m.errw(), "the container could not be recreated; the work inside it is kept in %s. Run \"aide ns:%s start\" to recreate it.\n", ImageSnap(ns), ns)
			return fmt.Errorf("remount of namespace %s: run with the new mounts: %w; restoring the previous mounts also failed: %v", ns, brief(err), brief(rerr))
		}
		if old == nil {
			st.AppliedMounts = []config.Mount{}
			if serr := m.Store.Save(ns, st); serr != nil {
				m.warnf("save state: %v", serr)
			}
		}
		m.printf("container recreated with the previous mounts")
		return fmt.Errorf("remount of namespace %s: run with the new mounts: %w; the previous mounts are restored", ns, brief(err))
	}
	m.printf("recreated in %s", since(t))

	// 8. Record and clean up.
	st.Mounts = cloneMounts(next)
	st.AppliedMounts = cloneMounts(next)
	if st.AppliedMounts == nil {
		st.AppliedMounts = []config.Mount{}
	}
	if err := m.Store.Save(ns, st); err != nil {
		return err
	}
	if prevSnap != "" && prevSnap != st.Snapshot {
		m.dropImage(wctx, prevSnap)
	}
	return nil
}

// maybeFlatten rewrites the snapshot as one layer when it has more layers
// than the threshold. Every failure is a warning: the unflattened snapshot
// still works.
func (m *Manager) maybeFlatten(ctx context.Context, ns string, st *config.State) {
	info, err := m.Docker.Image(ctx, ImageSnap(ns))
	if err != nil {
		m.warnf("inspect %s: %v; not flattening", ImageSnap(ns), err)
		return
	}
	if info.Layers <= m.threshold() {
		return
	}
	base, err := m.Docker.Image(ctx, ImageBase(ns))
	if err != nil {
		m.warnf("inspect %s for its configuration: %v; not flattening", ImageBase(ns), err)
		return
	}
	t := time.Now()
	id, err := m.Docker.Flatten(ctx, ContainerName(ns), ImageSnap(ns), base.Config, m.errw())
	if err != nil {
		if errors.Is(err, context.Canceled) {
			m.warnf("flatten cancelled; keeping the unflattened snapshot")
		} else {
			m.warnf("flatten failed, keeping the unflattened snapshot: %v", err)
		}
		return
	}
	unflat := st.Snapshot
	st.Snapshot = id
	if err := m.Store.Save(ns, st); err != nil {
		m.warnf("save state: %v", err)
	}
	m.printf("flattened %d layers into 1 in %s", info.Layers, since(t))
	if unflat != "" && unflat != id {
		m.dropImage(ctx, unflat)
	}
}
