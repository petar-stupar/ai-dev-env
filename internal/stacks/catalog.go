package stacks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
)

var (
	stackNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	hookNameRe  = regexp.MustCompile(`^\d\d-[a-z0-9-]+\.sh$`)
	cacheDirRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*(/[a-z0-9][a-z0-9._-]*)*$`)
)

// Contrib document names a stack may ship under contrib/.
const (
	ContribClaude   = "claude-managed.json"
	ContribOpencode = "opencode.json"
)

// stringList is a JSON string or array of strings.
type stringList []string

func (l *stringList) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*l = stringList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return errors.New("must be a string or an array of strings")
	}
	*l = many
	return nil
}

// stackJSON is the on-disk shape of stack.json.
type stackJSON struct {
	Description string     `json:"description"`
	Order       int        `json:"order"`
	Requires    stringList `json:"requires"`
	Conflicts   stringList `json:"conflicts"`
	Suggests    stringList `json:"suggests"`
	Run         RunReq     `json:"run"`
	Cache       []string   `json:"cache"`
}

// Catalog loads every embedded stack.
func Catalog() (map[string]*Stack, error) { return loadCatalog(catalogFS) }

func loadCatalog(fsys fs.FS) (map[string]*Stack, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	cat := map[string]*Stack{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		s, err := loadStack(fsys, e.Name())
		if err != nil {
			return nil, err
		}
		cat[s.Name] = s
	}
	for _, s := range cat {
		for _, list := range [][]string{s.Requires, s.Conflicts, s.Suggests} {
			for _, n := range list {
				if _, ok := cat[n]; !ok {
					return nil, fmt.Errorf("stack %s: references unknown stack %q", s.Name, n)
				}
			}
		}
	}
	return cat, nil
}

func loadStack(fsys fs.FS, name string) (*Stack, error) {
	raw, err := fs.ReadFile(fsys, path.Join(name, "stack.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("stack %s: no stack.json", name)
		}
		return nil, fmt.Errorf("stack %s: %w", name, err)
	}
	var sj stackJSON
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sj); err != nil {
		return nil, fmt.Errorf("stack %s: stack.json: %w", name, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("stack %s: stack.json: trailing data", name)
	}
	s := &Stack{
		Name:        name,
		Description: sj.Description,
		Order:       sj.Order,
		Requires:    sj.Requires,
		Conflicts:   sj.Conflicts,
		Suggests:    sj.Suggests,
		Run:         sj.Run,
		Cache:       sj.Cache,
		Hooks:       map[string]File{},
		Files:       map[string]File{},
		Contrib:     map[string][]byte{},
	}

	frag, err := fs.ReadFile(fsys, path.Join(name, "Dockerfile"))
	switch {
	case err == nil:
		s.Fragment = string(frag)
	case !errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("stack %s: %w", name, err)
	}

	if err := readFlatDir(fsys, path.Join(name, "entrypoint.d"), func(fn string, data []byte) error {
		s.Hooks[fn] = File{Data: data, Mode: 0o755}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("stack %s: %w", name, err)
	}

	if err := readFlatDir(fsys, path.Join(name, "contrib"), func(fn string, data []byte) error {
		s.Contrib[fn] = data
		return nil
	}); err != nil {
		return nil, fmt.Errorf("stack %s: %w", name, err)
	}

	root := path.Join(name, "files")
	err = fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipDir
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s: not a regular file", p)
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, root+"/")
		s.Files[rel] = File{Data: data, Mode: fileMode(rel)}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("stack %s: %w", name, err)
	}

	if err := ValidateStack(s); err != nil {
		return nil, err
	}
	return s, nil
}

// fileMode is the mode a stack file gets in the build context.
func fileMode(rel string) fs.FileMode {
	if strings.HasSuffix(rel, ".sh") {
		return 0o755
	}
	return 0o644
}

// readFlatDir calls fn for each regular file directly in dir; a missing dir is
// empty and a subdirectory is an error.
func readFlatDir(fsys fs.FS, dir string, fn func(name string, data []byte) error) error {
	entries, err := fs.ReadDir(fsys, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			return fmt.Errorf("%s/%s: not a regular file", dir, e.Name())
		}
		data, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		if err := fn(e.Name(), data); err != nil {
			return err
		}
	}
	return nil
}

// ValidateStack checks the invariants every stack must hold, whether it came
// from the embedded catalog or was built in code: a valid name, a fragment
// without FROM, hook names matching NN-name.sh, known contrib documents that
// parse as JSON, clean relative file paths and cache directories.
func ValidateStack(s *Stack) error {
	if !stackNameRe.MatchString(s.Name) {
		return fmt.Errorf("stack %q: invalid name", s.Name)
	}
	if err := checkFragment(s.Fragment); err != nil {
		return fmt.Errorf("stack %s: Dockerfile: %w", s.Name, err)
	}
	for _, h := range sortedKeys(s.Hooks) {
		if !hookNameRe.MatchString(h) {
			return fmt.Errorf("stack %s: entrypoint.d/%s: hook names must match %s", s.Name, h, hookNameRe)
		}
	}
	for _, c := range sortedKeys(s.Contrib) {
		if c != ContribClaude && c != ContribOpencode {
			return fmt.Errorf("stack %s: contrib/%s: unknown document (want %s or %s)", s.Name, c, ContribClaude, ContribOpencode)
		}
		if !json.Valid(s.Contrib[c]) {
			return fmt.Errorf("stack %s: contrib/%s: invalid JSON", s.Name, c)
		}
	}
	for _, f := range sortedKeys(s.Files) {
		if f == "" || path.IsAbs(f) || path.Clean(f) != f || f == ".." || strings.HasPrefix(f, "../") {
			return fmt.Errorf("stack %s: files/%s: invalid path", s.Name, f)
		}
	}
	for _, c := range s.Cache {
		if !cacheDirRe.MatchString(c) || strings.Contains(c, "..") {
			return fmt.Errorf("stack %s: cache %q: must be a relative lower-case path", s.Name, c)
		}
	}
	for _, n := range s.Requires {
		if n == s.Name {
			return fmt.Errorf("stack %s: requires itself", s.Name)
		}
	}
	for _, n := range s.Conflicts {
		if n == s.Name {
			return fmt.Errorf("stack %s: conflicts with itself", s.Name)
		}
	}
	return nil
}

// checkFragment rejects a fragment that starts a new build stage: any line
// whose first word is FROM (any case), unless it continues the previous line.
func checkFragment(frag string) error {
	continued := false
	for i, line := range strings.Split(frag, "\n") {
		f := strings.Fields(line)
		if !continued && len(f) > 0 && strings.EqualFold(f[0], "FROM") {
			return fmt.Errorf("line %d: FROM is not allowed in a stack fragment", i+1)
		}
		continued = strings.HasSuffix(strings.TrimRight(line, " \t\r"), "\\")
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
