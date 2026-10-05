package namespace

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/petar-stupar/ai-dev-env/internal/aidefile"
	"github.com/petar-stupar/ai-dev-env/internal/config"
	"github.com/petar-stupar/ai-dev-env/internal/platform"
	"github.com/petar-stupar/ai-dev-env/internal/stacks"
)

// ProtectedPaths are container paths a mount may not equal, sit inside, or
// contain: the two volumes, the 9P mount roots and system directories.
//
// The check is on the path as written, so the top-level names that are
// symlinks into /usr on the base image (/bin, /sbin, /lib, /lib64) are listed
// too, and so are the agent's own configuration directories: a mount there
// would replace the policy files and the tools the image ships.
var ProtectedPaths = []string{
	stacks.CredsDir,
	stacks.CacheDir,
	stacks.AgentHome + "/mnt",
	stacks.AgentHome + "/.config",
	stacks.AgentHome + "/.local",
	stacks.AgentHome + "/.claude",
	stacks.AgentHome + "/.opencode",
	"/etc", "/usr", "/proc", "/sys", "/dev",
	"/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32",
	"/boot", "/opt", "/root", "/run", "/var",
}

// within reports whether p is dir or inside it (both clean and absolute).
func within(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, dir+"/")
}

// CleanContainerPath checks and cleans a container mount path.
func CleanContainerPath(p string) (string, error) {
	if !strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("container path must be absolute: %q", p)
	}
	c := path.Clean(p)
	if c == "/" {
		return "", fmt.Errorf("container path cannot be /")
	}
	for _, prot := range ProtectedPaths {
		if within(c, prot) {
			return "", fmt.Errorf("container path %s is inside %s, which aide manages", c, prot)
		}
		if within(prot, c) {
			return "", fmt.Errorf("container path %s would hide %s, which aide manages", c, prot)
		}
	}
	return c, nil
}

// CleanHostPath expands ~, makes the path absolute, resolves symlinks and
// checks that it is a directory the Docker host can share.
func CleanHostPath(p, home string, plat platform.Platform) (string, error) {
	h, err := aidefile.ExpandHome(p, home)
	if err != nil {
		return "", err
	}
	h, err = filepath.Abs(h)
	if err != nil {
		return "", fmt.Errorf("host path %q: %w", p, err)
	}
	r, err := filepath.EvalSymlinks(h)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("host path %s does not exist", h)
		}
		return "", fmt.Errorf("host path %s: %w", h, err)
	}
	fi, err := os.Stat(r)
	if err != nil {
		return "", fmt.Errorf("host path %s: %w", h, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("host path %s is not a directory", h)
	}
	if plat != nil {
		if err := plat.CheckShared(r); err != nil {
			return "", err
		}
	}
	return r, nil
}

// dockerSockets are where a Docker daemon's control socket usually lives. A
// container that can write to one controls the host.
var dockerSockets = []string{"/var/run/docker.sock", "/run/docker.sock"}

// validateMounts is the package function plus the host paths aide never
// shares: its own configuration (it holds every namespace's passwords) and
// any directory that holds the Docker socket.
func (m *Manager) validateMounts(pairs []config.Mount, home string) ([]config.Mount, error) {
	valid, err := validateMounts(pairs, home, m.Plat)
	if err != nil {
		return nil, err
	}
	root := m.Store.Root
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	socks := append([]string{}, dockerSockets...)
	if home != "" {
		socks = append(socks, filepath.Join(home, ".docker", "run", "docker.sock"))
	}
	for _, v := range valid {
		if within(v.Host, root) || within(root, v.Host) {
			return nil, fmt.Errorf("mount %s %s: the host path holds aide's own configuration (%s), which stores the passwords of every namespace", v.Host, v.Container, root)
		}
		for _, s := range socks {
			if r, err := filepath.EvalSymlinks(filepath.Dir(s)); err == nil {
				s = filepath.Join(r, filepath.Base(s))
			}
			if within(s, v.Host) {
				return nil, fmt.Errorf("mount %s %s: the host path holds the Docker socket (%s); a container that can reach it controls the host", v.Host, v.Container, s)
			}
		}
	}
	return valid, nil
}

// validateMounts cleans every pair and rejects a container path given twice.
// Nothing is touched; the result is ready to merge.
func validateMounts(pairs []config.Mount, home string, plat platform.Platform) ([]config.Mount, error) {
	seen := map[string]bool{}
	out := make([]config.Mount, 0, len(pairs))
	for _, p := range pairs {
		c, err := CleanContainerPath(p.Container)
		if err != nil {
			return nil, fmt.Errorf("mount %s %s: %w", p.Host, p.Container, err)
		}
		if seen[c] {
			return nil, fmt.Errorf("mount %s %s: container path %s is given twice", p.Host, p.Container, c)
		}
		seen[c] = true
		h, err := CleanHostPath(p.Host, home, plat)
		if err != nil {
			return nil, fmt.Errorf("mount %s %s: %w", p.Host, p.Container, err)
		}
		out = append(out, config.Mount{Host: h, Container: c})
	}
	return out, nil
}

// mergeMounts applies validated pairs to cur: a container path already
// mounted gets the new host, a new one is appended.
func mergeMounts(cur, add []config.Mount) []config.Mount {
	out := append([]config.Mount{}, cur...)
	for _, a := range add {
		replaced := false
		for i := range out {
			if out[i].Container == a.Container {
				out[i].Host = a.Host
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, a)
		}
	}
	return out
}

func containsAny(s string, subs ...string) bool {
	s = strings.ToLower(s)
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}
