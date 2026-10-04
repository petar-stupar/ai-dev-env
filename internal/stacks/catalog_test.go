package stacks

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

func TestCatalogLoads(t *testing.T) {
	cat, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cat["base"]; !ok {
		t.Fatal("catalog has no base stack")
	}
	hookOwner := map[string]string{}
	for _, name := range sortedKeys(cat) {
		s := cat[name]
		if err := ValidateStack(s); err != nil {
			t.Error(err)
		}
		for h := range s.Hooks {
			if other, ok := hookOwner[h]; ok {
				t.Errorf("hook %s in both %s and %s", h, other, name)
			}
			hookOwner[h] = name
		}
		for _, ref := range fragmentRefs(s.Fragment) {
			if !strings.HasPrefix(ref, "stacks/"+name+"/files/") {
				t.Errorf("stack %s: fragment references another stack's file %s", name, ref)
				continue
			}
			if !refExists(s, strings.TrimPrefix(ref, "stacks/"+name+"/files/")) {
				t.Errorf("stack %s: fragment references %s, which is not in files/", name, ref)
			}
		}
		if _, err := Resolve(cat, []string{name}); err != nil {
			t.Errorf("stack %s alone does not resolve: %v", name, err)
		}
	}
}

var refRe = regexp.MustCompile(`(?:^|\s)(stacks/[^\s"']+)`)

// fragmentRefs returns every stacks/... path mentioned in a fragment.
func fragmentRefs(frag string) []string {
	var out []string
	for _, m := range refRe.FindAllStringSubmatch(frag, -1) {
		out = append(out, m[1])
	}
	return out
}

// refExists accepts a file or a directory prefix ending in "/".
func refExists(s *Stack, rel string) bool {
	if strings.HasSuffix(rel, "/") || rel == "" {
		for f := range s.Files {
			if strings.HasPrefix(f, rel) {
				return true
			}
		}
		return false
	}
	_, ok := s.Files[rel]
	return ok
}

func TestCatalogBaseFiles(t *testing.T) {
	cat, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	f, ok := cat["base"].Files["entrypoint.sh"]
	if !ok || f.Mode != 0o755 {
		t.Fatalf("base files/entrypoint.sh missing or wrong mode: %+v", f.Mode)
	}
}

func TestDefaultAide(t *testing.T) {
	d := DefaultAide()
	if !bytes.Contains(d, []byte("aide ns:default stack base ")) {
		t.Fatalf("unexpected default.aide:\n%s", d)
	}
}

func TestLoadCatalogErrors(t *testing.T) {
	ok := func() fstest.MapFS {
		return fstest.MapFS{
			"a/stack.json":            {Data: []byte(`{"description":"a","order":1,"conflicts":"b","cache":["nuget"]}`)},
			"a/Dockerfile":            {Data: []byte("RUN true\n")},
			"a/entrypoint.d/10-a.sh":  {Data: []byte("true\n")},
			"a/files/x/y.sh":          {Data: []byte("#!/bin/sh\n")},
			"a/files/x/z.txt":         {Data: []byte("z")},
			"a/contrib/opencode.json": {Data: []byte(`{"a":1}`)},
			"b/stack.json":            {Data: []byte(`{"requires":["a"],"suggests":["a"]}`)},
			"default.aide":            {Data: []byte("x")},
		}
	}
	cat, err := loadCatalog(ok())
	if err != nil {
		t.Fatal(err)
	}
	a := cat["a"]
	eq(t, a.Conflicts, []string{"b"})
	eq(t, a.Files["x/y.sh"].Mode.Perm().String(), "-rwxr-xr-x")
	eq(t, a.Files["x/z.txt"].Mode.Perm().String(), "-rw-r--r--")
	eq(t, string(a.Contrib["opencode.json"]), `{"a":1}`)
	eq(t, cat["b"].Requires, []string{"a"})

	cases := map[string]struct {
		path, data, want string
	}{
		"unknown field":   {"a/stack.json", `{"order":1,"nope":2}`, `unknown field "nope"`},
		"bad conflicts":   {"a/stack.json", `{"conflicts":3}`, "string or an array"},
		"FROM":            {"a/Dockerfile", "RUN true\nFROM x\n", "line 2: FROM"},
		"hook name":       {"a/entrypoint.d/a.sh", "", "hook names must match"},
		"contrib json":    {"a/contrib/opencode.json", "{", "invalid JSON"},
		"contrib name":    {"a/contrib/other.json", "{}", "unknown document"},
		"no stack.json":   {"c/Dockerfile", "RUN true", "no stack.json"},
		"unknown require": {"b/stack.json", `{"requires":["zz"]}`, `unknown stack "zz"`},
		"cache path":      {"a/stack.json", `{"cache":["../x"]}`, "cache"},
	}
	for name, c := range cases {
		fsys := ok()
		fsys[c.path] = &fstest.MapFile{Data: []byte(c.data)}
		_, err := loadCatalog(fsys)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want %q", name, err, c.want)
		}
	}
}
