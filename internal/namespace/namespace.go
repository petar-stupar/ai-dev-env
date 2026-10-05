// Package namespace implements aide's commands over one namespace: the phase
// machine, build, start, stop, reset, mount/umount with remount, clone, remove.
//
// Every verb that changes a namespace takes the namespace's lock itself
// (config.Store.Lock), so callers must not hold it.
package namespace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/petar-stupar/ai-dev-env/internal/config"
	"github.com/petar-stupar/ai-dev-env/internal/docker"
	"github.com/petar-stupar/ai-dev-env/internal/platform"
	"github.com/petar-stupar/ai-dev-env/internal/stacks"
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

// LabelNamespace labels every image and container aide creates.
const LabelNamespace = "aide.namespace"

// ProxyURL is the allowlist proxy inside a container whose network is
// restricted, and NetworkScript the root-only script that manages it.
const (
	ProxyURL      = "http://127.0.0.1:3128"
	NetworkScript = "/usr/local/lib/aide/aide-network.sh"
)

// proxyVars are the variables that send a tool to the proxy; no_proxy and
// NO_PROXY go with them.
var proxyVars = []string{"http_proxy", "https_proxy", "HTTP_PROXY", "HTTPS_PROXY", "no_proxy", "NO_PROXY"}

// StopTimeout is the grace period, in seconds, for `docker stop` and the
// container's --stop-timeout.
const StopTimeout = 30

// DefaultFlattenThreshold is the snapshot layer count above which a remount
// flattens the snapshot.
const DefaultFlattenThreshold = 100

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
	// Catalog loads the stack catalog; nil means stacks.Catalog. Tests set it
	// to a synthetic catalog.
	Catalog func() (map[string]*stacks.Stack, error)
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

func (m *Manager) out() io.Writer {
	if m.Out == nil {
		return io.Discard
	}
	return m.Out
}

func (m *Manager) errw() io.Writer {
	if m.Err == nil {
		return io.Discard
	}
	return m.Err
}

func (m *Manager) printf(format string, a ...any) { fmt.Fprintf(m.out(), format+"\n", a...) }
func (m *Manager) warnf(format string, a ...any) {
	fmt.Fprintf(m.errw(), "warning: "+format+"\n", a...)
}

func (m *Manager) confirm(prompt string) bool { return m.Confirm != nil && m.Confirm(prompt) }

func (m *Manager) catalog() (map[string]*stacks.Stack, error) {
	if m.Catalog != nil {
		return m.Catalog()
	}
	return stacks.Catalog()
}

func (m *Manager) threshold() int {
	if m.FlattenThreshold > 0 {
		return m.FlattenThreshold
	}
	return DefaultFlattenThreshold
}

// resolve resolves the namespace's selected stacks and prints the warnings.
func (m *Manager) resolve(ns string, names []string) (*stacks.Resolution, error) {
	cat, err := m.catalog()
	if err != nil {
		return nil, fmt.Errorf("load the stack catalog: %w", err)
	}
	res, err := stacks.Resolve(cat, names)
	if err != nil {
		return nil, fmt.Errorf("namespace %s: %w", ns, err)
	}
	for _, w := range res.Warnings {
		m.warnf("%s", w)
	}
	return res, nil
}

// open validates the name, takes the namespace lock and loads its state.
func (m *Manager) open(ns string) (*config.State, func(), error) {
	if err := config.ValidName(ns); err != nil {
		return nil, nil, err
	}
	ok, err := m.Store.Exists(ns)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("namespace %s does not exist; create it with \"aide ns new %s\": %w", ns, ns, config.ErrNoNamespace)
	}
	unlock, err := m.Store.Lock(ns)
	if err != nil {
		return nil, nil, err
	}
	st, err := m.Store.Load(ns)
	if err != nil {
		unlock()
		return nil, nil, err
	}
	return st, unlock, nil
}

// Observe works out the phase from Docker and repairs drift in st: a
// snapshot whose image is gone is forgotten, and AppliedMounts is cleared
// when there is no container. A repaired state is saved, so the caller must
// hold the namespace lock.
func (m *Manager) Observe(ctx context.Context, ns string, st *config.State) (Phase, error) {
	phase, changed, err := m.observe(ctx, ns, st)
	if err != nil {
		return phase, err
	}
	if changed {
		if err := m.Store.Save(ns, st); err != nil {
			return phase, err
		}
	}
	return phase, nil
}

func (m *Manager) observe(ctx context.Context, ns string, st *config.State) (Phase, bool, error) {
	changed := false
	_, err := m.Docker.Image(ctx, ImageBase(ns))
	baseMissing := errors.Is(err, docker.ErrNotFound)
	if err != nil && !baseMissing {
		return NoImage, false, fmt.Errorf("inspect image %s: %w", ImageBase(ns), err)
	}
	if st.Snapshot != "" {
		_, err := m.Docker.Image(ctx, ImageSnap(ns))
		switch {
		case errors.Is(err, docker.ErrNotFound):
			m.warnf("snapshot %s is gone", ImageSnap(ns))
			st.Snapshot = ""
			changed = true
		case err != nil:
			return NoImage, false, fmt.Errorf("inspect image %s: %w", ImageSnap(ns), err)
		}
	}
	noContainer := func() {
		if st.AppliedMounts != nil {
			st.AppliedMounts = nil
			changed = true
		}
	}
	// The container decides the phase even when the base tag is gone (docker
	// lets a tag go while a container runs from the snapshot): reporting "no
	// image" then would leave a running container that no verb can stop.
	c, err := m.Docker.Container(ctx, ContainerName(ns))
	if errors.Is(err, docker.ErrNotFound) {
		noContainer()
		if baseMissing {
			return NoImage, changed, nil
		}
		return Built, changed, nil
	}
	if err != nil {
		return NoImage, false, fmt.Errorf("inspect container %s: %w", ContainerName(ns), err)
	}
	if got := c.Labels[LabelNamespace]; got != ns {
		return NoImage, false, fmt.Errorf("container %s does not carry the label %s=%s, so aide did not create it for this namespace; aide will not touch it. Remove or rename that container, or use another namespace name", ContainerName(ns), LabelNamespace, ns)
	}
	if baseMissing {
		m.warnf("image %s is gone but container %s still exists", ImageBase(ns), ContainerName(ns))
	}
	if c.Running {
		return Running, changed, nil
	}
	return Stopped, changed, nil
}

// refuse is the error for a verb the availability table does not allow.
func refuse(ns, what string, p Phase, hint string) error {
	return fmt.Errorf("cannot %s while %s; %s", what, phaseClause(p), fmt.Sprintf(hint, ns))
}

func phaseClause(p Phase) string {
	switch p {
	case Stopped, Running:
		return "a container exists (" + p.String() + ")"
	case Built:
		return "the namespace is built and has no container"
	}
	return "the namespace has no image"
}

const (
	hintReset = `run "aide ns:%s reset" first`
	hintBuild = `run "aide ns:%s build" first`
)

func since(t time.Time) string { return fmt.Sprintf("%.1fs", time.Since(t).Seconds()) }

func cloneMounts(ms []config.Mount) []config.Mount {
	if ms == nil {
		return nil
	}
	return append([]config.Mount{}, ms...)
}

func mountsEqual(a, b []config.Mount) bool { return slices.Equal(a, b) }

// runSpec is the `docker run` shared by start and remount.
func (m *Manager) runSpec(ns string, st *config.State, res *stacks.Resolution, image string, mounts []config.Mount) docker.RunSpec {
	req := res.Run()
	env := map[string]string{}
	for k, v := range req.Env {
		env[k] = v
	}
	env["CODE_SERVER_PASSWORD"] = st.Password
	if st.SudoPassword != "" {
		env["AIDE_SUDO_PASSWORD"] = st.SudoPassword
	}
	capAdd := req.CapAdd
	// Always set, never left out: a snapshot image keeps the environment of
	// the container it was committed from, so a variable that is merely
	// absent here would come back from the snapshot, and a namespace switched
	// to an open network would start as an allowlist one without NET_ADMIN.
	env["AIDE_NETWORK"] = config.NetworkOpen
	for _, k := range proxyVars {
		env[k] = ""
	}
	if st.Restricted() {
		// The entrypoint installs the firewall rules that leave the proxy as
		// the only way out, which takes NET_ADMIN; the variables send every
		// tool that honours them to the proxy.
		capAdd = append(append([]string{}, capAdd...), "NET_ADMIN")
		slices.Sort(capAdd)
		capAdd = slices.Compact(capAdd)
		env["AIDE_NETWORK"] = config.NetworkAllowlist
		for _, k := range proxyVars {
			env[k] = ProxyURL
		}
		env["no_proxy"], env["NO_PROXY"] = "localhost,127.0.0.1", "localhost,127.0.0.1"
	}
	spec := docker.RunSpec{
		Name:        ContainerName(ns),
		Hostname:    ns,
		Image:       image,
		Publish:     fmt.Sprintf("127.0.0.1:%d:%d", st.Port, stacks.CodeServerPort),
		CapAdd:      capAdd,
		SecurityOpt: req.SecurityOpt,
		Devices:     req.Devices,
		Env:         env,
		Labels:      map[string]string{LabelNamespace: ns},
		StopTimeout: StopTimeout,
		Init:        true,
		Mounts: []docker.MountSpec{
			{Type: "volume", Source: CredsVolume(ns), Target: stacks.CredsDir},
			{Type: "volume", Source: CacheVolume(ns), Target: stacks.CacheDir},
		},
	}
	if m.Plat != nil && len(spec.SecurityOpt) > 0 {
		spec.SecurityOpt = m.Plat.FilterSecurityOpt(spec.SecurityOpt)
	}
	for _, mt := range mounts {
		spec.Mounts = append(spec.Mounts, docker.MountSpec{Type: "bind", Source: mt.Host, Target: mt.Container})
	}
	return spec
}

// ensureSudoPassword gives a namespace created by an older aide the password
// its next container asks for on sudo. The caller saves st.
func ensureSudoPassword(st *config.State) error {
	if st.SudoPassword != "" {
		return nil
	}
	pw, err := config.NewPassword()
	if err != nil {
		return err
	}
	st.SudoPassword = pw
	return nil
}

// printAccess tells the user how to reach a running namespace.
func (m *Manager) printAccess(ns string, st *config.State) {
	m.printf("  url:      http://127.0.0.1:%d", st.Port)
	m.printf("  password: %s", st.Password)
	if st.SudoPassword != "" {
		m.printf("  sudo:     %s", st.SudoPassword)
	}
	m.printf("  shell:    docker exec -it -u agent %s bash -l", ContainerName(ns))
	if st.Restricted() {
		m.printf("  network:  allowlist (the stacks' hosts plus %d of your own; \"aide ns:%s allow <host>\" adds one)", len(st.Allow), ns)
	} else {
		m.printf("  network:  open")
	}
}

// dropImage removes an image that is no longer needed; a missing image or
// one that later snapshots still build on is left alone silently.
func (m *Manager) dropImage(ctx context.Context, ref string) {
	// ref is an image ID. A clone shares its source's snapshot image, so the
	// ID may be all that stands behind another namespace's :snap tag, and
	// removing an image by ID takes a lone tag with it.
	info, err := m.Docker.Image(ctx, ref)
	if errors.Is(err, docker.ErrNotFound) {
		return
	}
	if err != nil {
		m.warnf("could not inspect old image %s: %v; leaving it", ref, err)
		return
	}
	if len(info.Tags) > 0 {
		return
	}
	err = m.Docker.RemoveImage(ctx, ref)
	if err == nil || errors.Is(err, docker.ErrNotFound) {
		return
	}
	var ee *docker.ExitError
	if errors.As(err, &ee) && containsAny(ee.Stderr, "dependent child", "being used", "is using") {
		return
	}
	m.warnf("could not remove old image %s: %v", ref, err)
}

// briefError shows only docker's stderr for a failed docker command, whose
// full argv is long and already known from context; it still unwraps to the
// original error.
type briefError struct{ err error }

func (b briefError) Error() string {
	var ee *docker.ExitError
	if errors.As(b.err, &ee) && strings.TrimSpace(ee.Stderr) != "" {
		return strings.TrimSpace(ee.Stderr)
	}
	return b.err.Error()
}

func (b briefError) Unwrap() error { return b.err }

func brief(err error) error {
	if err == nil {
		return nil
	}
	return briefError{err}
}
