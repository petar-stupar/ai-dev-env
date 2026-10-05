package main

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
)

func call(t *testing.T, args ...string) (code int, out, errOut string) {
	t.Helper()
	dir := t.TempDir()
	mk := func(n string) *os.File {
		f, err := os.Create(dir + "/" + n)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}
	in, so, se := mk("in"), mk("out"), mk("err")
	env := func(k string) string {
		switch k {
		case "AIDE_CONFIG_DIR", "XDG_CONFIG_HOME":
			return dir + "/cfg"
		case "HOME":
			return dir
		}
		return ""
	}
	code = run(context.Background(), args, env, in, so, se)
	o, _ := os.ReadFile(so.Name())
	e, _ := os.ReadFile(se.Name())
	return code, string(o), string(e)
}

func TestHelp(t *testing.T) {
	code, out, _ := call(t, "help")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, v := range []string{"ns new", "ns clone", "ns remove", "ns list", "stack", "build", "start", "stop", "reset", "mount", "umount", "state", "dockerfile", "stacks", "version", "help"} {
		if !strings.Contains(out, v) {
			t.Errorf("help lacks %q", v)
		}
	}
	for _, a := range []string{"-h", "--help"} {
		if c, _, _ := call(t, a); c != 0 {
			t.Errorf("%s: %d", a, c)
		}
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{}, {"bogus"}, {"ns:x"}, {"ns:"}, {"ns:x", "frobnicate"}, {"ns"}, {"ns", "bogus"},
		{"ns:x", "mount", "a"}, {"ns:x", "mount", "a", "b", "c"}, {"ns:x", "mount"},
		{"ns:x", "build", "--bogus"}, {"ns:x", "stack"}, {"ns:x", "stop", "extra"},
		{"ns", "clone", "a"}, {"ns", "new"}, {"version", "x"},
	} {
		code, _, errOut := call(t, args...)
		if code != 2 {
			t.Errorf("%v: code %d", args, code)
		}
		if !strings.Contains(errOut, "aide help") {
			t.Errorf("%v: stderr %q", args, errOut)
		}
	}
}

func TestVersionAndStacks(t *testing.T) {
	if c, out, _ := call(t, "version"); c != 0 || strings.TrimSpace(out) != version {
		t.Errorf("version: %d %q", c, out)
	}
	c, out, errOut := call(t, "stacks")
	if c != 0 || !strings.Contains(out, "base") || !strings.Contains(out, "NAME") {
		t.Errorf("stacks: %d %q %q", c, out, errOut)
	}
}

func TestInterspersed(t *testing.T) {
	a, err := parse([]string{"ns:x", "build", "--no-cache"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := parse([]string{"ns:x", "--no-cache", "build"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) || !a.NoCache || a.NS != "x" || a.Verb != "build" {
		t.Errorf("%+v vs %+v", a, b)
	}
	c, err := parse([]string{"-v", "ns:x", "mount", "--yes", "/h", "/c", "-v", "/h2", "/c2"})
	if err != nil {
		t.Fatal(err)
	}
	if !c.Yes || !c.Verbose || !reflect.DeepEqual(c.Args, []string{"/h", "/c", "/h2", "/c2"}) {
		t.Errorf("%+v", c)
	}
	d, err := parse([]string{"ns", "remove", "--keep-volumes", "n", "--yes"})
	if err != nil || !d.Yes || !d.KeepVolumes || d.Args[0] != "n" {
		t.Errorf("%+v %v", d, err)
	}
	e, err := parse([]string{"ns", "new", "n", "-"})
	if err != nil || !reflect.DeepEqual(e.Args, []string{"n", "-"}) {
		t.Errorf("%+v %v", e, err)
	}
}
