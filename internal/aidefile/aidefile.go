// Package aidefile parses and prints .aide files: the `aide ns:<name> stack ...`
// and `aide ns:<name> mount <host> <container>...` lines that `state` prints.
package aidefile

import (
	"errors"
	"io"

	"github.com/petar-stupar/ai-dev-env/internal/config"
)

// Kind is the verb of one .aide line.
type Kind int

const (
	Stack Kind = iota + 1
	Mount
)

// Command is one parsed line. For Stack, Args are stack names. For Mount, Args
// are host/container pairs in order (even length, host paths not yet expanded).
type Command struct {
	Line int
	Kind Kind
	Args []string
}

// Parse reads .aide text. Blank lines and lines starting with # are skipped.
// Every other line must be `aide ns:<any> stack <name>...` or
// `aide ns:<any> mount <host> <container> [<host> <container>]...`; the
// namespace is ignored. Errors name the line number.
func Parse(r io.Reader) ([]Command, error) { return nil, errors.New("not implemented") }

// Split splits a line into words with POSIX-like quoting: single quotes,
// double quotes and backslash escapes. No variable or glob expansion.
func Split(line string) ([]string, error) { return nil, errors.New("not implemented") }

// Quote returns s quoted for a shell, unquoted when it needs no quoting.
func Quote(s string) string { return s }

// ExpandHome replaces a leading "~" or "~/" with home. "~user" is an error, as
// is a relative path, because mounts must be absolute.
func ExpandHome(p, home string) (string, error) { return "", errors.New("not implemented") }

// Print renders the configuration as commands: one stack line, then one mount
// line per pair, each with a trailing newline.
func Print(ns string, stacks []string, mounts []config.Mount) string { return "" }
