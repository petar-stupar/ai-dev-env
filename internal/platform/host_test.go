package platform

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petar-stupar/ai-dev-env/internal/docker"
)

type fakeInfo struct{ info *docker.Info }

func (f fakeInfo) Info(context.Context) (*docker.Info, error) { return f.info, nil }

type fakePorts struct {
	ports []int
	err   error
}

func (f fakePorts) PublishedPorts(context.Context) ([]int, error) { return f.ports, f.err }

func mk(t *testing.T, info *docker.Info, home, goos string, env map[string]string, ports portsSource) *Host {
	t.Helper()
	h, err := detect(context.Background(), fakeInfo{info}, ports, func(k string) string { return env[k] }, home, goos)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestKind(t *testing.T) {
	tests := []struct {
		name string
		info docker.Info
		env  map[string]string
		goos string
		want Kind
	}{
		{"desktop", docker.Info{OperatingSystem: "Docker Desktop"}, nil, "darwin", DockerDesktop},
		{"colima name", docker.Info{Name: "colima"}, nil, "darwin", Colima},
		{"colima ctx", docker.Info{}, map[string]string{"DOCKER_CONTEXT": "colima-dev"}, "darwin", Colima},
		{"colima host", docker.Info{}, map[string]string{"DOCKER_HOST": "unix:///Users/a/.colima/default/docker.sock"}, "darwin", Colima},
		{"orbstack name", docker.Info{Name: "orbstack"}, nil, "darwin", OrbStack},
		{"orbstack host", docker.Info{}, map[string]string{"DOCKER_HOST": "unix:///Users/a/.orbstack/run/docker.sock"}, "darwin", OrbStack},
		{"linux", docker.Info{OperatingSystem: "Ubuntu"}, nil, "linux", Linux},
		{"unknown", docker.Info{OperatingSystem: "x"}, nil, "windows", Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mk(t, &tt.info, "/h", tt.goos, tt.env, nil).Kind(); got != tt.want {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}

func writeSettings(t *testing.T, home, name, body string) {
	t.Helper()
	d := filepath.Join(home, "Library", "Group Containers", "group.com.docker")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckSharedFormats(t *testing.T) {
	for _, tt := range []struct{ file, body string }{
		{"settings-store.json", `{"FilesharingDirectories":["/Users","/work"]}`},
		{"settings.json", `{"filesharingDirectories":["/Users","/work"]}`},
	} {
		t.Run(tt.file, func(t *testing.T) {
			home := t.TempDir()
			writeSettings(t, home, tt.file, tt.body)
			h := mk(t, &docker.Info{OperatingSystem: "Docker Desktop"}, home, "darwin", nil, nil)
			if err := h.CheckShared("/work/proj"); err != nil {
				t.Fatal(err)
			}
			if err := h.CheckShared("/Users"); err != nil {
				t.Fatal(err)
			}
			err := h.CheckShared("/opt/x")
			if err == nil || !strings.Contains(err.Error(), "/opt/x is outside Docker Desktop's file sharing list (/Users, /work)") ||
				!strings.Contains(err.Error(), "Settings > Resources > File sharing") {
				t.Fatalf("err = %v", err)
			}
			if len(h.Warnings()) != 0 {
				t.Fatalf("warnings: %v", h.Warnings())
			}
		})
	}
}

func TestCheckSharedDefaults(t *testing.T) {
	h := mk(t, &docker.Info{OperatingSystem: "Docker Desktop"}, t.TempDir(), "darwin", nil, nil)
	if err := h.CheckShared("/Users/a/src"); err != nil {
		t.Fatal(err)
	}
	if err := h.CheckShared("/Users2/x"); err == nil {
		t.Fatal("prefix trap: /Users2/x must not be under /Users")
	}
	if err := h.CheckShared("/opt"); err == nil {
		t.Fatal("want error")
	}
	if len(h.Warnings()) == 0 {
		t.Fatal("want warning")
	}
}

func TestCheckSharedOthers(t *testing.T) {
	c := mk(t, &docker.Info{Name: "colima"}, "/Users/a", "darwin", nil, nil)
	if err := c.CheckShared("/Users/a/x"); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckShared("/Users/ab/x"); err == nil || !strings.Contains(err.Error(), "mounts") {
		t.Fatalf("err = %v", err)
	}
	for _, info := range []*docker.Info{{Name: "orbstack"}, {OperatingSystem: "Ubuntu"}} {
		if err := mk(t, info, "/h", "linux", nil, nil).CheckShared("/anything"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFreePort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l.Close()

	h := mk(t, &docker.Info{}, "/h", "linux", nil, fakePorts{ports: []int{8081}})
	// Occupy 8083 if possible.
	busy, err := net.Listen("tcp", "127.0.0.1:8083")
	if err != nil {
		t.Skip("8083 not available")
	}
	defer busy.Close()
	got, err := h.FreePort([]int{8080, 8082})
	if err != nil {
		t.Fatal(err)
	}
	if got < 8084 && got != 8084 {
		// 8080 excluded, 8081 published, 8082 excluded, 8083 listening.
		t.Fatalf("got %d, want >= 8084", got)
	}
	if got != 8084 {
		t.Logf("8084 busy on host, got %d", got)
	}
}

func TestFreePortPortsError(t *testing.T) {
	h := mk(t, &docker.Info{}, "/h", "linux", nil, fakePorts{err: errors.New("boom")})
	if _, err := h.FreePort(nil); err != nil {
		t.Fatal(err)
	}
	if len(h.Warnings()) != 1 {
		t.Fatalf("warnings: %v", h.Warnings())
	}
}

func TestFreePortExhausted(t *testing.T) {
	var all []int
	for p := 8080; p <= 8199; p++ {
		all = append(all, p)
	}
	h := mk(t, &docker.Info{}, "/h", "linux", nil, nil)
	_, err := h.FreePort(all)
	if err == nil || err.Error() != "no free port between 8080 and 8199" {
		t.Fatalf("err = %v", err)
	}
}

func TestFilterSecurityOpt(t *testing.T) {
	in := []string{"apparmor=unconfined", "seccomp=unconfined", "no-new-privileges"}
	without := mk(t, &docker.Info{SecurityOptions: []string{"name=seccomp,profile=builtin"}}, "/h", "linux", nil, nil)
	got := without.FilterSecurityOpt(in)
	if strings.Join(got, " ") != "seccomp=unconfined no-new-privileges" {
		t.Fatalf("got %v", got)
	}
	with := mk(t, &docker.Info{SecurityOptions: []string{"name=apparmor", "name=seccomp,profile=builtin"}}, "/h", "linux", nil, nil)
	if got := with.FilterSecurityOpt(in); len(got) != 3 {
		t.Fatalf("got %v", got)
	}
}
