package docker_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petar-stupar/ai-dev-env/internal/docker"
)

// These drive Exec with /bin/sh as the binary so they need no docker.

func TestExecRunCapture(t *testing.T) {
	var verbose bytes.Buffer
	e := &docker.Exec{Bin: "/bin/sh", Verbose: &verbose}
	res, err := e.Run(context.Background(), []string{"-c", "echo out; echo oops >&2; exit 3"}, docker.IO{})
	var ee *docker.ExitError
	if !errors.As(err, &ee) || ee.Code != 3 || ee.Stderr != "oops" {
		t.Fatalf("err = %#v", err)
	}
	if string(res.Stdout) != "out\n" || string(res.Stderr) != "oops\n" || res.Code != 3 {
		t.Errorf("res = %+v", res)
	}
	if verbose.String() != "+ docker -c 'echo out; echo oops >&2; exit 3'\n" {
		t.Errorf("verbose = %q", verbose.String())
	}
}

func TestExecRunStream(t *testing.T) {
	var out bytes.Buffer
	e := &docker.Exec{Bin: "/bin/sh"}
	res, err := e.Run(context.Background(), []string{"-c", "cat; echo e >&2"},
		docker.IO{Stdin: strings.NewReader("in\n"), Stdout: &out, Stderr: &out})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "in\n") || !strings.Contains(out.String(), "e\n") || len(res.Stdout) != 0 {
		t.Errorf("out = %q res = %+v", out.String(), res)
	}
}

func TestExecPipe(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "out")
	e := &docker.Exec{Bin: "/bin/sh"}
	var stderr bytes.Buffer
	err := e.Pipe(context.Background(), []string{"-c", "printf abc; echo src >&2"}, []string{"-c", "cat > " + f + "; echo dst >&2"}, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(f); string(b) != "abc" {
		t.Errorf("piped = %q", b)
	}
	if !strings.Contains(stderr.String(), "src") || !strings.Contains(stderr.String(), "dst") {
		t.Errorf("stderr = %q", stderr.String())
	}

	err = e.Pipe(context.Background(), []string{"-c", "echo x"}, []string{"-c", "cat >/dev/null; echo bad >&2; exit 2"}, nil)
	var ee *docker.ExitError
	if !errors.As(err, &ee) || ee.Code != 2 || ee.Stderr != "bad" || !strings.Contains(err.Error(), "pipe sink") {
		t.Errorf("sink err = %v", err)
	}
	err = e.Pipe(context.Background(), []string{"-c", "echo gone >&2; exit 1"}, []string{"-c", "cat >/dev/null"}, nil)
	if !errors.As(err, &ee) || ee.Stderr != "gone" || !strings.Contains(err.Error(), "pipe source") {
		t.Errorf("source err = %v", err)
	}
}
