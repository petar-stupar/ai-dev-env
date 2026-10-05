// Package aidefile parses and prints .aide files: the `aide ns:<name> stack ...`
// and `aide ns:<name> mount <host> <container>...` lines that `state` prints.
package aidefile

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/petar-stupar/ai-dev-env/internal/config"
)

// Kind is the verb of one .aide line.
type Kind int

const (
	Stack Kind = iota + 1
	Mount
	Allow   // Args are host names
	Network // Args is one of "open", "allowlist"
)

// Command is one parsed line. For Stack, Args are stack names. For Mount, Args
// are host/container pairs in order (even length, host paths not yet expanded).
type Command struct {
	Line int
	Kind Kind
	Args []string
}

// Parse reads .aide text. Blank lines and lines starting with # are skipped.
// Every other line must be `aide ns:<any> stack <name>...`,
// `aide ns:<any> mount <host> <container> [<host> <container>]...`,
// `aide ns:<any> allow <host>...` or `aide ns:<any> network open|allowlist`;
// the namespace is ignored. Errors name the line number.
func Parse(r io.Reader) ([]Command, error) {
	var cmds []Command
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if n == 1 {
			line = strings.TrimPrefix(line, "\xef\xbb\xbf") // a byte-order mark some editors write
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed[0] == '#' {
			continue
		}
		words, err := Split(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		if len(words) == 0 || words[0] != "aide" {
			return nil, fmt.Errorf("line %d: not an aide line (must start with \"aide\")", n)
		}
		if len(words) < 2 {
			return nil, fmt.Errorf("line %d: missing arguments: expected ns:<name> and a verb", n)
		}
		if name, ok := strings.CutPrefix(words[1], "ns:"); !ok || name == "" {
			return nil, fmt.Errorf("line %d: expected ns:<name> after \"aide\", got %q", n, words[1])
		}
		if len(words) < 3 {
			return nil, fmt.Errorf("line %d: missing arguments: expected a verb (stack, mount, allow or network)", n)
		}
		args := words[3:]
		switch words[2] {
		case "stack":
			if len(args) == 0 {
				return nil, fmt.Errorf("line %d: missing arguments: stack needs at least one name", n)
			}
			cmds = append(cmds, Command{Line: n, Kind: Stack, Args: args})
		case "mount":
			if len(args) == 0 {
				return nil, fmt.Errorf("line %d: missing arguments: mount needs <host> <container>", n)
			}
			if len(args)%2 != 0 {
				return nil, fmt.Errorf("line %d: odd mount arguments: expected host/container pairs, got %d", n, len(args))
			}
			cmds = append(cmds, Command{Line: n, Kind: Mount, Args: args})
		case "allow":
			if len(args) == 0 {
				return nil, fmt.Errorf("line %d: missing arguments: allow needs at least one host", n)
			}
			cmds = append(cmds, Command{Line: n, Kind: Allow, Args: args})
		case "network":
			if len(args) != 1 || args[0] != config.NetworkOpen && args[0] != config.NetworkAllowlist {
				return nil, fmt.Errorf("line %d: network needs one argument: %s or %s", n, config.NetworkOpen, config.NetworkAllowlist)
			}
			cmds = append(cmds, Command{Line: n, Kind: Network, Args: args})
		default:
			return nil, fmt.Errorf("line %d: unknown verb %q (want stack, mount, allow or network)", n, words[2])
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return cmds, nil
}

// Split splits a line into words with POSIX-like quoting: single quotes,
// double quotes and backslash escapes. No variable or glob expansion.
func Split(line string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	rs := []rune(line)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		case c == '\'':
			inWord = true
			i++
			for ; i < len(rs) && rs[i] != '\''; i++ {
				cur.WriteRune(rs[i])
			}
			if i >= len(rs) {
				return nil, errors.New("unterminated single quote")
			}
		case c == '"':
			inWord = true
			i++
			closed := false
			for ; i < len(rs); i++ {
				if rs[i] == '"' {
					closed = true
					break
				}
				if rs[i] == '\\' && i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\\') {
					i++
				}
				cur.WriteRune(rs[i])
			}
			if !closed {
				return nil, errors.New("unterminated double quote")
			}
		case c == '\\':
			if i+1 >= len(rs) {
				return nil, errors.New("trailing backslash")
			}
			i++
			inWord = true
			cur.WriteRune(rs[i])
		default:
			inWord = true
			cur.WriteRune(c)
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
}

// Quote returns s quoted for a shell, unquoted when it needs no quoting.
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("_./:=+@%,~-", c) >= 0) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ExpandHome replaces a leading "~" or "~/" with home. "~user" is an error, as
// is a relative path, because mounts must be absolute.
func ExpandHome(p, home string) (string, error) {
	if home == "" && (p == "~" || strings.HasPrefix(p, "~/")) {
		return "", fmt.Errorf("host path %q: cannot expand ~ because HOME is not set", p)
	}
	switch {
	case p == "~":
		p = home
	case strings.HasPrefix(p, "~/"):
		p = home + p[1:]
	case strings.HasPrefix(p, "~"):
		return "", fmt.Errorf("host path %q: ~user expansion is not supported", p)
	}
	if !strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("host path must be absolute: %q", p)
	}
	return p, nil
}

// Print renders stacks and mounts as commands: one stack line, then one
// mount line per pair, each with a trailing newline.
func Print(ns string, stacks []string, mounts []config.Mount) string {
	return printStacksMounts(ns, stacks, mounts)
}

// PrintState is Print plus the network mode and the allowed hosts. The
// network line is always written, because a file without one means the
// default, which is the allowlist.
func PrintState(ns string, st *config.State) string {
	s := printStacksMounts(ns, st.Stacks, st.Mounts)
	var b strings.Builder
	b.WriteString(s)
	mode := config.NetworkOpen
	if st.Restricted() {
		mode = config.NetworkAllowlist
	}
	fmt.Fprintf(&b, "aide ns:%s network %s\n", ns, mode)
	if len(st.Allow) > 0 {
		fmt.Fprintf(&b, "aide ns:%s allow %s\n", ns, strings.Join(st.Allow, " "))
	}
	return b.String()
}

func printStacksMounts(ns string, stacks []string, mounts []config.Mount) string {
	var b strings.Builder
	if len(stacks) > 0 {
		fmt.Fprintf(&b, "aide ns:%s stack %s\n", ns, strings.Join(stacks, " "))
	}
	for _, m := range mounts {
		fmt.Fprintf(&b, "aide ns:%s mount %s %s\n", ns, Quote(m.Host), Quote(m.Container))
	}
	return b.String()
}
