package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDir(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    string
		wantErr bool
	}{
		{"xdg absolute", map[string]string{"XDG_CONFIG_HOME": "/x/cfg", "HOME": "/h"}, "/x/cfg/aide", false},
		{"no xdg", map[string]string{"HOME": "/h"}, "/h/.config/aide", false},
		{"relative xdg ignored", map[string]string{"XDG_CONFIG_HOME": "rel", "HOME": "/h"}, "/h/.config/aide", false},
		{"no home", map[string]string{}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Dir(envMap(tt.env))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSaveLoad(t *testing.T) {
	tests := []struct {
		name    string
		applied []Mount
		wantTxt string
	}{
		{"nil", nil, `"appliedMounts": null`},
		{"empty", []Mount{}, `"appliedMounts": []`},
		{"some", []Mount{{Host: "/a", Container: "/b"}}, `"host": "/a"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Store{Root: t.TempDir()}
			st := &State{
				Version: CurrentVersion, Stacks: []string{"base"}, Mounts: []Mount{{Host: "/h", Container: "/c"}},
				Image: &Image{ID: "sha256:x", Stacks: []string{"base"}, Fingerprint: "f"},
				Port:  8081, Password: "pw", AppliedMounts: tt.applied,
			}
			if err := s.Save("dev", st); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(s.Root, "namespaces", "dev", "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), tt.wantTxt) {
				t.Errorf("state.json lacks %q:\n%s", tt.wantTxt, raw)
			}
			if !strings.Contains(string(raw), "\n  \"version\": 1") {
				t.Errorf("not indented with two spaces:\n%s", raw)
			}
			fi, _ := os.Stat(filepath.Join(s.Root, "namespaces", "dev", "state.json"))
			if fi.Mode().Perm() != 0o600 {
				t.Errorf("mode = %v", fi.Mode().Perm())
			}
			got, err := s.Load("dev")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, st) {
				t.Errorf("round trip:\n got %+v\nwant %+v", got, st)
			}
		})
	}
}

func TestLoadErrors(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	if _, err := s.Load("nope"); !errors.Is(err, ErrNoNamespace) {
		t.Errorf("err = %v, want ErrNoNamespace", err)
	}
	dir := filepath.Join(s.Root, "namespaces", "old")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"version": 99}`), 0o600)
	if _, err := s.Load("old"); err == nil || !strings.Contains(err.Error(), "unsupported state version") {
		t.Errorf("err = %v, want unsupported version", err)
	}
}

func TestLeftoverTmpIgnored(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	dir := filepath.Join(s.Root, "namespaces", "crash")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "state.json.123.tmp"), []byte("{"), 0o600)
	if _, err := s.Load("crash"); !errors.Is(err, ErrNoNamespace) {
		t.Errorf("Load err = %v", err)
	}
	names, err := s.List()
	if err != nil || len(names) != 0 {
		t.Errorf("List = %v, %v", names, err)
	}
}

func TestLock(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	unlock, err := s.Lock("a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lock("a"); err == nil || !strings.Contains(err.Error(), "namespace a is busy") {
		t.Errorf("err = %v, want busy", err)
	}
	unlock()
	unlock2, err := s.Lock("a")
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	unlock2()
}

func TestListDeletePorts(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	if names, err := s.List(); err != nil || len(names) != 0 {
		t.Fatalf("empty List = %v, %v", names, err)
	}
	for i, n := range []string{"zeta", "alpha", "mid"} {
		if err := s.Save(n, &State{Version: CurrentVersion, Port: 8000 + i}); err != nil {
			t.Fatal(err)
		}
	}
	// a directory without state.json is not a namespace
	os.MkdirAll(filepath.Join(s.Root, "namespaces", "empty"), 0o700)
	// an unreadable state is skipped by Ports
	os.MkdirAll(filepath.Join(s.Root, "namespaces", "bad"), 0o700)
	os.WriteFile(filepath.Join(s.Root, "namespaces", "bad", "state.json"), []byte("junk"), 0o600)

	names, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"alpha", "bad", "mid", "zeta"}; !reflect.DeepEqual(names, want) {
		t.Errorf("List = %v, want %v", names, want)
	}
	ports, err := s.Ports()
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{8001, 8002, 8000}; !reflect.DeepEqual(ports, want) {
		t.Errorf("Ports = %v, want %v", ports, want)
	}
	if ok, _ := s.Exists("mid"); !ok {
		t.Error("Exists(mid) = false")
	}
	if err := s.Delete("mid"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Exists("mid"); ok {
		t.Error("Exists(mid) after Delete = true")
	}
	if err := s.Delete("mid"); err != nil {
		t.Errorf("second Delete: %v", err)
	}
}

func TestDefaultAide(t *testing.T) {
	s := &Store{Root: filepath.Join(t.TempDir(), "aide")}
	got, err := s.DefaultAide([]byte("seed\n"))
	if err != nil || string(got) != "seed\n" {
		t.Fatalf("seed = %q, %v", got, err)
	}
	p := filepath.Join(s.Root, "default.aide")
	if b, _ := os.ReadFile(p); string(b) != "seed\n" {
		t.Errorf("file = %q", b)
	}
	os.WriteFile(p, []byte("edited\n"), 0o644)
	got, err = s.DefaultAide([]byte("other"))
	if err != nil || string(got) != "edited\n" {
		t.Errorf("second = %q, %v", got, err)
	}
}

func TestValidName(t *testing.T) {
	tests := []struct {
		name string
		ns   string
		ok   bool
	}{
		{"simple", "dev", true},
		{"single char", "a", true},
		{"digits and dash", "a-1-b", true},
		{"max length", strings.Repeat("a", 32), true},
		{"too long", strings.Repeat("a", 33), false},
		{"uppercase", "Dev", false},
		{"leading dash", "-dev", false},
		{"trailing dash", "dev-", false},
		{"empty", "", false},
		{"slash", "a/b", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidName(tt.ns)
			if (err == nil) != tt.ok {
				t.Errorf("ValidName(%q) = %v, ok want %v", tt.ns, err, tt.ok)
			}
			if err != nil && !strings.Contains(err.Error(), "lowercase") {
				t.Errorf("message does not state the rule: %v", err)
			}
		})
	}
}

func TestNewPassword(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		p, err := NewPassword()
		if err != nil {
			t.Fatal(err)
		}
		if len(p) != 16 {
			t.Fatalf("len = %d", len(p))
		}
		for _, c := range p {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
				t.Fatalf("bad char %q in %q", c, p)
			}
		}
		seen[p] = true
	}
	if len(seen) < 49 {
		t.Errorf("passwords not random: %d unique of 50", len(seen))
	}
}
