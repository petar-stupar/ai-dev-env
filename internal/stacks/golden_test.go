package stacks

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/golden from the current catalog")

// goldenCases are computed over the real embedded catalog.
func goldenCases(t *testing.T) map[string][]string {
	return map[string][]string{
		"base":    {"base"},
		"default": parseAideStacks(t, DefaultAide()),
		// Each agent without terminalfs: the policy it gets must stand on
		// its own stack's fragment.
		"claude":   {"base", "claude"},
		"opencode": {"base", "opencode"},
	}
}

// parseAideStacks collects the names of every `aide ns:<x> stack …` line. It
// is deliberately minimal (no quoting); internal/aidefile is the real parser.
func parseAideStacks(t *testing.T, b []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		if len(f) < 3 || f[0] != "aide" || !strings.HasPrefix(f[1], "ns:") {
			t.Fatalf("default.aide: unexpected line %q", sc.Text())
		}
		if f[2] == "stack" {
			out = append(out, f[3:]...)
		}
	}
	return out
}

func TestGolden(t *testing.T) {
	cat, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for name, sel := range goldenCases(t) {
		t.Run(name, func(t *testing.T) {
			var missing []string
			for _, s := range sel {
				if _, ok := cat[s]; !ok {
					missing = append(missing, s)
				}
			}
			if len(missing) > 0 {
				t.Fatalf("golden case %q names stacks that are not in the catalog: %s", name, strings.Join(missing, " "))
			}
			bc, err := Generate(mustResolve(t, cat, sel...))
			if err != nil {
				t.Fatal(err)
			}
			got := map[string][]byte{
				name + ".Dockerfile": bc.Dockerfile,
				name + ".files.txt":  filesListing(bc),
			}
			if f, ok := bc.Files[ContextManagedSettings]; ok {
				got[name+".managed-settings.json"] = f.Data
			}
			if f, ok := bc.Files[ContextOpencode]; ok {
				got[name+".opencode.json"] = f.Data
			}
			dir := filepath.Join("testdata", "golden")
			for fn, data := range got {
				p := filepath.Join(dir, fn)
				if *update {
					if err := os.MkdirAll(dir, 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(p, data, 0o644); err != nil {
						t.Fatal(err)
					}
					continue
				}
				want, err := os.ReadFile(p)
				if err != nil {
					t.Fatalf("%v (run go test ./internal/stacks -update)", err)
				}
				if !bytes.Equal(want, data) {
					t.Errorf("%s differs from the golden file (run go test ./internal/stacks -update and review the diff)\n--- got ---\n%s", p, data)
				}
			}
		})
	}
}

// filesListing is the fingerprint followed by one "path mode sha256" line per
// context entry, Dockerfile included, in path order.
func filesListing(bc *BuildContext) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "fingerprint %s\n", bc.Fingerprint)
	all := bc.Entries()
	for _, p := range sortedKeys(all) {
		fmt.Fprintf(&b, "%s %04o %x\n", p, all[p].Mode.Perm(), sha256.Sum256(all[p].Data))
	}
	return b.Bytes()
}
