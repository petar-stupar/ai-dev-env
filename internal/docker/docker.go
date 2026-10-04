// Package docker is a thin, testable wrapper around the docker CLI. aide calls
// the CLI rather than the SDK so it follows the user's docker context.
package docker

import (
	"context"
	"errors"
	"io"
)

// ErrNotFound is returned when docker reports no such container, image or volume.
var ErrNotFound = errors.New("not found")

// IO optionally streams a command's stdio. A nil writer means capture.
type IO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Result is a finished docker command.
type Result struct {
	Stdout []byte
	Stderr []byte
	Code   int
}

// Runner runs docker commands. Exec is the real one; fake.Recorder is for tests.
type Runner interface {
	// Run runs `docker <args>`. A non-zero exit is returned as an error that
	// wraps *ExitError; Result still carries the output.
	Run(ctx context.Context, args []string, io IO) (Result, error)
	// Pipe runs `docker <from> | docker <to>`, for export|import.
	Pipe(ctx context.Context, from, to []string, stderr io.Writer) error
}

// ExitError is a docker command that exited non-zero.
type ExitError struct {
	Args   []string
	Code   int
	Stderr string
}

func (e *ExitError) Error() string { return "docker " + join(e.Args) + ": " + e.Stderr }

func join(a []string) string {
	s := ""
	for i, x := range a {
		if i > 0 {
			s += " "
		}
		s += x
	}
	return s
}

// Exec runs the real docker binary.
type Exec struct {
	Bin     string    // "docker" or $AIDE_DOCKER
	Verbose io.Writer // when non-nil, every command line is echoed here
}

func (e *Exec) Run(ctx context.Context, args []string, io IO) (Result, error) {
	return Result{}, errors.New("not implemented")
}

func (e *Exec) Pipe(ctx context.Context, from, to []string, stderr io.Writer) error {
	return errors.New("not implemented")
}

// ContainerInfo is what aide needs from `docker container inspect`.
type ContainerInfo struct {
	ID      string
	Running bool
	Image   string // image ID
}

// ImageConfig is the subset of an image's Config that flatten re-applies.
type ImageConfig struct {
	Entrypoint   []string
	Cmd          []string
	Env          []string
	User         string
	WorkingDir   string
	ExposedPorts []string // "8080/tcp"
	Labels       map[string]string
	StopSignal   string
}

// ImageInfo is what aide needs from `docker image inspect`.
type ImageInfo struct {
	ID     string
	Layers int // len(RootFS.Layers)
	Config ImageConfig
}

// Proc is one line of `ps` inside a container.
type Proc struct {
	User    string
	PID     int
	PPID    int
	Elapsed int // seconds
	Args    string
}

// Info is what aide needs from `docker info`.
type Info struct {
	OperatingSystem string
	Name            string
	Driver          string
	SecurityOptions []string
}

// BuildSpec is one `docker build`.
type BuildSpec struct {
	Tag       string
	Dir       string
	NoCache   bool
	BuildArgs map[string]string
	Labels    map[string]string
	Progress  io.Writer // build output
}

// MountSpec is one --mount.
type MountSpec struct {
	Type     string // bind | volume
	Source   string
	Target   string
	ReadOnly bool
}

// RunSpec is one `docker run -d`.
type RunSpec struct {
	Name        string
	Hostname    string
	Image       string
	Publish     string // "127.0.0.1:8081:8080"
	Mounts      []MountSpec
	CapAdd      []string
	SecurityOpt []string
	Devices     []string
	Env         map[string]string
	Labels      map[string]string
	StopTimeout int
	Init        bool
}

// Client is the typed API over a Runner. Every method is one docker command.
type Client struct {
	R Runner
}

func (c *Client) Build(ctx context.Context, s BuildSpec) (id string, err error) {
	return "", errors.New("not implemented")
}
func (c *Client) RunContainer(ctx context.Context, s RunSpec) (id string, err error) {
	return "", errors.New("not implemented")
}
func (c *Client) Start(ctx context.Context, name string) error { return errors.New("not implemented") }
func (c *Client) Stop(ctx context.Context, name string, timeout int) error {
	return errors.New("not implemented")
}
func (c *Client) Remove(ctx context.Context, name string, force bool) error {
	return errors.New("not implemented")
}
func (c *Client) Commit(ctx context.Context, name, ref string, pause bool) (id string, err error) {
	return "", errors.New("not implemented")
}
func (c *Client) Container(ctx context.Context, name string) (*ContainerInfo, error) {
	return nil, errors.New("not implemented")
}
func (c *Client) Image(ctx context.Context, ref string) (*ImageInfo, error) {
	return nil, errors.New("not implemented")
}
func (c *Client) Tag(ctx context.Context, src, dst string) error {
	return errors.New("not implemented")
}
func (c *Client) RemoveImage(ctx context.Context, ref string) error {
	return errors.New("not implemented")
}
func (c *Client) VolumeCreate(ctx context.Context, name string) error {
	return errors.New("not implemented")
}
func (c *Client) VolumeRemove(ctx context.Context, name string) error {
	return errors.New("not implemented")
}
func (c *Client) VolumeExists(ctx context.Context, name string) (bool, error) {
	return false, errors.New("not implemented")
}

// VolumeClone copies src into dst using helperImage (`cp -a`), so nothing is pulled.
func (c *Client) VolumeClone(ctx context.Context, src, dst, helperImage string) error {
	return errors.New("not implemented")
}

// Flatten runs `docker export <ctr> | docker import --change ... - <ref>` with
// one --change per config item, and returns the new image ID.
func (c *Client) Flatten(ctx context.Context, container, ref string, cfg ImageConfig, stderr io.Writer) (id string, err error) {
	return "", errors.New("not implemented")
}
func (c *Client) Exec(ctx context.Context, name string, args ...string) (Result, error) {
	return Result{}, errors.New("not implemented")
}

// Processes runs `ps -eo user:32,pid,ppid,etimes,args --no-headers` in the container.
func (c *Client) Processes(ctx context.Context, name string) ([]Proc, error) {
	return nil, errors.New("not implemented")
}
func (c *Client) Info(ctx context.Context) (*Info, error) { return nil, errors.New("not implemented") }

// PublishedPorts returns host ports published by any container.
func (c *Client) PublishedPorts(ctx context.Context) ([]int, error) {
	return nil, errors.New("not implemented")
}
