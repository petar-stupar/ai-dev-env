// Command aide manages isolated AI development environments (namespaces).
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/petar-stupar/ai-dev-env/internal/config"
	"github.com/petar-stupar/ai-dev-env/internal/docker"
	"github.com/petar-stupar/ai-dev-env/internal/namespace"
	"github.com/petar-stupar/ai-dev-env/internal/platform"
	"github.com/petar-stupar/ai-dev-env/internal/stacks"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run is the testable entry point. Exit codes: 0 ok, 1 error, 2 usage.
func run(ctx context.Context, args []string, env func(string) string, stdin *os.File, stdout, stderr *os.File) int {
	cmd, err := parse(args)
	if err != nil {
		var ue *usageError
		if errors.As(err, &ue) {
			fmt.Fprintf(stderr, "aide: %s\nrun 'aide help'\n", ue.msg)
			return 2
		}
		fmt.Fprintf(stderr, "aide: %v\n", err)
		return 1
	}
	if err := execute(ctx, cmd, env, stdin, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "aide: %v\n", err)
		return 1
	}
	return 0
}

const usageText = `aide manages isolated AI development environments (namespaces).

Usage:
  aide [-v] ns new <name> [file.aide]       create a namespace from a .aide file ("-" = stdin)
  aide [-v] ns clone <existing> <new> [--yes]
  aide [-v] ns remove <name> [--keep-volumes] [--yes]
  aide [-v] ns list                         NAME PHASE PORT MOUNTS STACKS
  aide [-v] ns:<name> stack <stack>...      add stacks
  aide [-v] ns:<name> build [--no-cache]
  aide [-v] ns:<name> start [--yes]
  aide [-v] ns:<name> stop
  aide [-v] ns:<name> reset [--yes]
  aide [-v] ns:<name> mount <host> <container> [<host> <container>]... [--yes]
  aide [-v] ns:<name> umount <container-path>... [--yes]
  aide [-v] ns:<name> network open|allowlist [--yes]   which hosts the container may reach
  aide [-v] ns:<name> allow <host>...       add hosts (name or *.name) to the allowlist
  aide [-v] ns:<name> disallow <host>...    remove hosts added with allow
  aide [-v] ns:<name> state                 print the configuration as .aide text
  aide [-v] ns:<name> dockerfile            print the Dockerfile build would use
  aide stacks                               list the stack catalog
  aide version
  aide help

Flags:
  -v    echo every docker command line to stderr (anywhere on the line)
  --yes skip confirmation prompts

Environment:
  AIDE_DOCKER             docker binary (default "docker")
  AIDE_FLATTEN_THRESHOLD  layer count above which snapshots are flattened
  XDG_CONFIG_HOME         config root (default ~/.config); aide uses <root>/aide
`

type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error { return &usageError{fmt.Sprintf(format, a...)} }

// command is a parsed command line.
type command struct {
	Verb        string // "help", "version", "stacks", "ns new", "ns clone", "ns remove", "ns list", or the ns:<name> verb
	NS          string // for ns:<name> verbs
	Args        []string
	Yes         bool
	NoCache     bool
	KeepVolumes bool
	Verbose     bool
}

// nsVerbs lists the ns:<name> verbs.
var nsVerbs = map[string]bool{
	"stack": true, "build": true, "start": true, "stop": true, "reset": true,
	"mount": true, "umount": true, "state": true, "dockerfile": true,
	"network": true, "allow": true, "disallow": true,
}

// parse turns argv (without the program name) into a command.
func parse(args []string) (*command, error) {
	c := &command{}
	var rest []string
	for i, a := range args {
		if a == "--" { // everything after it is positional, a literal -v included
			rest = append(rest, args[i:]...)
			break
		}
		if a == "-v" {
			c.Verbose = true
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) == 0 {
		return nil, usagef("missing command")
	}
	head, tail := rest[0], rest[1:]
	switch {
	case head == "help" || head == "-h" || head == "--help":
		c.Verb = "help"
		return c, nil
	case head == "version":
		c.Verb = "version"
		return c, noArgs(tail, "version")
	case head == "stacks":
		c.Verb = "stacks"
		return c, noArgs(tail, "stacks")
	case head == "ns":
		return parseNS(c, tail)
	case strings.HasPrefix(head, "ns:"):
		return parseNSVerb(c, strings.TrimPrefix(head, "ns:"), tail)
	}
	return nil, usagef("unknown command %q", head)
}

func noArgs(a []string, what string) error {
	if len(a) > 0 {
		return usagef("%s takes no arguments", what)
	}
	return nil
}

func parseNS(c *command, args []string) (*command, error) {
	if len(args) == 0 {
		return nil, usagef("ns: missing verb (new, clone, remove, list)")
	}
	sub, rest := args[0], args[1:]
	fs := newFlagSet("ns " + sub)
	switch sub {
	case "new":
		pos, err := parseInterspersed(fs, rest)
		if err != nil {
			return nil, err
		}
		if len(pos) < 1 || len(pos) > 2 {
			return nil, usagef("usage: aide ns new <name> [file.aide]")
		}
		c.Verb, c.Args = "ns new", pos
	case "clone":
		fs.BoolVar(&c.Yes, "yes", false, "")
		pos, err := parseInterspersed(fs, rest)
		if err != nil {
			return nil, err
		}
		if len(pos) != 2 {
			return nil, usagef("usage: aide ns clone <existing> <new> [--yes]")
		}
		c.Verb, c.Args = "ns clone", pos
	case "remove":
		fs.BoolVar(&c.Yes, "yes", false, "")
		fs.BoolVar(&c.KeepVolumes, "keep-volumes", false, "")
		pos, err := parseInterspersed(fs, rest)
		if err != nil {
			return nil, err
		}
		if len(pos) != 1 {
			return nil, usagef("usage: aide ns remove <name> [--keep-volumes] [--yes]")
		}
		c.Verb, c.Args = "ns remove", pos
	case "list":
		pos, err := parseInterspersed(fs, rest)
		if err != nil {
			return nil, err
		}
		if len(pos) != 0 {
			return nil, usagef("ns list takes no arguments")
		}
		c.Verb = "ns list"
	default:
		return nil, usagef("unknown ns verb %q", sub)
	}
	return c, nil
}

func parseNSVerb(c *command, name string, args []string) (*command, error) {
	if name == "" {
		return nil, usagef("ns: missing namespace name")
	}
	// The verb is the first argument that is not a flag.
	vi := -1
	for i, a := range args {
		if a == "--" {
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			vi = i
			break
		}
	}
	if vi < 0 {
		return nil, usagef("ns:%s: missing verb", name)
	}
	verb := args[vi]
	if !nsVerbs[verb] {
		return nil, usagef("ns:%s: unknown verb %q", name, verb)
	}
	rest := append(append([]string{}, args[:vi]...), args[vi+1:]...)
	fs := newFlagSet("ns:" + name + " " + verb)
	switch verb {
	case "build":
		fs.BoolVar(&c.NoCache, "no-cache", false, "")
	case "start", "reset", "mount", "umount", "network":
		fs.BoolVar(&c.Yes, "yes", false, "")
	}
	pos, err := parseInterspersed(fs, rest)
	if err != nil {
		return nil, err
	}
	c.Verb, c.NS, c.Args = verb, name, pos
	switch verb {
	case "stack":
		if len(pos) == 0 {
			return nil, usagef("usage: aide ns:%s stack <stack>...", name)
		}
	case "mount":
		if len(pos) == 0 || len(pos)%2 != 0 {
			return nil, usagef("usage: aide ns:%s mount <host> <container> [<host> <container>]... (needs pairs)", name)
		}
	case "umount":
		if len(pos) == 0 {
			return nil, usagef("usage: aide ns:%s umount <container-path>...", name)
		}
	case "allow", "disallow":
		if len(pos) == 0 {
			return nil, usagef("usage: aide ns:%s %s <host>...", name, verb)
		}
	case "network":
		if len(pos) != 1 {
			return nil, usagef("usage: aide ns:%s network open|allowlist [--yes]", name)
		}
	default:
		if len(pos) != 0 {
			return nil, usagef("ns:%s %s takes no arguments", name, verb)
		}
	}
	return c, nil
}

func newFlagSet(name string) *flagSet { return newFS(name) }

// parseInterspersed parses flags and positionals in any order. Everything
// after "--" is positional.
func parseInterspersed(fs *flagSet, args []string) ([]string, error) {
	var pos []string
	for len(args) > 0 {
		if args[0] == "--" {
			return append(pos, args[1:]...), nil
		}
		if err := fs.Parse(args); err != nil {
			return nil, usagef("%v", err)
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		if args[0] == "--" { // consumed by Parse only when it came first; keep as a guard
			continue
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
	return pos, nil
}

func execute(ctx context.Context, c *command, env func(string) string, stdin, stdout, stderr *os.File) error {
	switch c.Verb {
	case "help":
		fmt.Fprint(stdout, usageText)
		return nil
	case "version":
		fmt.Fprintln(stdout, version)
		return nil
	case "stacks":
		return printStacks(stdout)
	}

	dir, err := config.Dir(env)
	if err != nil {
		return err
	}
	store := &config.Store{Root: dir}
	ex := &docker.Exec{Bin: env("AIDE_DOCKER")}
	if ex.Bin == "" {
		ex.Bin = "docker"
	}
	if c.Verbose {
		ex.Verbose = stderr
	}
	dc := &docker.Client{R: ex}
	home := env("HOME")
	m := &namespace.Manager{
		Store: store, Docker: dc, Out: stdout, Err: stderr,
		Confirm: confirmer(stdin, stderr),
	}
	if v := env("AIDE_FLATTEN_THRESHOLD"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return fmt.Errorf("AIDE_FLATTEN_THRESHOLD must be a positive integer, got %q", v)
		}
		m.FlattenThreshold = n
	}

	switch c.Verb {
	case "state":
		s, err := m.State(ctx, c.NS)
		if err != nil {
			return err
		}
		fmt.Fprint(stdout, s)
		return nil
	case "dockerfile":
		b, err := m.Dockerfile(ctx, c.NS)
		if err != nil {
			return err
		}
		_, err = stdout.Write(b)
		return err
	}

	// Everything else talks to docker.
	if c.Verb == "ns new" {
		// Read the .aide source before probing docker, so a bad path fails fast.
		return newNS(ctx, c, m, dc, env, home, stdin)
	}
	if err := detectPlatform(ctx, m, dc, env, home); err != nil {
		return err
	}
	defer printPlatformWarnings(m, stderr)
	o := namespace.Options{Yes: c.Yes}
	switch c.Verb {
	case "ns clone":
		return m.Clone(ctx, c.Args[0], c.Args[1], o)
	case "ns remove":
		return m.Remove(ctx, c.Args[0], c.KeepVolumes, o)
	case "ns list":
		return printList(ctx, m, stdout)
	case "stack":
		return m.AddStacks(ctx, c.NS, c.Args)
	case "build":
		return m.Build(ctx, c.NS, c.NoCache)
	case "start":
		return m.Start(ctx, c.NS, o)
	case "stop":
		return m.Stop(ctx, c.NS)
	case "reset":
		return m.Reset(ctx, c.NS, o)
	case "mount":
		pairs := make([]config.Mount, 0, len(c.Args)/2)
		for i := 0; i < len(c.Args); i += 2 {
			pairs = append(pairs, config.Mount{Host: c.Args[i], Container: c.Args[i+1]})
		}
		return m.Mount(ctx, c.NS, pairs, home, o)
	case "umount":
		return m.Umount(ctx, c.NS, c.Args, o)
	case "allow":
		return m.Allow(ctx, c.NS, c.Args, false)
	case "disallow":
		return m.Allow(ctx, c.NS, c.Args, true)
	case "network":
		return m.Network(ctx, c.NS, c.Args[0], o)
	}
	return fmt.Errorf("internal error: unhandled verb %q", c.Verb)
}

func detectPlatform(ctx context.Context, m *namespace.Manager, dc *docker.Client, env func(string) string, home string) error {
	p, err := platform.Detect(ctx, dc, env, home)
	if err != nil {
		return err
	}
	m.Plat = p
	return nil
}

// printPlatformWarnings reports what the platform probe could not find out
// and guessed instead, once the command is done.
func printPlatformWarnings(m *namespace.Manager, stderr io.Writer) {
	w, ok := m.Plat.(interface{ Warnings() []string })
	if !ok {
		return
	}
	seen := map[string]bool{}
	for _, s := range w.Warnings() {
		if !seen[s] {
			seen[s] = true
			fmt.Fprintf(stderr, "warning: %s\n", s)
		}
	}
}

func newNS(ctx context.Context, c *command, m *namespace.Manager, dc *docker.Client, env func(string) string, home string, stdin *os.File) error {
	name := c.Args[0]
	if err := config.ValidName(name); err != nil {
		return err
	}
	var src io.Reader
	switch {
	case len(c.Args) == 1:
		b, err := m.Store.DefaultAide(stacks.DefaultAide())
		if err != nil {
			return err
		}
		src = strings.NewReader(string(b))
	case c.Args[1] == "-":
		src = stdin
	default:
		f, err := os.Open(c.Args[1])
		if err != nil {
			return err
		}
		defer f.Close()
		src = f
	}
	if err := detectPlatform(ctx, m, dc, env, home); err != nil {
		return err
	}
	defer printPlatformWarnings(m, m.Err)
	return m.New(ctx, name, src, home)
}

// confirmer asks on stderr and reads one line from stdin. A non-terminal
// stdin answers no.
func confirmer(stdin, stderr *os.File) func(string) bool {
	return func(prompt string) bool {
		if stdin == nil {
			return false
		}
		fi, err := stdin.Stat()
		if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
			fmt.Fprintf(stderr, "%s [y/N] (not a terminal: assuming no; pass --yes)\n", prompt)
			return false
		}
		fmt.Fprintf(stderr, "%s [y/N] ", prompt)
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return true
		}
		return false
	}
}

func printList(ctx context.Context, m *namespace.Manager, out io.Writer) error {
	rows, err := m.List(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tPHASE\tPORT\tMOUNTS\tSTACKS")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\n", r.Name, r.Phase, r.Port, r.Mounts, strings.Join(r.Stacks, ","))
	}
	return tw.Flush()
}

func printStacks(out io.Writer) error {
	cat, err := stacks.Catalog()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(cat))
	for n := range cat {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := cat[names[i]], cat[names[j]]
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		return names[i] < names[j]
	})
	dash := func(l []string) string {
		if len(l) == 0 {
			return "-"
		}
		return strings.Join(l, ",")
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tDESCRIPTION\tREQUIRES\tCONFLICTS\tRUN")
	for _, n := range names {
		s := cat[n]
		var run []string
		for _, c := range s.Run.CapAdd {
			run = append(run, c)
		}
		for _, d := range s.Run.Devices {
			run = append(run, "dev:"+d)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", n, s.Description, dash(s.Requires), dash(s.Conflicts), dash(run))
	}
	return tw.Flush()
}
