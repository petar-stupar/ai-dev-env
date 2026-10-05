package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// IsNotFoundMessage reports whether docker's stderr says the object is missing.
// Only the daemon's own "no such <object>" wording counts: a bare "not found"
// also appears in unrelated failures (a missing content digest, a missing
// executable), and treating those as "already gone" loses track of objects.
func IsNotFoundMessage(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "no such container") ||
		strings.Contains(s, "no such image") ||
		strings.Contains(s, "no such volume") ||
		strings.Contains(s, "no such object")
}

// mapNotFound makes err match ErrNotFound (errors.Is) when it is an
// *ExitError whose stderr reports a missing object; *ExitError stays
// reachable through errors.As.
func mapNotFound(err error) error {
	var ee *ExitError
	if errors.As(err, &ee) && IsNotFoundMessage(ee.Stderr) {
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	}
	return err
}

// run runs one command with captured output and maps not-found errors.
func (c *Client) run(ctx context.Context, args ...string) (Result, error) {
	res, err := c.R.Run(ctx, args, IO{})
	return res, mapNotFound(err)
}

func (c *Client) out(ctx context.Context, args ...string) (string, error) {
	res, err := c.run(ctx, args...)
	return strings.TrimSpace(string(res.Stdout)), err
}

func (c *Client) imageID(ctx context.Context, ref string) (string, error) {
	return c.out(ctx, "image", "inspect", "-f", "{{.Id}}", ref)
}

func sortedKV(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		out = append(out, k+"="+m[k])
	}
	return out
}

// BuildArgs returns the argv of `docker build` for s.
func BuildArgs(s BuildSpec) []string {
	args := []string{"build", "-t", s.Tag}
	if s.NoCache {
		args = append(args, "--no-cache")
	}
	for _, kv := range sortedKV(s.BuildArgs) {
		args = append(args, "--build-arg", kv)
	}
	for _, kv := range sortedKV(s.Labels) {
		args = append(args, "--label", kv)
	}
	return append(args, s.Dir)
}

func (c *Client) Build(ctx context.Context, s BuildSpec) (id string, err error) {
	io := IO{Stdout: s.Progress, Stderr: s.Progress}
	if _, err := c.R.Run(ctx, BuildArgs(s), io); err != nil {
		return "", mapNotFound(err)
	}
	return c.imageID(ctx, s.Tag)
}

// csvField quotes one --mount field the way encoding/csv reads it back.
func csvField(f string) string {
	if f == "" || !strings.ContainsAny(f, ",\"\r\n") && f[0] != ' ' && f[0] != '\t' {
		return f
	}
	return `"` + strings.ReplaceAll(f, `"`, `""`) + `"`
}

// MountArg renders m as a --mount value, CSV-quoting fields that need it.
func MountArg(m MountSpec) string {
	fields := []string{"type=" + m.Type, "src=" + m.Source, "dst=" + m.Target}
	if m.ReadOnly {
		fields = append(fields, "readonly")
	}
	for i, f := range fields {
		fields[i] = csvField(f)
	}
	return strings.Join(fields, ",")
}

// RunArgs returns the argv of `docker run -d` for s.
func RunArgs(s RunSpec) []string {
	args := []string{"run", "-d", "--name", s.Name}
	if s.Hostname != "" {
		args = append(args, "--hostname", s.Hostname)
	}
	if s.Init {
		args = append(args, "--init")
	}
	if s.StopTimeout > 0 {
		args = append(args, "--stop-timeout", strconv.Itoa(s.StopTimeout))
	}
	for _, kv := range sortedKV(s.Labels) {
		args = append(args, "--label", kv)
	}
	if s.Publish != "" {
		args = append(args, "--publish", s.Publish)
	}
	for _, m := range s.Mounts {
		args = append(args, "--mount", MountArg(m))
	}
	for _, x := range s.CapAdd {
		args = append(args, "--cap-add", x)
	}
	for _, x := range s.SecurityOpt {
		args = append(args, "--security-opt", x)
	}
	for _, x := range s.Devices {
		args = append(args, "--device", x)
	}
	for _, kv := range sortedKV(s.Env) {
		args = append(args, "--env", kv)
	}
	return append(args, s.Image)
}

func (c *Client) RunContainer(ctx context.Context, s RunSpec) (id string, err error) {
	return c.out(ctx, RunArgs(s)...)
}

func (c *Client) Start(ctx context.Context, name string) error {
	_, err := c.run(ctx, "start", name)
	return err
}

func (c *Client) Stop(ctx context.Context, name string, timeout int) error {
	_, err := c.run(ctx, "stop", "-t", strconv.Itoa(timeout), name)
	return err
}

func (c *Client) Remove(ctx context.Context, name string, force bool) error {
	args := []string{"rm"}
	if force {
		args = append(args, "-f")
	}
	_, err := c.run(ctx, append(args, name)...)
	return err
}

func (c *Client) Commit(ctx context.Context, name, ref string, pause bool) (id string, err error) {
	args := []string{"commit"}
	if !pause {
		args = append(args, "--pause=false")
	}
	if _, err := c.run(ctx, append(args, name, ref)...); err != nil {
		return "", err
	}
	return c.imageID(ctx, ref)
}

func (c *Client) inspectJSON(ctx context.Context, v any, args ...string) error {
	res, err := c.run(ctx, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(bytes.TrimSpace(res.Stdout), v); err != nil {
		return fmt.Errorf("docker %s: parse: %w", join(args), err)
	}
	return nil
}

func (c *Client) Container(ctx context.Context, name string) (*ContainerInfo, error) {
	var raw struct {
		ID     string `json:"Id"`
		Image  string
		State  struct{ Running bool }
		Config *struct{ Labels map[string]string }
	}
	if err := c.inspectJSON(ctx, &raw, "container", "inspect", "-f", "{{json .}}", name); err != nil {
		return nil, err
	}
	info := &ContainerInfo{ID: raw.ID, Running: raw.State.Running, Image: raw.Image}
	if raw.Config != nil {
		info.Labels = raw.Config.Labels
	}
	return info, nil
}

// ParseImageInspect parses one object of `docker image inspect` JSON (the
// output of `-f {{json .}}`).
func ParseImageInspect(data []byte) (*ImageInfo, error) {
	var raw struct {
		ID       string `json:"Id"`
		RepoTags []string
		RootFS   struct{ Layers []string }
		Config   *struct {
			Entrypoint   []string
			Cmd          []string
			Env          []string
			User         string
			WorkingDir   string
			ExposedPorts map[string]json.RawMessage
			Labels       map[string]string
			StopSignal   string
		}
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &raw); err != nil {
		return nil, err
	}
	info := &ImageInfo{ID: raw.ID, Tags: raw.RepoTags, Layers: len(raw.RootFS.Layers)}
	if cfg := raw.Config; cfg != nil {
		info.Config = ImageConfig{
			Entrypoint:   cfg.Entrypoint,
			Cmd:          cfg.Cmd,
			Env:          cfg.Env,
			User:         cfg.User,
			WorkingDir:   cfg.WorkingDir,
			ExposedPorts: slices.Sorted(maps.Keys(cfg.ExposedPorts)),
			Labels:       cfg.Labels,
			StopSignal:   cfg.StopSignal,
		}
		if len(info.Config.ExposedPorts) == 0 {
			info.Config.ExposedPorts = nil
		}
	}
	return info, nil
}

func (c *Client) Image(ctx context.Context, ref string) (*ImageInfo, error) {
	args := []string{"image", "inspect", "-f", "{{json .}}", ref}
	res, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	info, err := ParseImageInspect(res.Stdout)
	if err != nil {
		return nil, fmt.Errorf("docker %s: parse: %w", join(args), err)
	}
	return info, nil
}

func (c *Client) Tag(ctx context.Context, src, dst string) error {
	_, err := c.run(ctx, "tag", src, dst)
	return err
}

func (c *Client) RemoveImage(ctx context.Context, ref string) error {
	_, err := c.run(ctx, "image", "rm", ref)
	return err
}

func (c *Client) VolumeCreate(ctx context.Context, name string) error {
	_, err := c.run(ctx, "volume", "create", name)
	return err
}

func (c *Client) VolumeRemove(ctx context.Context, name string) error {
	_, err := c.run(ctx, "volume", "rm", name)
	return err
}

func (c *Client) VolumeExists(ctx context.Context, name string) (bool, error) {
	_, err := c.run(ctx, "volume", "inspect", name)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// VolumeClone copies src into dst using helperImage (`cp -a`), so nothing is pulled.
func (c *Client) VolumeClone(ctx context.Context, src, dst, helperImage string) error {
	_, err := c.run(ctx, "run", "--rm", "--entrypoint", "cp",
		"--mount", MountArg(MountSpec{Type: "volume", Source: src, Target: "/from", ReadOnly: true}),
		"--mount", MountArg(MountSpec{Type: "volume", Source: dst, Target: "/to"}),
		helperImage, "-a", "/from/.", "/to/")
	return err
}

// dfQuote double-quotes s for a Dockerfile instruction word: the parser
// keeps quoted spaces and '=' together and the word expansion that follows
// strips the quotes, so `\`, `"` and `$` are escaped to stay literal.
func dfQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		if r == '"' || r == '$' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

func jsonArray(a []string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(a)
	return strings.TrimSpace(b.String())
}

// FlattenChanges renders cfg as `docker import --change` instructions,
// skipping empty fields. ENV and LABEL use the KEY="value" form with the
// value quoted (see dfQuote): measured against docker 29, an unquoted
// `ENV K=a b` splits into two variables and `$X` is expanded.
func FlattenChanges(cfg ImageConfig) []string {
	var ch []string
	if len(cfg.Entrypoint) > 0 {
		ch = append(ch, "ENTRYPOINT "+jsonArray(cfg.Entrypoint))
	}
	if len(cfg.Cmd) > 0 {
		ch = append(ch, "CMD "+jsonArray(cfg.Cmd))
	}
	for _, e := range cfg.Env {
		k, v, _ := strings.Cut(e, "=")
		if k == "" {
			continue
		}
		ch = append(ch, "ENV "+k+"="+dfQuote(v))
	}
	if cfg.User != "" {
		ch = append(ch, "USER "+cfg.User)
	}
	if cfg.WorkingDir != "" {
		ch = append(ch, "WORKDIR "+cfg.WorkingDir)
	}
	for _, p := range cfg.ExposedPorts {
		ch = append(ch, "EXPOSE "+strings.TrimSuffix(p, "/tcp"))
	}
	for _, k := range slices.Sorted(maps.Keys(cfg.Labels)) {
		ch = append(ch, "LABEL "+dfQuote(k)+"="+dfQuote(cfg.Labels[k]))
	}
	if cfg.StopSignal != "" {
		ch = append(ch, "STOPSIGNAL "+cfg.StopSignal)
	}
	return ch
}

// Flatten runs `docker export <ctr> | docker import --change ... - <ref>` with
// one --change per config item, and returns the new image ID.
func (c *Client) Flatten(ctx context.Context, container, ref string, cfg ImageConfig, stderr io.Writer) (id string, err error) {
	to := []string{"import"}
	for _, ch := range FlattenChanges(cfg) {
		to = append(to, "--change", ch)
	}
	to = append(to, "-", ref)
	if err := c.R.Pipe(ctx, []string{"export", container}, to, stderr); err != nil {
		return "", mapNotFound(err)
	}
	return c.imageID(ctx, ref)
}

// Exec runs `docker exec <name> args...`. Errors are not mapped to
// ErrNotFound: "not found" here usually means a missing executable.
func (c *Client) Exec(ctx context.Context, name string, args ...string) (Result, error) {
	return c.R.Run(ctx, append([]string{"exec", name}, args...), IO{})
}

// Processes runs `ps -eo user:32,pid,ppid,etimes,args --no-headers` in the container.
func (c *Client) Processes(ctx context.Context, name string) ([]Proc, error) {
	res, err := c.Exec(ctx, name, "ps", "-eo", "user:32,pid,ppid,etimes,args", "--no-headers")
	if err != nil {
		return nil, err
	}
	return ParseProcesses(string(res.Stdout)), nil
}

// ParseProcesses parses `ps -eo user,pid,ppid,etimes,args --no-headers`
// output. Lines that do not have four leading fields with numeric
// pid/ppid/etimes are skipped; args keeps its inner spacing.
func ParseProcesses(out string) []Proc {
	var procs []Proc
	for _, line := range strings.Split(out, "\n") {
		rest := strings.TrimSpace(line)
		var f [4]string
		ok := true
		for i := range f {
			end := strings.IndexAny(rest, " \t")
			if end < 0 {
				if i < 3 {
					ok = false
					break
				}
				end = len(rest)
			}
			f[i], rest = rest[:end], strings.TrimLeft(rest[end:], " \t")
		}
		if !ok || f[0] == "" {
			continue
		}
		pid, e1 := strconv.Atoi(f[1])
		ppid, e2 := strconv.Atoi(f[2])
		et, e3 := strconv.Atoi(f[3])
		if e1 != nil || e2 != nil || e3 != nil {
			continue
		}
		procs = append(procs, Proc{User: f[0], PID: pid, PPID: ppid, Elapsed: et, Args: strings.TrimRight(rest, " \t\r")})
	}
	return procs
}

func (c *Client) Info(ctx context.Context) (*Info, error) {
	var raw struct {
		OperatingSystem string
		Name            string
		Driver          string
		SecurityOptions []string
	}
	if err := c.inspectJSON(ctx, &raw, "info", "--format", "{{json .}}"); err != nil {
		return nil, err
	}
	return &Info{OperatingSystem: raw.OperatingSystem, Name: raw.Name, Driver: raw.Driver, SecurityOptions: raw.SecurityOptions}, nil
}

// PublishedPorts returns host ports published by any container.
func (c *Client) PublishedPorts(ctx context.Context) ([]int, error) {
	res, err := c.run(ctx, "ps", "--format", "{{.Ports}}")
	if err != nil {
		return nil, err
	}
	return ParsePorts(string(res.Stdout)), nil
}

// ParsePorts extracts the host ports of every `host:port->` mapping in
// `docker ps --format {{.Ports}}` output, deduplicated and sorted. Ranges
// (`0.0.0.0:8000-8002->...`) contribute every port in the range.
func ParsePorts(out string) []int {
	seen := map[int]bool{}
	for _, entry := range strings.FieldsFunc(out, func(r rune) bool { return r == ',' || r == '\n' }) {
		host, _, ok := strings.Cut(strings.TrimSpace(entry), "->")
		if !ok {
			continue
		}
		i := strings.LastIndex(host, ":")
		if i < 0 {
			continue
		}
		lo, hi, isRange := strings.Cut(host[i+1:], "-")
		a, err := strconv.Atoi(lo)
		if err != nil {
			continue
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(hi); err != nil || b < a {
				continue
			}
		}
		for p := a; p <= b; p++ {
			seen[p] = true
		}
	}
	return slices.Sorted(maps.Keys(seen))
}
