package stacks

import (
	"strings"
	"testing"
)

func mergeStr(t *testing.T, docs ...string) string {
	t.Helper()
	var bs [][]byte
	for _, d := range docs {
		bs = append(bs, []byte(d))
	}
	out, err := MergeJSON(bs...)
	if err != nil {
		t.Fatalf("MergeJSON: %v", err)
	}
	return string(out)
}

func TestMergeKeepsKeyOrder(t *testing.T) {
	got := mergeStr(t,
		`{"z": 1, "permission": {"bash": {"*": "allow", "sudo *": "deny"}}}`,
		`{"a": "<mount>", "permission": {"bash": {"rm -rf *": "deny"}}}`)
	want := `{
  "z": 1,
  "permission": {
    "bash": {
      "*": "allow",
      "sudo *": "deny",
      "rm -rf *": "deny"
    }
  },
  "a": "<mount>"
}
`
	eq(t, got, want)
}

func TestMergeArraysConcatDedup(t *testing.T) {
	got := mergeStr(t,
		`{"allow": ["Bash", "Read(x)", "Bash"], "objs": [{"a": 1, "b": 2}]}`,
		`{"allow": ["Read(x)", "Edit(x)"], "objs": [{"b": 2, "a": 1}, {"a": 3}], "empty": []}`)
	want := `{
  "allow": [
    "Bash",
    "Read(x)",
    "Edit(x)"
  ],
  "objs": [
    {
      "a": 1,
      "b": 2
    },
    {
      "a": 3
    }
  ],
  "empty": []
}
`
	eq(t, got, want)
}

func TestMergeScalarConflict(t *testing.T) {
	_, err := MergeJSON([]byte(`{"permissions": {"defaultMode": "acceptEdits"}}`), []byte(`{"permissions": {"defaultMode": "plan"}}`))
	errContains(t, err, `conflicting values at permissions.defaultMode: "acceptEdits" vs "plan"`)

	_, err = MergeJSON([]byte(`{"a": {"x.y": 1}}`), []byte(`{"a": {"x.y": 2}}`))
	errContains(t, err, `a["x.y"]`)

	_, err = MergeJSON([]byte(`{"a": [1]}`), []byte(`{"a": {"b": 1}}`))
	errContains(t, err, "conflicting values at a")
}

func TestMergeIdenticalScalarsAndNull(t *testing.T) {
	got := mergeStr(t, `{"a": 1, "b": null, "c": true}`, `{"a": 1, "b": "set", "c": null}`)
	eq(t, got, "{\n  \"a\": 1,\n  \"b\": \"set\",\n  \"c\": true\n}\n")
}

func TestMergeNested(t *testing.T) {
	got := mergeStr(t,
		`{"env": {"A": "1"}, "permissions": {"allow": ["Bash"]}}`,
		`{"permissions": {"deny": ["WebSearch"], "allow": ["Read(y)"]}, "env": {"B": "2"}}`,
		`{"env": {"A": "1"}}`)
	want := `{
  "env": {
    "A": "1",
    "B": "2"
  },
  "permissions": {
    "allow": [
      "Bash",
      "Read(y)"
    ],
    "deny": [
      "WebSearch"
    ]
  }
}
`
	eq(t, got, want)
}

func TestMergeEdgeCases(t *testing.T) {
	eq(t, mergeStr(t), "{}\n")
	eq(t, mergeStr(t, `{}`, `{}`), "{}\n")
	_, err := MergeJSON([]byte(`{} {}`))
	errContains(t, err, "trailing data")
	_, err = MergeJSON([]byte(`  `))
	errContains(t, err, "empty document")
	_, err = MergeJSON([]byte(`{"a":`))
	if err == nil {
		t.Fatal("truncated document accepted")
	}
}

func TestMergeRejectsNonObject(t *testing.T) {
	for _, doc := range []string{`["x"]`, `null`, `"s"`, `1`} {
		if _, err := MergeJSON([]byte(doc)); err == nil || !strings.Contains(err.Error(), "must be a JSON object") {
			t.Errorf("MergeJSON(%s) error = %v", doc, err)
		}
	}
}

// opencode reads a permission map in key order, last match wins, and the
// merge appends new keys: a later allow must not land behind earlier denies.
func TestCheckOpencodeOrder(t *testing.T) {
	denies := `{"permission":{"bash":{"*":"allow","git push --force*":"deny"}}}`
	for _, tc := range []struct {
		name, later, want string
	}{
		{"allow behind a deny", `{"permission":{"bash":{"git *":"allow"}}}`, `pattern "git *"`},
		{"ask behind a deny", `{"permission":{"bash":{"git *":"ask"}}}`, `pattern "git *"`},
		{"another deny", `{"permission":{"bash":{"sudo *":"deny"}}}`, ""},
		{"same patterns again", denies, ""},
		{"another tool", `{"permission":{"external_directory":{"/x":"allow"}}}`, ""},
	} {
		err := CheckOpencodeOrder([]byte(denies), []byte(tc.later))
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%s: error = %v, want %q", tc.name, err, tc.want)
		}
	}
	// A map that denies nothing may grow: the default set relies on it.
	if err := CheckOpencodeOrder([]byte(`{"permission":{"external_directory":{"*":"ask"}}}`), []byte(`{"permission":{"external_directory":{"/t":"allow"}}}`)); err != nil {
		t.Error(err)
	}
	// A deny placed before a later "*": "allow" would be cancelled by it.
	if err := CheckOpencodeOrder([]byte(`{"permission":{"bash":{"sudo *":"deny"}}}`), []byte(`{"permission":{"bash":{"*":"allow"}}}`)); err == nil {
		t.Error("a later catch-all allow behind a deny was accepted")
	}
}

func TestCheckFragment(t *testing.T) {
	for _, tc := range []struct {
		name, frag string
		ok         bool
	}{
		{"plain", "RUN true\n", true},
		{"continued RUN mentioning FROM", "RUN echo \\\n    FROM x\n", true},
		{"FROM", "RUN true\nfrom evil\n", false},
		{"FROM split by a continuation", "FROM\\\n x\n", false},
		{"FROM after a comment that ends in a backslash", "# note \\\nFROM evil AS x\n", false},
		{"ends continued", "USER agent\nRUN true \\\n", false},
		{"ends continued before blank lines", "RUN true \\\n\n", false},
		{"comment inside a continued RUN", "RUN true \\\n# why\n    && true\n", true},
	} {
		if err := checkFragment(tc.frag); (err == nil) != tc.ok {
			t.Errorf("%s: error = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}
