package stacks

import (
	"reflect"
	"strings"
	"testing"
)

type opt func(*Stack)

func requires(n ...string) opt  { return func(s *Stack) { s.Requires = n } }
func conflicts(n ...string) opt { return func(s *Stack) { s.Conflicts = n } }
func suggests(n ...string) opt  { return func(s *Stack) { s.Suggests = n } }
func fragment(f string) opt     { return func(s *Stack) { s.Fragment = f } }
func hook(n, body string) opt {
	return func(s *Stack) { s.Hooks[n] = File{Data: []byte(body), Mode: 0o755} }
}
func file(rel, body string) opt {
	return func(s *Stack) { s.Files[rel] = File{Data: []byte(body), Mode: fileMode(rel)} }
}
func contrib(n, body string) opt { return func(s *Stack) { s.Contrib[n] = []byte(body) } }
func cache(c ...string) opt      { return func(s *Stack) { s.Cache = c } }
func run(r RunReq) opt           { return func(s *Stack) { s.Run = r } }

func mk(name string, order int, opts ...opt) *Stack {
	s := &Stack{Name: name, Order: order, Hooks: map[string]File{}, Files: map[string]File{}, Contrib: map[string][]byte{}}
	for _, o := range opts {
		o(s)
	}
	return s
}

func catalogOf(ss ...*Stack) map[string]*Stack {
	m := map[string]*Stack{}
	for _, s := range ss {
		m[s.Name] = s
	}
	return m
}

func mustResolve(t *testing.T, cat map[string]*Stack, sel ...string) *Resolution {
	t.Helper()
	r, err := Resolve(cat, sel)
	if err != nil {
		t.Fatalf("Resolve(%v): %v", sel, err)
	}
	return r
}

func eq(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func errContains(t *testing.T, err error, sub string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), sub) {
		t.Fatalf("error %v, want one containing %q", err, sub)
	}
}
