// Package platform knows the differences between Docker hosts: Linux, Docker
// Desktop, Colima and OrbStack.
package platform

import (
	"context"
	"errors"

	"github.com/petar-stupar/ai-dev-env/internal/docker"
)

// Kind is the Docker host flavour.
type Kind int

const (
	Unknown Kind = iota
	Linux
	DockerDesktop
	Colima
	OrbStack
)

func (k Kind) String() string {
	switch k {
	case Linux:
		return "linux"
	case DockerDesktop:
		return "docker-desktop"
	case Colima:
		return "colima"
	case OrbStack:
		return "orbstack"
	}
	return "unknown"
}

// Platform answers host-specific questions.
type Platform interface {
	Kind() Kind
	// CheckShared returns an error naming the setting to change when hostPath
	// (absolute, symlinks resolved) cannot be bind-mounted on this host.
	CheckShared(hostPath string) error
	// UIDGID is the invoking user's uid and gid, passed as build args.
	UIDGID() (int, int)
	// FreePort picks a host port in 8080-8199 that is not in exclude, not
	// published by a container and not listening.
	FreePort(exclude []int) (int, error)
	// FilterSecurityOpt drops options the daemon cannot honour, such as
	// apparmor=... when it has no AppArmor.
	FilterSecurityOpt(opts []string) []string
}

// Detect inspects `docker info` and the environment.
func Detect(ctx context.Context, d *docker.Client, env func(string) string, home string) (Platform, error) {
	return nil, errors.New("not implemented")
}
