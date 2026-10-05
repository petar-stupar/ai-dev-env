// Package fake provides a scripted docker.Runner that records every call.
package fake

import (
	"context"
	"io"
	"slices"
	"sync"

	"github.com/petar-stupar/ai-dev-env/internal/docker"
)

// Response is one scripted reply.
type Response struct {
	Stdout string
	Stderr string
	Code   int
}

type rule struct {
	prefix []string
	resp   Response
	used   bool
}

// Call is one recorded invocation. For a Pipe, Args is the source side and To
// the sink side.
type Call struct {
	Args []string
	To   []string
	Pipe bool
}

// Recorder implements docker.Runner. Each call is answered by the scripted
// rule with the longest matching argv prefix (default: empty stdout, exit 0).
// Several rules with the same prefix form a queue: each is used once, in
// order, and the last one repeats.
type Recorder struct {
	// Strict makes calls that match no rule fail with exit 1.
	Strict bool

	mu    sync.Mutex
	rules []*rule
	calls []Call
}

var _ docker.Runner = (*Recorder)(nil)

// On scripts stdout and exit code for calls starting with prefix.
func (r *Recorder) On(prefix []string, stdout string, code int) {
	r.Respond(prefix, Response{Stdout: stdout, Code: code})
}

// OnError scripts a failing call with docker's stderr message.
func (r *Recorder) OnError(prefix []string, stderr string, code int) {
	r.Respond(prefix, Response{Stderr: stderr, Code: code})
}

// Respond scripts a full response for calls starting with prefix.
func (r *Recorder) Respond(prefix []string, resp Response) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rules = append(r.rules, &rule{prefix: slices.Clone(prefix), resp: resp})
}

func (r *Recorder) match(args []string) (Response, bool) {
	best := -1
	for _, ru := range r.rules {
		if len(ru.prefix) > best && len(ru.prefix) <= len(args) && slices.Equal(ru.prefix, args[:len(ru.prefix)]) {
			best = len(ru.prefix)
		}
	}
	if best < 0 {
		if r.Strict {
			return Response{Stderr: "fake: unscripted call: " + docker.CommandLine(args), Code: 1}, false
		}
		return Response{}, false
	}
	var queue []*rule
	for _, ru := range r.rules {
		if len(ru.prefix) == best && slices.Equal(ru.prefix, args[:best]) {
			queue = append(queue, ru)
		}
	}
	for i, ru := range queue {
		if !ru.used || i == len(queue)-1 {
			ru.used = true
			return ru.resp, true
		}
	}
	return Response{}, false // unreachable
}

func exitErr(args []string, resp Response) error {
	if resp.Code == 0 {
		return nil
	}
	return &docker.ExitError{Args: args, Code: resp.Code, Stderr: resp.Stderr}
}

// Run records args and returns the scripted response. Stdout/Stderr writers,
// when set, receive the scripted output instead of Result.
func (r *Recorder) Run(ctx context.Context, args []string, sio docker.IO) (docker.Result, error) {
	r.mu.Lock()
	r.calls = append(r.calls, Call{Args: slices.Clone(args)})
	resp, _ := r.match(args)
	r.mu.Unlock()

	res := docker.Result{Code: resp.Code, Stderr: []byte(resp.Stderr)}
	if sio.Stdout != nil {
		io.WriteString(sio.Stdout, resp.Stdout)
	} else {
		res.Stdout = []byte(resp.Stdout)
	}
	if sio.Stderr != nil {
		io.WriteString(sio.Stderr, resp.Stderr)
	}
	if err := ctx.Err(); err != nil {
		return res, err
	}
	return res, exitErr(args, resp)
}

// Pipe records both sides as one call. Each side is matched against the
// rules independently; the source side's failure wins.
func (r *Recorder) Pipe(ctx context.Context, from, to []string, stderr io.Writer) error {
	r.mu.Lock()
	r.calls = append(r.calls, Call{Args: slices.Clone(from), To: slices.Clone(to), Pipe: true})
	rf, _ := r.match(from)
	rt, _ := r.match(to)
	r.mu.Unlock()
	if stderr != nil {
		io.WriteString(stderr, rf.Stderr+rt.Stderr)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := exitErr(from, rf); err != nil {
		return err
	}
	return exitErr(to, rt)
}

// Calls returns the recorded calls.
func (r *Recorder) Calls() []Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls)
}

// Transcript renders each call shell-quoted, e.g. "docker rm -f x" or
// "docker export x | docker import --change 'USER agent' - ref".
func (r *Recorder) Transcript() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	for i, c := range r.calls {
		out[i] = docker.CommandLine(c.Args)
		if c.Pipe {
			out[i] += " | " + docker.CommandLine(c.To)
		}
	}
	return out
}

// Reset forgets recorded calls but keeps the rules.
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = nil
}
