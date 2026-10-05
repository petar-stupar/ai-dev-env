package namespace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/petar-stupar/ai-dev-env/internal/aidefile"
	"github.com/petar-stupar/ai-dev-env/internal/config"
	"github.com/petar-stupar/ai-dev-env/internal/docker"
	"github.com/petar-stupar/ai-dev-env/internal/stacks"
)

// appendStacks appends names not yet in list, in order.
func appendStacks(list, names []string) []string {
	out := append([]string{}, list...)
	for _, n := range names {
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// New creates a namespace from .aide text. home expands ~ in host paths.
func (m *Manager) New(ctx context.Context, ns string, aide io.Reader, home string) error {
	if err := config.ValidName(ns); err != nil {
		return err
	}
	if ok, err := m.Store.Exists(ns); err != nil {
		return err
	} else if ok {
		return fmt.Errorf("namespace %s already exists", ns)
	}
	unlock, err := m.Store.Lock(ns)
	if err != nil {
		return err
	}
	defer unlock()
	created := false
	defer func() {
		if !created {
			if ok, _ := m.Store.Exists(ns); !ok {
				_ = m.Store.Delete(ns)
			}
		}
	}()
	if ok, err := m.Store.Exists(ns); err != nil {
		return err
	} else if ok {
		return fmt.Errorf("namespace %s already exists", ns)
	}

	if err := m.refuseLeftovers(ctx, ns, "create namespace "+ns); err != nil {
		return err
	}

	cmds, err := aidefile.Parse(aide)
	if err != nil {
		return fmt.Errorf("new namespace %s: %w", ns, err)
	}
	// A new namespace reaches only the hosts on its allowlist unless the
	// file says otherwise.
	st := &config.State{Version: config.CurrentVersion, Stacks: []string{}, Mounts: []config.Mount{}, Network: config.NetworkAllowlist}
	for _, c := range cmds {
		switch c.Kind {
		case aidefile.Network:
			st.Network = c.Args[0]
		case aidefile.Allow:
			for _, h := range c.Args {
				if err := stacks.ValidHost(h); err != nil {
					return fmt.Errorf("new namespace %s: line %d: %w", ns, c.Line, err)
				}
			}
			st.Allow = appendStacks(st.Allow, c.Args)
		case aidefile.Stack:
			st.Stacks = appendStacks(st.Stacks, c.Args)
		case aidefile.Mount:
			var pairs []config.Mount
			for i := 0; i+1 < len(c.Args); i += 2 {
				pairs = append(pairs, config.Mount{Host: c.Args[i], Container: c.Args[i+1]})
			}
			valid, err := m.validateMounts(pairs, home)
			if err != nil {
				return fmt.Errorf("new namespace %s: line %d: %w", ns, c.Line, err)
			}
			st.Mounts = mergeMounts(st.Mounts, valid)
		}
	}
	res, err := m.resolve(ns, st.Stacks)
	if err != nil {
		return err
	}
	if st.Password, err = config.NewPassword(); err != nil {
		return err
	}
	if err := ensureSudoPassword(st); err != nil {
		return err
	}
	if err := m.savePicked(ns, st, "new namespace "+ns); err != nil {
		return err
	}
	created = true
	m.printf("created namespace %s", ns)
	m.printf("  stacks: %s", strings.Join(res.Names(), " "))
	// Every mount is listed: the .aide file may be someone else's, and each
	// line hands a host directory to the agents read-write.
	m.printf("  mounts: %d", len(st.Mounts))
	for _, mt := range st.Mounts {
		m.printf("    %s -> %s (read-write)", mt.Host, mt.Container)
	}
	m.printf("  port:   %d", st.Port)
	if st.Restricted() {
		m.printf("  network: allowlist: %s", strings.Join(append(res.Allow(), st.Allow...), " "))
	} else {
		m.printf("  network: open")
	}
	m.printf("next: aide ns:%s build", ns)
	return nil
}

// savePicked picks a free port for st and saves it, under the store-wide
// lock so two commands cannot pick the same port.
func (m *Manager) savePicked(ns string, st *config.State, what string) error {
	unlock, err := m.Store.LockPorts()
	if err != nil {
		return err
	}
	defer unlock()
	ports, err := m.Store.Ports()
	if err != nil {
		return err
	}
	if st.Port, err = m.Plat.FreePort(ports); err != nil {
		return fmt.Errorf("%s: pick a port: %w", what, brief(err))
	}
	return m.Store.Save(ns, st)
}

// refuseLeftovers fails when Docker already has a container or an image
// under the names of ns, which has no state here: they belong to another
// aide configuration on the same daemon, or are left over, and every verb
// would go on to treat them as this namespace's own.
func (m *Manager) refuseLeftovers(ctx context.Context, ns, what string) error {
	found := func(kind, name string, err error) error {
		if errors.Is(err, docker.ErrNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s: inspect %s %s: %w", what, kind, name, brief(err))
		}
		return fmt.Errorf("cannot %s: Docker already has %s %s, which this aide configuration does not know. It belongs to another aide configuration or is left over; remove it with docker, or choose another name", what, kind, name)
	}
	_, err := m.Docker.Container(ctx, ContainerName(ns))
	if err := found("container", ContainerName(ns), err); err != nil {
		return err
	}
	for _, ref := range []string{ImageBase(ns), ImageSnap(ns)} {
		_, err := m.Docker.Image(ctx, ref)
		if err := found("image", ref, err); err != nil {
			return err
		}
	}
	return nil
}

// AddStacks appends stacks to the selection; allowed only without a container.
func (m *Manager) AddStacks(ctx context.Context, ns string, names []string) error {
	st, unlock, err := m.open(ns)
	if err != nil {
		return err
	}
	defer unlock()
	phase, err := m.Observe(ctx, ns, st)
	if err != nil {
		return err
	}
	if phase == Stopped || phase == Running {
		return refuse(ns, "add stacks", phase, hintReset)
	}
	next := appendStacks(st.Stacks, names)
	if slices.Equal(next, st.Stacks) {
		m.printf("namespace %s already has stacks %s", ns, strings.Join(names, " "))
		return nil
	}
	res, err := m.resolve(ns, next)
	if err != nil {
		return err
	}
	st.Stacks = next
	if err := m.Store.Save(ns, st); err != nil {
		return err
	}
	m.printf("stacks of %s: %s", ns, strings.Join(res.Names(), " "))
	if phase == Built {
		m.printf("the image is stale; run \"aide ns:%s build\"", ns)
	}
	return nil
}

// Build builds aide-<ns>:base from the selected stacks.
func (m *Manager) Build(ctx context.Context, ns string, noCache bool) error {
	st, unlock, err := m.open(ns)
	if err != nil {
		return err
	}
	defer unlock()
	phase, err := m.Observe(ctx, ns, st)
	if err != nil {
		return err
	}
	if phase == Stopped || phase == Running {
		return refuse(ns, "build", phase, hintReset)
	}
	if st.Snapshot != "" {
		return fmt.Errorf("cannot build while snapshot %s exists: start would keep running it instead of the new image; run \"aide ns:%s reset\" first (it discards the work inside it)", ImageSnap(ns), ns)
	}
	if len(st.Stacks) == 0 {
		return fmt.Errorf("cannot build namespace %s: no stacks selected; run \"aide ns:%s stack base ...\" first", ns, ns)
	}
	res, err := m.resolve(ns, st.Stacks)
	if err != nil {
		return err
	}
	bc, err := stacks.Generate(res)
	if err != nil {
		return fmt.Errorf("build namespace %s: %w", ns, err)
	}
	dir, err := os.MkdirTemp("", "aide-build-")
	if err != nil {
		return fmt.Errorf("build namespace %s: %w", ns, err)
	}
	defer os.RemoveAll(dir)
	if err := bc.WriteDir(dir); err != nil {
		return fmt.Errorf("build namespace %s: write the build context: %w", ns, err)
	}
	uid, gid := m.Plat.UIDGID()
	id, err := m.Docker.Build(ctx, docker.BuildSpec{
		Tag:     ImageBase(ns),
		Dir:     dir,
		NoCache: noCache,
		BuildArgs: map[string]string{
			"AGENT_UID": strconv.Itoa(uid),
			"AGENT_GID": strconv.Itoa(gid),
		},
		Labels:   map[string]string{LabelNamespace: ns},
		Progress: m.out(),
	})
	if err != nil {
		return fmt.Errorf("build namespace %s: %w", ns, err)
	}
	st.Image = &config.Image{ID: id, Stacks: res.Names(), Fingerprint: bc.Fingerprint}
	if err := m.Store.Save(ns, st); err != nil {
		return err
	}
	m.printf("built %s (%s)", ImageBase(ns), strings.Join(res.Names(), " "))
	m.printf("next: aide ns:%s start", ns)
	return nil
}

// Start creates or starts the container, remounting when the mount set changed.
func (m *Manager) Start(ctx context.Context, ns string, o Options) error {
	st, unlock, err := m.open(ns)
	if err != nil {
		return err
	}
	defer unlock()
	phase, err := m.Observe(ctx, ns, st)
	if err != nil {
		return err
	}
	switch phase {
	case NoImage:
		return refuse(ns, "start", phase, hintBuild)
	case Running:
		m.printf("namespace %s is already running", ns)
		m.printAccess(ns, st)
		return nil
	}
	res, err := m.resolve(ns, st.Stacks)
	if err != nil {
		return err
	}
	name := ContainerName(ns)
	if phase == Built {
		if st.Image == nil || !slices.Equal(st.Image.Stacks, res.Names()) {
			return fmt.Errorf("cannot start namespace %s: the image is stale (stacks changed); run \"aide ns:%s reset\", then \"aide ns:%s build\"", ns, ns, ns)
		}
		if bc, err := stacks.Generate(res); err == nil && bc.Fingerprint != st.Image.Fingerprint {
			m.warnf("the stack definitions changed since %s was built; run \"aide ns:%s build\" to pick them up", ImageBase(ns), ns)
		}
		for _, v := range []string{CredsVolume(ns), CacheVolume(ns)} {
			if err := m.Docker.VolumeCreate(ctx, v); err != nil {
				return fmt.Errorf("start namespace %s: create volume %s: %w", ns, v, err)
			}
		}
		image := ImageBase(ns)
		if st.Snapshot != "" {
			image = ImageSnap(ns)
		}
		if err := ensureSudoPassword(st); err != nil {
			return err
		}
		if _, err := m.Docker.RunContainer(ctx, m.runSpec(ns, st, res, image, st.Mounts)); err != nil {
			_ = m.Docker.Remove(context.WithoutCancel(ctx), name, true)
			return fmt.Errorf("start namespace %s: %w", ns, brief(err))
		}
		st.AppliedMounts = cloneMounts(st.Mounts)
		if st.AppliedMounts == nil {
			st.AppliedMounts = []config.Mount{}
		}
		if err := m.Store.Save(ns, st); err != nil {
			return err
		}
	} else { // Stopped
		if st.AppliedMounts != nil && mountsEqual(st.Mounts, st.AppliedMounts) {
			if err := m.Docker.Start(ctx, name); err != nil {
				return fmt.Errorf("start namespace %s: %w", ns, brief(err))
			}
		} else {
			m.printf("the mounts changed since the container was created; recreating it")
			if err := m.remount(ctx, ns, st, res, st.Mounts, Stopped, o); err != nil {
				return err
			}
		}
	}
	m.pushAllow(ctx, ns, st)
	m.printf("namespace %s is running", ns)
	m.printAccess(ns, st)
	return nil
}

// Stop stops a running container; any other phase is a no-op.
func (m *Manager) Stop(ctx context.Context, ns string) error {
	st, unlock, err := m.open(ns)
	if err != nil {
		return err
	}
	defer unlock()
	phase, err := m.Observe(ctx, ns, st)
	if err != nil {
		return err
	}
	if phase != Running {
		m.printf("namespace %s is not running (%s)", ns, phase)
		return nil
	}
	if err := m.Docker.Stop(ctx, ContainerName(ns), StopTimeout); err != nil {
		return fmt.Errorf("stop namespace %s: %w", ns, brief(err))
	}
	m.printf("stopped namespace %s", ns)
	return nil
}

// Reset drops the container and the snapshot; the next start is clean.
func (m *Manager) Reset(ctx context.Context, ns string, o Options) error {
	st, unlock, err := m.open(ns)
	if err != nil {
		return err
	}
	defer unlock()
	phase, err := m.Observe(ctx, ns, st)
	if err != nil {
		return err
	}
	switch phase {
	case NoImage:
		m.printf("namespace %s has no image; nothing to reset", ns)
		return nil
	case Built:
		if st.Snapshot == "" {
			m.printf("namespace %s has no container; nothing to reset", ns)
			return nil
		}
		if !o.Yes && !m.confirm(fmt.Sprintf("reset namespace %s: this discards the work saved in %s. Continue?", ns, ImageSnap(ns))) {
			return fmt.Errorf("reset of namespace %s cancelled", ns)
		}
	default:
		if !o.Yes && !m.confirm(fmt.Sprintf("reset namespace %s: this discards everything inside the container. Continue?", ns)) {
			return fmt.Errorf("reset of namespace %s cancelled", ns)
		}
		name := ContainerName(ns)
		if phase == Running {
			if err := m.Docker.Stop(ctx, name, StopTimeout); err != nil {
				return fmt.Errorf("reset namespace %s: stop the container: %w", ns, brief(err))
			}
		}
		if err := m.Docker.Remove(ctx, name, false); err != nil && !errors.Is(err, docker.ErrNotFound) {
			return fmt.Errorf("reset namespace %s: remove the container: %w", ns, brief(err))
		}
	}
	if err := m.Docker.RemoveImage(ctx, ImageSnap(ns)); err != nil && !errors.Is(err, docker.ErrNotFound) {
		m.warnf("could not remove %s: %v", ImageSnap(ns), err)
	}
	st.Snapshot = ""
	st.AppliedMounts = nil
	if err := m.Store.Save(ns, st); err != nil {
		return err
	}
	m.printf("reset namespace %s; the next start is clean", ns)
	return nil
}

// Mount adds or replaces mounts. home expands ~ in host paths.
func (m *Manager) Mount(ctx context.Context, ns string, pairs []config.Mount, home string, o Options) error {
	if len(pairs) == 0 {
		return errors.New("mount needs at least one <host-path> <container-path> pair")
	}
	valid, err := m.validateMounts(pairs, home)
	if err != nil {
		return err
	}
	return m.changeMounts(ctx, ns, "mount", o, func(cur []config.Mount) ([]config.Mount, error) {
		return mergeMounts(cur, valid), nil
	})
}

// Umount removes mounts by container path.
func (m *Manager) Umount(ctx context.Context, ns string, containerPaths []string, o Options) error {
	if len(containerPaths) == 0 {
		return errors.New("umount needs at least one container path")
	}
	return m.changeMounts(ctx, ns, "umount", o, func(cur []config.Mount) ([]config.Mount, error) {
		out := append([]config.Mount{}, cur...)
		for _, p := range containerPaths {
			c := p
			if strings.HasPrefix(p, "/") {
				c = cleanSlash(p)
			}
			i := slices.IndexFunc(out, func(mt config.Mount) bool { return mt.Container == c })
			if i < 0 {
				return nil, fmt.Errorf("umount: nothing is mounted at %s in namespace %s", p, ns)
			}
			out = slices.Delete(out, i, i+1)
		}
		return out, nil
	})
}

func (m *Manager) changeMounts(ctx context.Context, ns, verb string, o Options, apply func([]config.Mount) ([]config.Mount, error)) error {
	st, unlock, err := m.open(ns)
	if err != nil {
		return err
	}
	defer unlock()
	next, err := apply(st.Mounts)
	if err != nil {
		return err
	}
	phase, err := m.Observe(ctx, ns, st)
	if err != nil {
		return err
	}
	if phase != Running {
		st.Mounts = next
		if err := m.Store.Save(ns, st); err != nil {
			return err
		}
		if phase == Stopped && !mountsEqual(next, st.AppliedMounts) {
			m.printf("%s recorded; applied on next start", verb)
		} else {
			m.printf("%s recorded", verb)
		}
		return nil
	}
	if st.AppliedMounts != nil && mountsEqual(next, st.AppliedMounts) {
		st.Mounts = next
		if err := m.Store.Save(ns, st); err != nil {
			return err
		}
		m.printf("mounts unchanged; nothing to do")
		return nil
	}
	res, err := m.resolve(ns, st.Stacks)
	if err != nil {
		return err
	}
	if err := m.remount(ctx, ns, st, res, next, Running, o); err != nil {
		return err
	}
	m.pushAllow(ctx, ns, st)
	m.printf("namespace %s is running with the new mounts", ns)
	m.printAccess(ns, st)
	return nil
}

// pushAllow hands the namespace's own allowed hosts to its running container,
// which keeps them in its filesystem and reloads the proxy. A failure is a
// warning: the container then allows only what its stacks need.
func (m *Manager) pushAllow(ctx context.Context, ns string, st *config.State) {
	if !st.Restricted() {
		return
	}
	args := append([]string{NetworkScript, "allow"}, st.Allow...)
	if _, err := m.Docker.Exec(ctx, ContainerName(ns), args...); err != nil {
		m.warnf("could not send the allowed hosts to %s: %v; only the stacks' own hosts are reachable (an image built before the allowlist existed needs \"aide ns:%s reset\" and a build)", ContainerName(ns), brief(err), ns)
	}
}

// Allow adds hosts to the namespace's allowlist; Disallow removes them. A
// running container picks the change up at once.
func (m *Manager) Allow(ctx context.Context, ns string, hosts []string, remove bool) error {
	if len(hosts) == 0 {
		return errors.New("allow needs at least one host")
	}
	for _, h := range hosts {
		if err := stacks.ValidHost(h); err != nil {
			return err
		}
	}
	st, unlock, err := m.open(ns)
	if err != nil {
		return err
	}
	defer unlock()
	if remove {
		for _, h := range hosts {
			i := slices.Index(st.Allow, h)
			if i < 0 {
				return fmt.Errorf("disallow: %s is not one of the hosts added to namespace %s (a stack's own hosts cannot be removed)", h, ns)
			}
			st.Allow = slices.Delete(st.Allow, i, i+1)
		}
	} else {
		st.Allow = appendStacks(st.Allow, hosts)
	}
	phase, err := m.Observe(ctx, ns, st)
	if err != nil {
		return err
	}
	if err := m.Store.Save(ns, st); err != nil {
		return err
	}
	if len(st.Allow) == 0 {
		m.printf("the allowlist of %s is now only what its stacks need", ns)
	} else {
		m.printf("the allowlist of %s is what its stacks need plus: %s", ns, strings.Join(st.Allow, " "))
	}
	switch {
	case !st.Restricted():
		m.printf("the network of %s is open, so the list has no effect until \"aide ns:%s network allowlist\"", ns, ns)
	case phase == Running:
		m.pushAllow(ctx, ns, st)
	}
	return nil
}

// Network switches a namespace between an open network and the allowlist. A
// container is created with one or the other, so an existing one is
// committed and recreated, as for a mount change.
func (m *Manager) Network(ctx context.Context, ns, mode string, o Options) error {
	if mode != config.NetworkOpen && mode != config.NetworkAllowlist {
		return fmt.Errorf("network: want %s or %s, got %q", config.NetworkOpen, config.NetworkAllowlist, mode)
	}
	st, unlock, err := m.open(ns)
	if err != nil {
		return err
	}
	defer unlock()
	if st.Restricted() == (mode == config.NetworkAllowlist) {
		m.printf("the network of %s is already %s", ns, mode)
		return nil
	}
	if mode == config.NetworkOpen && !o.Yes && !m.confirm(fmt.Sprintf("open the network of namespace %s: the agents will be able to reach any host. Continue?", ns)) {
		return fmt.Errorf("network change of namespace %s cancelled", ns)
	}
	phase, err := m.Observe(ctx, ns, st)
	if err != nil {
		return err
	}
	st.Network = mode
	if phase != Stopped && phase != Running {
		if err := m.Store.Save(ns, st); err != nil {
			return err
		}
		m.printf("the network of %s is %s from its next start", ns, mode)
		return nil
	}
	res, err := m.resolve(ns, st.Stacks)
	if err != nil {
		return err
	}
	m.printf("recreating the container: the network mode is fixed when it is created")
	mounts := st.AppliedMounts
	if mounts == nil {
		mounts = st.Mounts
	}
	if err := m.remount(ctx, ns, st, res, mounts, phase, o); err != nil {
		return err
	}
	m.pushAllow(ctx, ns, st)
	m.printf("namespace %s is running with the network %s", ns, mode)
	m.printAccess(ns, st)
	return nil
}

func cleanSlash(p string) string { return path.Clean(p) }

// State returns the configuration as .aide text.
func (m *Manager) State(ctx context.Context, ns string) (string, error) {
	if err := config.ValidName(ns); err != nil {
		return "", err
	}
	st, err := m.Store.Load(ns)
	if err != nil {
		return "", err
	}
	return aidefile.PrintState(ns, st), nil
}

// Dockerfile returns the Dockerfile that build would use.
func (m *Manager) Dockerfile(ctx context.Context, ns string) ([]byte, error) {
	if err := config.ValidName(ns); err != nil {
		return nil, err
	}
	st, err := m.Store.Load(ns)
	if err != nil {
		return nil, err
	}
	res, err := m.resolve(ns, st.Stacks)
	if err != nil {
		return nil, err
	}
	bc, err := stacks.Generate(res)
	if err != nil {
		return nil, err
	}
	return bc.Dockerfile, nil
}

// List summarises every namespace. It holds no lock, so it only looks: the
// drift Observe would repair is left for the next verb that takes the lock.
// Saving here could overwrite a state another command is changing, and one
// run against the wrong docker context would make every namespace forget
// its snapshot.
func (m *Manager) List(ctx context.Context) ([]Summary, error) {
	names, err := m.Store.List()
	if err != nil {
		return nil, err
	}
	var out []Summary
	for _, n := range names {
		st, err := m.Store.Load(n)
		if err != nil {
			return nil, err
		}
		phase, _, err := m.observe(ctx, n, st)
		if err != nil {
			return nil, fmt.Errorf("namespace %s: %w", n, err)
		}
		out = append(out, Summary{Name: n, Phase: phase, Port: st.Port, Mounts: len(st.Mounts), Stacks: st.Stacks})
	}
	return out, nil
}
