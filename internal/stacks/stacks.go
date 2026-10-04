// Package stacks is the embedded catalog of stacks, their resolution into an
// ordered set, and the generation of a Dockerfile and build context from it.
package stacks

import "io/fs"

// RunReq is what a stack needs from `docker run`.
type RunReq struct {
	CapAdd      []string          `json:"capAdd,omitempty"`
	SecurityOpt []string          `json:"securityOpt,omitempty"`
	Devices     []string          `json:"devices,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
}

// File is one file of a stack or of a generated build context.
type File struct {
	Data []byte
	Mode fs.FileMode
}

// Stack is one catalog entry, loaded from stacks/<name>/.
type Stack struct {
	Name        string
	Description string   `json:"description"`
	Order       int      `json:"order"`
	Requires    []string `json:"requires"`
	Conflicts   []string `json:"conflicts"`
	Suggests    []string `json:"suggests"`
	Run         RunReq   `json:"run"`
	Cache       []string `json:"cache"` // subdirectories of /var/cache/aide

	Fragment string            // Dockerfile fragment, no FROM
	Hooks    map[string]File   // entrypoint.d/NN-name.sh -> file
	Files    map[string]File   // files/<relative path> -> file
	Contrib  map[string][]byte // contrib/claude-managed.json, contrib/opencode.json
}

// Resolution is an ordered, validated set of stacks.
type Resolution struct {
	Stacks   []*Stack
	Warnings []string
}

// Names returns the resolved stack names in order.
func (r *Resolution) Names() []string {
	out := make([]string, 0, len(r.Stacks))
	for _, s := range r.Stacks {
		out = append(out, s.Name)
	}
	return out
}

// Has reports whether a stack is in the resolution.
func (r *Resolution) Has(name string) bool {
	for _, s := range r.Stacks {
		if s.Name == name {
			return true
		}
	}
	return false
}

// BuildContext is a generated Docker build context.
type BuildContext struct {
	Dockerfile  []byte
	Files       map[string]File // paths relative to the context root, Dockerfile excluded
	Fingerprint string          // "sha256:<hex>" over sorted (path, mode, data), Dockerfile included
}

// Paths inside the image that every stack may rely on.
const (
	AgentHome      = "/home/agent"
	CredsDir       = "/home/agent/.credentials"
	CacheDir       = "/var/cache/aide"
	WorkspaceDir   = "/home/agent/workspace"
	EtcDir         = "/etc/aide"
	CodeServerPort = 8080
)
