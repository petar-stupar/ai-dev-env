package stacks

import (
	"embed"
	"io/fs"
)

// catalogFS holds every stack directory and default.aide. Each pattern must
// match at least one file at compile time, so at least one stack has to ship a
// Dockerfile, an entrypoint.d hook, a files/ tree and a contrib/ document.
//
//go:embed */stack.json */Dockerfile all:*/entrypoint.d all:*/files all:*/contrib default.aide
var catalogFS embed.FS

// DefaultAide returns the embedded default.aide.
func DefaultAide() []byte {
	b, err := fs.ReadFile(catalogFS, "default.aide")
	if err != nil {
		panic("stacks: embedded default.aide missing: " + err.Error())
	}
	return b
}
