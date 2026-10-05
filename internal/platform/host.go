package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/petar-stupar/ai-dev-env/internal/docker"
)

type infoer interface {
	Info(ctx context.Context) (*docker.Info, error)
}

type portsSource interface {
	PublishedPorts(ctx context.Context) ([]int, error)
}

var defaultShared = []string{"/Users", "/Volumes", "/private", "/tmp", "/var/folders"}

// Host is the Platform implementation backed by `docker info`.
type Host struct {
	kind     Kind
	info     *docker.Info
	home     string
	env      func(string) string
	ports    portsSource
	ctx      context.Context
	warnings []string
}

var _ Platform = (*Host)(nil)

func detect(ctx context.Context, i infoer, ports portsSource, env func(string) string, home, goos string) (*Host, error) {
	info, err := i.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("docker info: %w", err)
	}
	if env == nil {
		env = func(string) string { return "" }
	}
	h := &Host{info: info, home: home, env: env, ports: ports, ctx: ctx}
	h.kind = classify(info, env, goos)
	return h, nil
}

func classify(info *docker.Info, env func(string) string, goos string) Kind {
	if info.OperatingSystem == "Docker Desktop" {
		return DockerDesktop
	}
	hay := strings.ToLower(strings.Join([]string{info.Name, env("DOCKER_CONTEXT"), env("DOCKER_HOST")}, " "))
	switch {
	case strings.Contains(hay, "colima"):
		return Colima
	case strings.Contains(hay, "orbstack"):
		return OrbStack
	}
	if goos == "linux" {
		return Linux
	}
	return Unknown
}

func (h *Host) Kind() Kind { return h.kind }

// Warnings returns non-fatal problems noticed so far.
func (h *Host) Warnings() []string { return append([]string(nil), h.warnings...) }

func (h *Host) UIDGID() (int, int) { return os.Getuid(), os.Getgid() }

func under(path, dir string) bool {
	path, dir = filepath.Clean(path), filepath.Clean(dir)
	if path == dir {
		return true
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (h *Host) sharedDirs() []string {
	base := filepath.Join(h.home, "Library", "Group Containers", "group.com.docker")
	if b, err := os.ReadFile(filepath.Join(base, "settings-store.json")); err == nil {
		var v struct {
			Dirs []string `json:"FilesharingDirectories"`
		}
		if json.Unmarshal(b, &v) == nil && v.Dirs != nil {
			return v.Dirs
		}
	}
	if b, err := os.ReadFile(filepath.Join(base, "settings.json")); err == nil {
		var v struct {
			Dirs []string `json:"filesharingDirectories"`
		}
		if json.Unmarshal(b, &v) == nil && v.Dirs != nil {
			return v.Dirs
		}
	}
	h.warnings = append(h.warnings, "could not read Docker Desktop file sharing settings; assuming defaults "+strings.Join(defaultShared, ", "))
	return defaultShared
}

func (h *Host) CheckShared(hostPath string) error {
	switch h.kind {
	case DockerDesktop:
		dirs := h.sharedDirs()
		for _, d := range dirs {
			if under(hostPath, d) {
				return nil
			}
		}
		return fmt.Errorf("%s is outside Docker Desktop's file sharing list (%s); add it under Settings > Resources > File sharing",
			hostPath, strings.Join(dirs, ", "))
	case Colima:
		if under(hostPath, h.home) {
			return nil
		}
		return fmt.Errorf("%s is outside Colima's mounted directories (only %s is mounted by default); add it to the mounts setting in colima.yaml (colima start --edit)",
			hostPath, h.home)
	}
	return nil
}

func (h *Host) FreePort(exclude []int) (int, error) {
	skip := map[int]bool{}
	for _, p := range exclude {
		skip[p] = true
	}
	if h.ports != nil {
		ps, err := h.ports.PublishedPorts(h.ctx)
		if err != nil {
			h.warnings = append(h.warnings, "could not list published container ports: "+err.Error())
		}
		for _, p := range ps {
			skip[p] = true
		}
	}
	for p := 8080; p <= 8199; p++ {
		if skip[p] {
			continue
		}
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err != nil {
			continue
		}
		l.Close()
		return p, nil
	}
	return 0, fmt.Errorf("no free port between 8080 and 8199")
}

func (h *Host) FilterSecurityOpt(opts []string) []string {
	has := false
	for _, s := range h.info.SecurityOptions {
		if strings.Contains(s, "apparmor") {
			has = true
		}
	}
	var out []string
	for _, o := range opts {
		if !has && strings.HasPrefix(o, "apparmor=") {
			continue
		}
		out = append(out, o)
	}
	return out
}
