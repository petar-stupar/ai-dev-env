package namespace

import (
	"context"
	"errors"
	"fmt"

	"github.com/petar-stupar/ai-dev-env/internal/config"
	"github.com/petar-stupar/ai-dev-env/internal/docker"
)

// Clone copies a namespace: its configuration, its in-container work (the
// source container committed, else its snapshot tagged), its base image and
// its credentials volume. The clone gets a new port and password; its cache
// volume starts empty.
func (m *Manager) Clone(ctx context.Context, from, to string, o Options) error {
	if err := config.ValidName(to); err != nil {
		return err
	}
	src, unlockFrom, err := m.open(from)
	if err != nil {
		return err
	}
	defer unlockFrom()
	if ok, err := m.Store.Exists(to); err != nil {
		return err
	} else if ok {
		return fmt.Errorf("cannot clone %s: namespace %s already exists", from, to)
	}
	unlockTo, err := m.Store.Lock(to)
	if err != nil {
		return err
	}
	defer unlockTo()
	saved := false
	var undo []func()
	defer func() {
		if saved {
			return
		}
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
		if ok, _ := m.Store.Exists(to); !ok {
			_ = m.Store.Delete(to)
		}
	}()
	wctx := context.WithoutCancel(ctx)

	phase, err := m.Observe(ctx, from, src)
	if err != nil {
		return err
	}
	if phase == Running && !o.Yes {
		return fmt.Errorf("cannot clone namespace %s while it is running: stop it first, or pass --yes to commit it paused", from)
	}

	dst := &config.State{
		Version: config.CurrentVersion,
		Stacks:  append([]string{}, src.Stacks...),
		Mounts:  append([]config.Mount{}, src.Mounts...),
	}
	ports, err := m.Store.Ports()
	if err != nil {
		return err
	}
	if dst.Port, err = m.Plat.FreePort(ports); err != nil {
		return fmt.Errorf("clone %s to %s: pick a port: %w", from, to, brief(err))
	}
	if dst.Password, err = config.NewPassword(); err != nil {
		return err
	}

	switch {
	case phase == Stopped || phase == Running:
		id, err := m.Docker.Commit(ctx, ContainerName(from), ImageSnap(to), true)
		if err != nil {
			return fmt.Errorf("clone %s to %s: commit the container: %w", from, to, brief(err))
		}
		undo = append(undo, func() { _ = m.Docker.RemoveImage(wctx, ImageSnap(to)) })
		dst.Snapshot = id
	case src.Snapshot != "":
		if err := m.Docker.Tag(ctx, ImageSnap(from), ImageSnap(to)); err != nil {
			return fmt.Errorf("clone %s to %s: tag the snapshot: %w", from, to, brief(err))
		}
		undo = append(undo, func() { _ = m.Docker.RemoveImage(wctx, ImageSnap(to)) })
		dst.Snapshot = src.Snapshot
	}
	if phase != NoImage {
		if err := m.Docker.Tag(ctx, ImageBase(from), ImageBase(to)); err != nil {
			return fmt.Errorf("clone %s to %s: tag the base image: %w", from, to, brief(err))
		}
		undo = append(undo, func() { _ = m.Docker.RemoveImage(wctx, ImageBase(to)) })
		if src.Image != nil {
			img := *src.Image
			img.Stacks = append([]string{}, src.Image.Stacks...)
			dst.Image = &img
		}
	}

	ok, err := m.Docker.VolumeExists(ctx, CredsVolume(from))
	if err != nil {
		return fmt.Errorf("clone %s to %s: inspect volume %s: %w", from, to, CredsVolume(from), err)
	}
	if ok {
		helper := ""
		switch {
		case phase != NoImage:
			helper = ImageBase(from)
		case dst.Snapshot != "":
			helper = ImageSnap(to)
		}
		if helper == "" {
			m.warnf("namespace %s has no image to copy volume %s with; the clone starts signed out", from, CredsVolume(from))
		} else {
			if err := m.Docker.VolumeCreate(ctx, CredsVolume(to)); err != nil {
				return fmt.Errorf("clone %s to %s: create volume %s: %w", from, to, CredsVolume(to), err)
			}
			undo = append(undo, func() { _ = m.Docker.VolumeRemove(wctx, CredsVolume(to)) })
			if err := m.Docker.VolumeClone(ctx, CredsVolume(from), CredsVolume(to), helper); err != nil {
				return fmt.Errorf("clone %s to %s: copy the credentials volume: %w", from, to, brief(err))
			}
		}
	}

	if err := m.Store.Save(to, dst); err != nil {
		return err
	}
	saved = true
	m.printf("cloned namespace %s to %s (port %d)", from, to, dst.Port)
	if dst.Snapshot != "" {
		m.printf("the in-container work carries over in %s", ImageSnap(to))
	}
	return nil
}

// Remove deletes a namespace: container, both images, its volumes unless
// keepVolumes, and its state. State is kept when a Docker object could not
// be removed, so the command can be retried.
func (m *Manager) Remove(ctx context.Context, ns string, keepVolumes bool, o Options) error {
	_, unlock, err := m.open(ns)
	if err != nil {
		return err
	}
	defer unlock()
	what := "its container, images and volumes"
	if keepVolumes {
		what = "its container and images (volumes are kept)"
	}
	if !o.Yes && !m.confirm(fmt.Sprintf("remove namespace %s: this deletes %s. Continue?", ns, what)) {
		return fmt.Errorf("remove of namespace %s cancelled", ns)
	}
	var errs []error
	gone := func(what string, err error) {
		if err != nil && !errors.Is(err, docker.ErrNotFound) {
			errs = append(errs, fmt.Errorf("remove %s: %w", what, err))
		}
	}
	gone("container "+ContainerName(ns), m.Docker.Remove(ctx, ContainerName(ns), true))
	gone("image "+ImageSnap(ns), m.Docker.RemoveImage(ctx, ImageSnap(ns)))
	gone("image "+ImageBase(ns), m.Docker.RemoveImage(ctx, ImageBase(ns)))
	if !keepVolumes {
		gone("volume "+CredsVolume(ns), m.Docker.VolumeRemove(ctx, CredsVolume(ns)))
		gone("volume "+CacheVolume(ns), m.Docker.VolumeRemove(ctx, CacheVolume(ns)))
	}
	if len(errs) > 0 {
		return fmt.Errorf("remove namespace %s: %w; its state is kept, so the command can be retried", ns, errors.Join(errs...))
	}
	if err := m.Store.Delete(ns); err != nil {
		return err
	}
	m.printf("removed namespace %s", ns)
	return nil
}
