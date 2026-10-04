// Package config holds aide's local state: the configuration directory, one
// state.json per namespace, and the seeded default.aide.
package config

import "errors"

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
	return "", errors.New("config.Dir: not implemented")
}

// Store reads and writes namespaces under Root (the directory Dir returns).
// Layout: Root/default.aide, Root/namespaces/<ns>/state.json, Root/namespaces/<ns>/.lock.
type Store struct {
	Root string
}

// Load reads a namespace's state. It returns ErrNoNamespace when none exists.
func (s *Store) Load(ns string) (*State, error) { return nil, errors.New("not implemented") }

// Save writes state atomically (temp file, fsync, rename), mode 0600, creating
// the namespace directory if needed.
func (s *Store) Save(ns string, st *State) error { return errors.New("not implemented") }

// Delete removes a namespace's directory.
func (s *Store) Delete(ns string) error { return errors.New("not implemented") }

// Exists reports whether a namespace has a state file.
func (s *Store) Exists(ns string) (bool, error) { return false, errors.New("not implemented") }

// List returns namespace names, sorted.
func (s *Store) List() ([]string, error) { return nil, errors.New("not implemented") }

// Lock takes an exclusive, non-blocking flock on the namespace. A held lock
// yields an error that says the namespace is busy.
func (s *Store) Lock(ns string) (unlock func(), err error) { return nil, errors.New("not implemented") }

// DefaultAide returns Root/default.aide, seeding it from seed when missing.
func (s *Store) DefaultAide(seed []byte) ([]byte, error) { return nil, errors.New("not implemented") }

// Ports returns every port saved in any namespace's state, for free-port selection.
func (s *Store) Ports() ([]int, error) { return nil, errors.New("not implemented") }

// ValidName checks a namespace name: ^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$,
// which is safe as a hostname, a docker tag and a directory name.
func ValidName(ns string) error { return errors.New("not implemented") }

// NewPassword returns 16 characters from crypto/rand, alphanumeric.
func NewPassword() (string, error) { return "", errors.New("not implemented") }
