package aidefile

import (
	"reflect"
	"strings"
	"testing"

	"github.com/petar-stupar/ai-dev-env/internal/config"
)

func TestSplit(t *testing.T) {
	tests := []struct {
		in   string
		want []string
		err  string
	}{
		{"a b  c", []string{"a", "b", "c"}, ""},
		{"  ", nil, ""},
		{`'a b' c`, []string{"a b", "c"}, ""},
		{`'a\b'`, []string{`a\b`}, ""},
		{`"a b" "c\"d" "e\\f" "g\h"`, []string{"a b", `c"d`, `e\f`, `g\h`}, ""},
		{`a\ b`, []string{"a b"}, ""},
		{`''`, []string{""}, ""},
		{`a'b'"c"`, []string{"abc"}, ""},
		{`'it'\''s'`, []string{"it's"}, ""},
		{`$HOME *`, []string{"$HOME", "*"}, ""},
		{`'abc`, nil, "unterminated single quote"},
		{`"abc`, nil, "unterminated double quote"},
		{`abc\`, nil, "trailing backslash"},
	}
	for _, tc := range tests {
		got, err := Split(tc.in)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("Split(%q) err = %v, want %q", tc.in, err, tc.err)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Split(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestParse(t *testing.T) {
	in := "# comment\n\n  # indented\naide ns:x stack a b\n   \naide ns:y mount /h/a /c/a '/h/b c' /c/b\naide ns:z mount ~/q /q\n"
	got, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []Command{
		{4, Stack, []string{"a", "b"}},
		{6, Mount, []string{"/h/a", "/c/a", "/h/b c", "/c/b"}},
		{7, Mount, []string{"~/q", "/q"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v want %+v", got, want)
	}

	bad := []struct{ in, err string }{
		{"stack a b", "line 1: not an aide line"},
		{"# c\nfoo bar", "line 2: not an aide line"},
		{"aide ns:x frob a", "line 1: unknown verb"},
		{"\n\naide ns:x stack", "line 3: missing arguments"},
		{"aide ns:x mount", "line 1: missing arguments"},
		{"aide ns:x mount /a", "line 1: odd mount arguments"},
		{"aide ns:x mount /a /b /c", "line 1: odd mount arguments"},
		{"aide ns: stack a", "line 1:"},
		{"aide stack a", "line 1:"},
		{"aide", "line 1: missing arguments"},
		{"aide ns:x", "line 1: missing arguments"},
		{"aide ns:x stack 'a", "line 1: unterminated single quote"},
	}
	for _, tc := range bad {
		_, err := Parse(strings.NewReader(tc.in))
		if err == nil || !strings.Contains(err.Error(), tc.err) {
			t.Errorf("Parse(%q) err = %v, want %q", tc.in, err, tc.err)
		}
	}
	// ns name is accepted whatever it is.
	if _, err := Parse(strings.NewReader("aide ns:weird/name! stack a\n")); err != nil {
		t.Errorf("any ns name: %v", err)
	}
}

func TestQuote(t *testing.T) {
	tests := map[string]string{
		"":              "''",
		"/a/b-c_d.e":    "/a/b-c_d.e",
		"a:b=c+d@e%f,~": "a:b=c+d@e%f,~",
		"a b":           "'a b'",
		"it's":          `'it'\''s'`,
		"$x":            "'$x'",
		"é":             "'é'",
	}
	for in, want := range tests {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExpandHome(t *testing.T) {
	tests := []struct{ in, want, err string }{
		{"~", "/home/u", ""},
		{"~/x/y", "/home/u/x/y", ""},
		{"/abs", "/abs", ""},
		{"~bob/x", "", "~user"},
		{"rel/x", "", "must be absolute"},
		{"./x", "", "must be absolute"},
	}
	for _, tc := range tests {
		got, err := ExpandHome(tc.in, "/home/u")
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("ExpandHome(%q) err = %v, want %q", tc.in, err, tc.err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("ExpandHome(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestPrintRoundTrip(t *testing.T) {
	mounts := []config.Mount{
		{Host: "/Users/me/my proj's", Container: "/work/a"},
		{Host: "/plain", Container: "/work/b c"},
	}
	out := Print("ns1", []string{"go", "node"}, mounts)
	wantFirst := "aide ns:ns1 stack go node\n"
	if !strings.HasPrefix(out, wantFirst) {
		t.Fatalf("output %q", out)
	}
	cmds, err := Parse(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 3 || cmds[0].Kind != Stack || !reflect.DeepEqual(cmds[0].Args, []string{"go", "node"}) {
		t.Fatalf("cmds %+v", cmds)
	}
	if !reflect.DeepEqual(cmds[1].Args, []string{mounts[0].Host, mounts[0].Container}) ||
		!reflect.DeepEqual(cmds[2].Args, []string{mounts[1].Host, mounts[1].Container}) {
		t.Errorf("mounts %+v", cmds[1:])
	}
	if got := Print("n", nil, nil); got != "" {
		t.Errorf("empty Print = %q", got)
	}
	if got := Print("n", nil, mounts[1:]); got != "aide ns:n mount /plain '/work/b c'\n" {
		t.Errorf("no stacks Print = %q", got)
	}
}
