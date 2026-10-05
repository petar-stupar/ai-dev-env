package namespace

import (
	"path"
	"slices"
	"strings"

	"github.com/petar-stupar/ai-dev-env/internal/docker"
)

// AgentUser is the in-container user whose processes count as work.
const AgentUser = "agent"

// idleCommands are agent processes a remount can end without losing work.
var idleCommands = []string{"ps", "sleep", "dotnetdoc"}

// shells are ignored when interactive: an idle terminal.
var shells = []string{"bash", "sh", "dash", "zsh"}

// shellFlagsWithValue take the next word as their value, which is therefore
// not a script to run.
var shellFlagsWithValue = []string{"--init-file", "--rcfile", "-O", "+O", "-o", "+o"}

// runsSomething reports whether a shell's arguments make it run a command or
// a script (`bash -c ...`, `bash -lc ...`, `bash build.sh`) rather than sit
// at a prompt.
func runsSomething(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case slices.Contains(shellFlagsWithValue, a):
			i++
		case strings.HasPrefix(a, "--"):
		case strings.HasPrefix(a, "-") && len(a) > 1:
			if strings.Contains(a, "c") {
				return true
			}
		default:
			return true
		}
	}
	return false
}

// busyProcesses keeps the agent's processes that a remount would interrupt:
// claude, opencode, terminalfs and anything else that is not code-server
// (its node processes included), an idle shell, ps, sleep or dotnetdoc.
func busyProcesses(procs []docker.Proc) []docker.Proc {
	var out []docker.Proc
	for _, p := range procs {
		if p.User != AgentUser {
			continue
		}
		f := strings.Fields(p.Args)
		if len(f) == 0 {
			continue
		}
		name := strings.TrimPrefix(path.Base(f[0]), "-")
		if name == "code-server" || strings.Contains(p.Args, "/usr/lib/code-server") {
			continue
		}
		if slices.Contains(idleCommands, name) {
			continue
		}
		if slices.Contains(shells, name) && !runsSomething(f[1:]) {
			continue
		}
		out = append(out, p)
	}
	return out
}
