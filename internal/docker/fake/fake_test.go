package fake

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/petar-stupar/ai-dev-env/internal/docker"
)

func TestRecorder(t *testing.T) {
	ctx := context.Background()
	r := &Recorder{}
	r.On([]string{"image"}, "short", 0)
	r.On([]string{"image", "inspect"}, "first", 0)
	r.On([]string{"image", "inspect"}, "second", 0)
	for _, want := range []string{"first", "second", "second"} {
		res, err := r.Run(ctx, []string{"image", "inspect", "x"}, docker.IO{})
		if err != nil || string(res.Stdout) != want {
			t.Errorf("got %q, %v; want %q", res.Stdout, err, want)
		}
	}
	if res, _ := r.Run(ctx, []string{"image", "rm", "x"}, docker.IO{}); string(res.Stdout) != "short" {
		t.Errorf("shorter prefix: %q", res.Stdout)
	}
	if res, err := r.Run(ctx, []string{"ps"}, docker.IO{}); err != nil || len(res.Stdout) != 0 {
		t.Errorf("default: %q %v", res.Stdout, err)
	}
	r.Strict = true
	var ee *docker.ExitError
	if _, err := r.Run(ctx, []string{"ps"}, docker.IO{}); !errors.As(err, &ee) {
		t.Errorf("strict: %v", err)
	}
	if err := r.Pipe(ctx, []string{"image", "x"}, []string{"image", "inspect", "a b"}, nil); err != nil {
		t.Error(err)
	}
	want := []string{
		"docker image inspect x", "docker image inspect x", "docker image inspect x",
		"docker image rm x", "docker ps", "docker ps",
		"docker image x | docker image inspect 'a b'",
	}
	if got := r.Transcript(); !slices.Equal(got, want) {
		t.Errorf("transcript\n got %q\nwant %q", got, want)
	}
}
