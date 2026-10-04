// Package namespace implements aide's commands over one namespace: the phase
// machine, build, start, stop, reset, mount/umount with remount, clone, remove.
package namespace

import (
	"context"
	"errors"
	"io"

	"github.com/petar-stupar/ai-dev-env/internal/config"
	"github.com/petar-stupar/ai-dev-env/internal/docker"
	"github.com/petar-stupar/ai-dev-env/internal/platform"
)

// Phase is where a namespace is in its life.
type Phase int

const (
	NoImage Phase = iota
	Built
	Stopped
	Running
)

func (p Phase) String() string {
	return [...]string{"no image", "built", "stopped", "running"}[p]
}

// Docker object names for a namespace.
func ImageBase(ns string) string     { return "aide-" + ns + ":base" }
func ImageSnap(ns string) string     { return "aide-" + ns + ":snap" }
func ContainerName(ns string) string { return "aide-" + ns }
func CredsVolume(ns string) string   { return "aide-" + ns + "-creds" }
func CacheVolume(ns string) string   { return "aide-" + ns + "-cache" }

// Manager runs commands. Confirm asks the user; a nil Confirm answers no.
type Manager struct {
	Store   *config.Store
	Docker  *docker.Client
	Plat    platform.Platform
	Confirm func(prompt string) bool
	Out     io.Writer
	Err     io.Writer
	// FlattenThreshold is the layer count above which a snapshot is flattened;
	// 0 means the default of 100. AIDE_FLATTEN_THRESHOLD overrides it.
	FlattenThreshold int
}

// Summary is one row of `ns list`.
type Summary struct {
	Name   string
	Phase  Phase
	Port   int
	Mounts int
	Stacks []string
}

// Options shared by verbs that may prompt or force.
type Options struct {
	Yes bool
}

func (m *Manager) Observe(ctx context.Context, ns string, st *config.State) (Phase, error) {
	return NoImage, errors.New("not implemented")
}
func (m *Manager) New(ctx context.Context, ns string, aide io.Reader, home string) error {
	return errors.New("not implemented")
}
func (m *Manager) Clone(ctx context.Context, from, to string, o Options) error {
	return errors.New("not implemented")
}
func (m *Manager) Remove(ctx context.Context, ns string, keepVolumes bool, o Options) error {
	return errors.New("not implemented")
}
func (m *Manager) List(ctx context.Context) ([]Summary, error) {
	return nil, errors.New("not implemented")
}
func (m *Manager) AddStacks(ctx context.Context, ns string, names []string) error {
	return errors.New("not implemented")
}
func (m *Manager) Build(ctx context.Context, ns string, noCache bool) error {
	return errors.New("not implemented")
}
func (m *Manager) Start(ctx context.Context, ns string, o Options) error {
	return errors.New("not implemented")
}
func (m *Manager) Stop(ctx context.Context, ns string) error { return errors.New("not implemented") }
func (m *Manager) Reset(ctx context.Context, ns string, o Options) error {
	return errors.New("not implemented")
}
func (m *Manager) Mount(ctx context.Context, ns string, pairs []config.Mount, home string, o Options) error {
	return errors.New("not implemented")
}
func (m *Manager) Umount(ctx context.Context, ns string, containerPaths []string, o Options) error {
	return errors.New("not implemented")
}

// State returns the configuration as .aide text.
func (m *Manager) State(ctx context.Context, ns string) (string, error) {
	return "", errors.New("not implemented")
}

// Dockerfile returns the Dockerfile that build would use.
func (m *Manager) Dockerfile(ctx context.Context, ns string) ([]byte, error) {
	return nil, errors.New("not implemented")
}
