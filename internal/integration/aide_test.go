// Package integration drives the real aide binary against a real Docker
// daemon. It is opt-in: set AIDE_INTEGRATION=1.
//
//	AIDE_INTEGRATION=1 go test ./internal/integration -v -timeout 40m
package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var aideBin string

func TestMain(m *testing.M) {
	if os.Getenv("AIDE_INTEGRATION") != "1" {
		os.Exit(m.Run()) // every test skips itself
	}
	dir, err := os.MkdirTemp("", "aide-integration-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	aideBin = filepath.Join(dir, "aide")
	build := exec.Command("go", "build", "-o", aideBin, "../../cmd/aide")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build aide:", err)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// harness runs aide with a private config root.
type harness struct {
	t      *testing.T
	config string // XDG_CONFIG_HOME
	home   string
}

func (h *harness) aide(timeout time.Duration, extraEnv []string, args ...string) (string, error) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, aideBin, args...)
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+h.config, "HOME="+h.home)
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.Stdin = nil
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	h.t.Logf("$ aide %s\n%s", strings.Join(args, " "), tail(buf.String(), 40))
	return buf.String(), err
}

func (h *harness) must(timeout time.Duration, extraEnv []string, args ...string) string {
	h.t.Helper()
	out, err := h.aide(timeout, extraEnv, args...)
	if err != nil {
		h.t.Fatalf("aide %s: %v\n%s", strings.Join(args, " "), err, tail(out, 80))
	}
	return out
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = append([]string{fmt.Sprintf("... (%d lines elided)", len(lines)-n)}, lines[len(lines)-n:]...)
	}
	return strings.Join(lines, "\n")
}

func docker(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func mustDocker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := docker(args...)
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

type state struct {
	Port          int `json:"port"`
	Snapshot      string
	AppliedMounts []struct {
		Host      string `json:"host"`
		Container string `json:"container"`
	} `json:"appliedMounts"`
}

func (h *harness) state(ns string) state {
	h.t.Helper()
	b, err := os.ReadFile(filepath.Join(h.config, "aide", "namespaces", ns, "state.json"))
	if err != nil {
		h.t.Fatalf("read state: %v", err)
	}
	var s state
	if err := json.Unmarshal(b, &s); err != nil {
		h.t.Fatalf("parse state: %v", err)
	}
	return s
}

// waitHTTP polls code-server until it answers 200 or 302.
func waitHTTP(t *testing.T, port int, within time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	client := &http.Client{
		Timeout:       2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	var last string
	for time.Since(start) < within {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 || resp.StatusCode == 302 {
				return time.Since(start)
			}
			last = resp.Status
		} else {
			last = err.Error()
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("code-server on %s did not answer within %s: %s", url, within, last)
	return 0
}

func execIn(ns string, args ...string) (string, error) {
	return docker(append([]string{"exec", "-u", "agent", "aide-" + ns}, args...)...)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

// forceClean removes everything aide may have created for ns.
func forceClean(ns string) {
	c := "aide-" + ns
	_, _ = docker("rm", "-f", c)
	_, _ = docker("rmi", "-f", c+":snap", c+":base")
	_, _ = docker("volume", "rm", "-f", c+"-creds", c+"-cache")
}

// timing extracts "<what> in 1.2s" from aide output.
func timing(out, what string) string {
	for _, l := range strings.Split(out, "\n") {
		if i := strings.Index(l, what+" in "); i >= 0 {
			return strings.TrimSpace(l[i:])
		}
	}
	return what + ": (not printed)"
}

func TestLifecycle(t *testing.T) {
	if os.Getenv("AIDE_INTEGRATION") != "1" {
		t.Skip("set AIDE_INTEGRATION=1 to run against a real Docker daemon")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, config: t.TempDir(), home: home}
	ns := "aidetest-" + randHex(6)
	clone := ns + "-c"
	t.Cleanup(func() { forceClean(ns); forceClean(clone) })

	// The host directories are resolved so they match the state's symlink-free paths.
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dirA, dirB := filepath.Join(tmp, "a"), filepath.Join(tmp, "b")
	for _, d := range []string{dirA, dirB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dirA, "hello.txt"), []byte("from a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirB, "world.txt"), []byte("from b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	aideFile := filepath.Join(tmp, "x.aide")
	if err := os.WriteFile(aideFile, []byte("aide ns:x stack base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const (
		wsA    = "/home/agent/workspace/a"
		wsB    = "/home/agent/workspace/b"
		marker = "/home/agent/marker"
	)
	min := time.Minute

	// new
	out := h.must(min, nil, "ns", "new", ns, aideFile)
	if !strings.Contains(out, "created namespace "+ns) {
		t.Fatalf("new: unexpected output\n%s", out)
	}

	// build
	t0 := time.Now()
	h.must(10*min, nil, "ns:"+ns, "build")
	t.Logf("TIMING build: %s", time.Since(t0).Round(time.Second))
	mustDocker(t, "image", "inspect", "aide-"+ns+":base")

	// start
	t0 = time.Now()
	out = h.must(2*min, nil, "ns:"+ns, "start")
	port := h.state(ns).Port
	if port == 0 || !strings.Contains(out, fmt.Sprintf("http://127.0.0.1:%d", port)) {
		t.Fatalf("start: port %d not in output\n%s", port, out)
	}
	up := waitHTTP(t, port, min)
	t.Logf("TIMING first start (run + code-server up): %s (http after %s)", time.Since(t0).Round(100*time.Millisecond), up.Round(100*time.Millisecond))

	// mount a while running (no mounts yet, so this is a remount)
	out = h.must(5*min, nil, "ns:"+ns, "mount", dirA, wsA, "--yes")
	t.Logf("TIMING mount a: %s, %s, %s", timing(out, "stopped"), timing(out, "committed"), timing(out, "recreated"))
	if got, err := execIn(ns, "cat", wsA+"/hello.txt"); err != nil || got != "from a" {
		t.Fatalf("cat through mount a: %q %v", got, err)
	}
	if _, err := execIn(ns, "touch", marker); err != nil {
		t.Fatalf("touch marker: %v", err)
	}

	// mount b: remount while running keeps the marker
	out = h.must(5*min, nil, "ns:"+ns, "mount", dirB, wsB, "--yes")
	t.Logf("TIMING mount b: %s, %s, %s", timing(out, "stopped"), timing(out, "committed"), timing(out, "recreated"))
	if _, err := execIn(ns, "test", "-e", marker); err != nil {
		t.Fatalf("marker lost after the second mount: %v", err)
	}
	if got, err := execIn(ns, "cat", wsA+"/hello.txt", wsB+"/world.txt"); err != nil || got != "from a\nfrom b" {
		t.Fatalf("both mounts: %q %v", got, err)
	}
	if n := len(h.state(ns).AppliedMounts); n != 2 {
		t.Fatalf("appliedMounts = %d, want 2", n)
	}

	// umount a
	h.must(5*min, nil, "ns:"+ns, "umount", wsA, "--yes")
	if got, _ := execIn(ns, "sh", "-c", "ls -A "+wsA+" 2>/dev/null"); got != "" {
		t.Fatalf("%s still has content after umount: %q", wsA, got)
	}
	if _, err := execIn(ns, "test", "-e", marker); err != nil {
		t.Fatalf("marker lost after umount: %v", err)
	}
	if n := len(h.state(ns).AppliedMounts); n != 1 {
		t.Fatalf("appliedMounts = %d, want 1", n)
	}

	// umount b with a flatten threshold of 1
	out = h.must(10*min, []string{"AIDE_FLATTEN_THRESHOLD=1"}, "ns:"+ns, "umount", wsB, "--yes")
	t.Logf("TIMING flatten remount: %s, %s, %s, %s", timing(out, "stopped"), timing(out, "committed"), timing(out, "into 1"), timing(out, "recreated"))
	if !strings.Contains(out, "flattened ") {
		t.Fatalf("no flatten reported\n%s", out)
	}
	if got := mustDocker(t, "image", "inspect", "-f", "{{len .RootFS.Layers}}", "aide-"+ns+":snap"); got != "1" {
		t.Fatalf("snapshot layers = %s, want 1", got)
	}
	if got := mustDocker(t, "inspect", "-f", "{{.State.Running}}", "aide-"+ns); got != "true" {
		t.Fatalf("container running = %s after flatten", got)
	}
	waitHTTP(t, port, min)
	if got := mustDocker(t, "inspect", "-f", "{{json .Config.Entrypoint}}", "aide-"+ns); got != `["/etc/aide/entrypoint.sh"]` {
		t.Fatalf("entrypoint after flatten = %s", got)
	}
	if got := mustDocker(t, "inspect", "-f", "{{json .Config.Env}}", "aide-"+ns); !strings.Contains(got, "AIDE_STACKS=") {
		t.Fatalf("env after flatten lacks AIDE_STACKS: %s", got)
	}
	if _, err := execIn(ns, "test", "-e", marker); err != nil {
		t.Fatalf("marker lost after flatten: %v", err)
	}

	// stop, then start through `docker start`
	t0 = time.Now()
	h.must(2*min, nil, "ns:"+ns, "stop")
	stopTook := time.Since(t0)
	t.Logf("TIMING aide stop: %s", stopTook.Round(100*time.Millisecond))
	if stopTook > 10*time.Second {
		t.Errorf("stop took %s; the entrypoint should exit within a few seconds", stopTook)
	}
	if got := mustDocker(t, "inspect", "-f", "{{.State.Running}}", "aide-"+ns); got != "false" {
		t.Fatalf("running = %s after stop", got)
	}
	h.must(2*min, nil, "ns:"+ns, "start")
	waitHTTP(t, port, min)
	if _, err := execIn(ns, "test", "-e", marker); err != nil {
		t.Fatalf("marker lost after stop/start: %v", err)
	}

	// reset drops the marker
	h.must(2*min, nil, "ns:"+ns, "reset", "--yes")
	if _, err := docker("image", "inspect", "aide-"+ns+":snap"); err == nil {
		t.Fatalf("snapshot survived reset")
	}
	h.must(2*min, nil, "ns:"+ns, "start")
	waitHTTP(t, port, min)
	if _, err := execIn(ns, "test", "-e", marker); err == nil {
		t.Fatalf("marker survived reset")
	}

	// list and state
	out = h.must(min, nil, "ns", "list")
	if !strings.Contains(out, ns) {
		t.Fatalf("ns list lacks %s\n%s", ns, out)
	}
	out = h.must(min, nil, "ns:"+ns, "state")
	if !strings.Contains(out, "stack base") {
		t.Fatalf("state lacks the stack line\n%s", out)
	}

	// clone (running source, --yes commits it paused), then remove the clone
	h.must(5*min, nil, "ns", "clone", ns, clone, "--yes")
	mustDocker(t, "image", "inspect", "aide-"+clone+":base")
	if p := h.state(clone).Port; p == port || p == 0 {
		t.Fatalf("clone port = %d, source %d", p, port)
	}
	h.must(2*min, nil, "ns", "remove", clone, "--yes")

	// remove
	h.must(2*min, nil, "ns", "remove", ns, "--yes")
	for _, n := range []string{ns, clone} {
		c := "aide-" + n
		if _, err := docker("container", "inspect", c); err == nil {
			t.Errorf("container %s survived remove", c)
		}
		for _, img := range []string{c + ":base", c + ":snap"} {
			if _, err := docker("image", "inspect", img); err == nil {
				t.Errorf("image %s survived remove", img)
			}
		}
		for _, v := range []string{c + "-creds", c + "-cache"} {
			if _, err := docker("volume", "inspect", v); err == nil {
				t.Errorf("volume %s survived remove", v)
			}
		}
		if _, err := os.Stat(filepath.Join(h.config, "aide", "namespaces", n)); !os.IsNotExist(err) {
			t.Errorf("state directory of %s survived remove: %v", n, err)
		}
	}
}
