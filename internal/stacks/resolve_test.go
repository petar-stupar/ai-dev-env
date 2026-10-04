package stacks

import "testing"

func synthetic() map[string]*Stack {
	return catalogOf(
		mk("base", 0),
		mk("dotnet8", 10, requires("base")),
		mk("dotnet10", 11, requires("base")),
		mk("node24", 31, requires("base")),
		mk("terminalfs", 50, requires("base"), run(RunReq{CapAdd: []string{"SYS_ADMIN"}, SecurityOpt: []string{"apparmor=unconfined"}, Env: map[string]string{"A": "1"}})),
		mk("claude", 60, requires("base")),
		mk("opencode", 70, requires("base"), conflicts("opencode-filesystem")),
		mk("opencode-filesystem", 71, requires("terminalfs")),
		mk("dotnetdocfs", 80, requires("base"), suggests("dotnet8", "dotnet10"), run(RunReq{CapAdd: []string{"SYS_ADMIN", "NET_ADMIN"}, SecurityOpt: []string{"apparmor=unconfined"}, Env: map[string]string{"A": "2", "B": "x"}})),
		// "late" has a low order but depends on something with a high one.
		mk("late", 1, requires("dotnetdocfs")),
	)
}

func TestResolveExpandsRequires(t *testing.T) {
	r := mustResolve(t, synthetic(), "opencode-filesystem")
	eq(t, r.Names(), []string{"base", "terminalfs", "opencode-filesystem"})
}

func TestResolveOrderAndTies(t *testing.T) {
	r := mustResolve(t, synthetic(), "late", "claude", "node24", "dotnet10")
	// late (order 1) must still follow dotnetdocfs, which it requires.
	eq(t, r.Names(), []string{"base", "dotnet10", "node24", "claude", "dotnetdocfs", "late"})
}

func TestResolveTieByName(t *testing.T) {
	cat := catalogOf(mk("b", 5), mk("a", 5), mk("c", 4))
	eq(t, mustResolve(t, cat, "a", "b", "c").Names(), []string{"c", "a", "b"})
}

func TestResolveInputOrderIrrelevantAndDuplicates(t *testing.T) {
	cat := synthetic()
	a := mustResolve(t, cat, "claude", "dotnet8", "terminalfs", "base").Names()
	b := mustResolve(t, cat, "terminalfs", "base", "dotnet8", "claude", "claude", "base").Names()
	eq(t, a, b)
}

func TestResolveUnknown(t *testing.T) {
	_, err := Resolve(synthetic(), []string{"base", "cobol"})
	errContains(t, err, `unknown stack "cobol"`)

	cat := catalogOf(mk("x", 0, requires("ghost")))
	_, err = Resolve(cat, []string{"x"})
	errContains(t, err, `stack x requires unknown stack "ghost"`)
}

func TestResolveConflictEitherDirection(t *testing.T) {
	cat := synthetic()
	// opencode declares the conflict.
	_, err := Resolve(cat, []string{"opencode", "opencode-filesystem"})
	errContains(t, err, "stacks opencode and opencode-filesystem conflict")
	// Only the other side declares it.
	cat2 := catalogOf(mk("p", 0), mk("q", 0, conflicts("p")), mk("r", 0, requires("p")))
	_, err = Resolve(cat2, []string{"r", "q"})
	errContains(t, err, "stacks p and q conflict")
	_, err = Resolve(cat2, []string{"q", "r"})
	errContains(t, err, "stacks p and q conflict")
}

func TestResolveCycle(t *testing.T) {
	cat := catalogOf(mk("a", 0, requires("b")), mk("b", 0, requires("c")), mk("c", 0, requires("a")), mk("d", 0, requires("a")))
	_, err := Resolve(cat, []string{"d"})
	errContains(t, err, "cycle: a -> b -> c -> a")
}

func TestResolveSuggestsWarning(t *testing.T) {
	cat := synthetic()
	r := mustResolve(t, cat, "dotnetdocfs")
	eq(t, r.Warnings, []string{"stack dotnetdocfs suggests one of dotnet8, dotnet10; none selected"})
	r = mustResolve(t, cat, "dotnetdocfs", "dotnet10")
	eq(t, len(r.Warnings), 0)
}

func TestResolutionRun(t *testing.T) {
	r := mustResolve(t, synthetic(), "dotnetdocfs", "terminalfs")
	eq(t, r.Run(), RunReq{
		CapAdd:      []string{"NET_ADMIN", "SYS_ADMIN"},
		SecurityOpt: []string{"apparmor=unconfined"},
		Env:         map[string]string{"A": "2", "B": "x"}, // dotnetdocfs (80) after terminalfs (50)
	})
	eq(t, mustResolve(t, synthetic(), "base").Run(), RunReq{})
}
