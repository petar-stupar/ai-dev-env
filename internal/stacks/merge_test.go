package stacks

import "testing"

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
