// Package config holds aide's local state: the configuration directory, one
// state.json per namespace, and the seeded default.aide.
package config

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
)

// ErrNoNamespace is returned when a namespace has no state directory.
var ErrNoNamespace = errors.New("no such namespace")

// Mount is one host directory bind-mounted at a container path. Host is
// absolute and symlink-resolved; Container is absolute and cleaned.
type Mount struct {
	Host      string `json:"host"`
	Container string `json:"container"`
}

// Image describes the base image a namespace was last built into.
type Image struct {
	ID          string   `json:"id"`
	Stacks      []string `json:"stacks"`      // resolved order the image was built from
	Fingerprint string   `json:"fingerprint"` // sha256 of the generated build context
}

// State is the persisted configuration of one namespace (state.json).
type State struct {
	Version  int      `json:"version"`
	Stacks   []string `json:"stacks"` // as selected, deduplicated, in selection order
	Mounts   []Mount  `json:"mounts"`
	Image    *Image   `json:"image,omitempty"`
	Snapshot string   `json:"snapshot,omitempty"` // image ID of aide-<ns>:snap, "" when none
	Port     int      `json:"port"`
	Password string   `json:"password"`
	// AppliedMounts is the mount set the current container was created with.
	// nil means there is no container; an empty slice means a container with
	// no mounts.
	AppliedMounts []Mount `json:"appliedMounts"`
}

// CurrentVersion is the state.json schema version this binary writes.
const CurrentVersion = 1

// Dir returns the aide configuration directory: $XDG_CONFIG_HOME/aide, else
// $HOME/.config/aide, on every platform. env looks up environment variables.
func Dir(env func(string) string) (string, error) {
	if x := env("XDG_CONFIG_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "aide"), nil
	}
	home := env("HOME")
	if home == "" {
		return "", errors.New("cannot locate the config directory: HOME is not set")
	}
	return filepath.Join(home, ".config", "aide"), nil
}

// Store reads and writes namespaces under Root (the directory Dir returns).
// Layout: Root/default.aide, Root/namespaces/<ns>/state.json, Root/namespaces/<ns>/.lock.
type Store struct {
	Root string
}

func (s *Store) nsDir(ns string) string     { return filepath.Join(s.Root, "namespaces", ns) }
func (s *Store) statePath(ns string) string { return filepath.Join(s.nsDir(ns), "state.json") }

// Load reads a namespace's state. It returns ErrNoNamespace when none exists.
func (s *Store) Load(ns string) (*State, error) {
	data, err := os.ReadFile(s.statePath(ns))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("namespace %s: %w", ns, ErrNoNamespace)
	}
	if err != nil {
		return nil, fmt.Errorf("read state of namespace %s: %w", ns, err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse state of namespace %s: %w", ns, err)
	}
	if st.Version != CurrentVersion {
		return nil, fmt.Errorf("namespace %s has unsupported state version %d (this aide supports version %d)", ns, st.Version, CurrentVersion)
	}
	return &st, nil
}

// Save writes state atomically (temp file, fsync, rename), mode 0600, creating
// the namespace directory if needed.
func (s *Store) Save(ns string, st *State) error {
	dir := s.nsDir(ns)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create namespace directory: %w", err)
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, "state.json.*.tmp")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}
	tmpName := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail(fmt.Errorf("chmod temp state file: %w", err))
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(fmt.Errorf("write temp state file: %w", err))
	}
	if err := tmp.Sync(); err != nil {
		return fail(fmt.Errorf("sync temp state file: %w", err))
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp state file: %w", err)
	}
	if err := os.Rename(tmpName, s.statePath(ns)); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("replace state file: %w", err)
	}
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open namespace directory: %w", err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("sync namespace directory: %w", err)
	}
	return nil
}

// Delete removes a namespace's directory.
func (s *Store) Delete(ns string) error {
	if err := os.RemoveAll(s.nsDir(ns)); err != nil {
		return fmt.Errorf("remove namespace %s: %w", ns, err)
	}
	return nil
}

// Exists reports whether a namespace has a state file.
func (s *Store) Exists(ns string) (bool, error) {
	_, err := os.Stat(s.statePath(ns))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("stat state of namespace %s: %w", ns, err)
}

// List returns namespace names, sorted.
func (s *Store) List() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.Root, "namespaces"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if ok, _ := s.Exists(e.Name()); ok {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// Lock takes an exclusive, non-blocking flock on the namespace. A held lock
// yields an error that says the namespace is busy.
func (s *Store) Lock(ns string) (unlock func(), err error) {
	dir := s.nsDir(ns)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create namespace directory: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("namespace %s is busy: another aide command holds its lock", ns)
		}
		return nil, fmt.Errorf("lock namespace %s: %w", ns, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// DefaultAide returns Root/default.aide, seeding it from seed when missing.
func (s *Store) DefaultAide(seed []byte) ([]byte, error) {
	path := filepath.Join(s.Root, "default.aide")
	data, err := os.ReadFile(path)
	if err == nil {
		return data, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read default.aide: %w", err)
	}
	if err := os.MkdirAll(s.Root, 0o700); err != nil {
		return nil, fmt.Errorf("create config directory: %w", err)
	}
	if err := os.WriteFile(path, seed, 0o644); err != nil {
		return nil, fmt.Errorf("seed default.aide: %w", err)
	}
	return seed, nil
}

// Ports returns every port saved in any namespace's state, for free-port selection.
func (s *Store) Ports() ([]int, error) {
	names, err := s.List()
	if err != nil {
		return nil, err
	}
	var ports []int
	for _, n := range names {
		st, err := s.Load(n)
		if err != nil {
			continue
		}
		ports = append(ports, st.Port)
	}
	return ports, nil
}

var nameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// ValidName checks a namespace name: ^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$,
// which is safe as a hostname, a docker tag and a directory name.
func ValidName(ns string) error {
	if !nameRE.MatchString(ns) {
		return fmt.Errorf("invalid namespace name %q: use 1 to 32 lowercase letters, digits or dashes, starting and ending with a letter or digit", ns)
	}
	return nil
}

const passwordAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// NewPassword returns 16 characters from crypto/rand, alphanumeric.
func NewPassword() (string, error) {
	var b strings.Builder
	max := big.NewInt(int64(len(passwordAlphabet)))
	for i := 0; i < 16; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("generate password: %w", err)
		}
		b.WriteByte(passwordAlphabet[n.Int64()])
	}
	return b.String(), nil
}
