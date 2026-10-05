package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

func (e *Exec) bin() string {
	if e.Bin == "" {
		return "docker"
	}
	return e.Bin
}

func (e *Exec) echo(line string) {
	if e.Verbose != nil {
		fmt.Fprintln(e.Verbose, "+ "+line)
	}
}

// Run runs `docker <args>`. Nil writers in io are captured into Result.
// Stderr is always also captured (teed when a writer is given) so a non-zero
// exit can be reported as *ExitError with docker's message.
func (e *Exec) Run(ctx context.Context, args []string, io IO) (Result, error) {
	e.echo(CommandLine(args))
	cmd := exec.CommandContext(ctx, e.bin(), args...)
	cmd.Stdin = io.Stdin
	var stdout, stderr bytes.Buffer
	// One lock for both user writers: they may be the same writer (build
	// progress) and exec copies stdout and stderr on separate goroutines.
	var mu sync.Mutex
	if io.Stdout != nil {
		cmd.Stdout = &lockedWriter{w: io.Stdout, mu: &mu}
	} else {
		cmd.Stdout = &stdout
	}
	if io.Stderr != nil {
		cmd.Stderr = multiWriter(&lockedWriter{w: io.Stderr, mu: &mu}, &stderr)
	} else {
		cmd.Stderr = &stderr
	}
	err := cmd.Run()
	res := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if cmd.ProcessState != nil {
		res.Code = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		return res, exitError(args, err, stderr.String())
	}
	return res, nil
}

// exitError turns a finished command's error into *ExitError when the
// process ran and exited non-zero; other failures (binary missing, context
// cancelled before start) are wrapped as-is.
func exitError(args []string, err error, stderr string) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = ee.Error()
		}
		return &ExitError{Args: args, Code: ee.ExitCode(), Stderr: msg}
	}
	return fmt.Errorf("docker %s: %w", join(args), err)
}

// Pipe runs `docker <from> | docker <to>`. Both sides' stderr go to stderr
// (captured when nil). The first error, source side first, is returned and
// names its side.
func (e *Exec) Pipe(ctx context.Context, from, to []string, stderr io.Writer) error {
	e.echo(CommandLine(from) + " | " + CommandLine(to))
	if stderr == nil {
		stderr = io.Discard
	}
	shared := &lockedWriter{w: stderr, mu: new(sync.Mutex)}
	var errFrom, errTo bytes.Buffer

	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	src := exec.CommandContext(ctx, e.bin(), from...)
	src.Stdout = pw
	src.Stderr = multiWriter(shared, &errFrom)
	dst := exec.CommandContext(ctx, e.bin(), to...)
	dst.Stdin = pr
	dst.Stderr = multiWriter(shared, &errTo)
	dst.Stdout = io.Discard // import prints the new ID; Flatten re-inspects

	if err := src.Start(); err != nil {
		pr.Close()
		pw.Close()
		return fmt.Errorf("pipe source: docker %s: %w", join(from), err)
	}
	if err := dst.Start(); err != nil {
		pr.Close()
		pw.Close()
		_ = src.Process.Kill()
		_ = src.Wait()
		return fmt.Errorf("pipe sink: docker %s: %w", join(to), err)
	}
	// The children hold their own copies; closing ours lets EOF and EPIPE
	// propagate when either side exits.
	pr.Close()
	pw.Close()

	var wg sync.WaitGroup
	var srcErr, dstErr error
	wg.Add(2)
	go func() { defer wg.Done(); srcErr = src.Wait() }()
	go func() { defer wg.Done(); dstErr = dst.Wait() }()
	wg.Wait()
	if srcErr != nil {
		return fmt.Errorf("pipe source: %w", exitError(from, srcErr, errFrom.String()))
	}
	if dstErr != nil {
		return fmt.Errorf("pipe sink: %w", exitError(to, dstErr, errTo.String()))
	}
	return nil
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// multiWriter tees to w and buf but never fails on w's error, so a broken
// progress writer cannot kill the docker command.
func multiWriter(w io.Writer, buf *bytes.Buffer) io.Writer {
	return writerFunc(func(p []byte) (int, error) {
		buf.Write(p)
		_, _ = w.Write(p)
		return len(p), nil
	})
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// CommandLine renders `docker <args>` shell-quoted, as Verbose and the fake
// transcript print it.
func CommandLine(args []string) string {
	var b strings.Builder
	b.WriteString("docker")
	for _, a := range args {
		b.WriteByte(' ')
		b.WriteString(ShellQuote(a))
	}
	return b.String()
}

// ShellQuote quotes s for a POSIX shell, leaving plain words unquoted.
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("-_./:=,@%+", r):
		default:
			safe = false
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
