package docker_test

import (
	"context"
	"encoding/csv"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/petar-stupar/ai-dev-env/internal/docker"
	"github.com/petar-stupar/ai-dev-env/internal/docker/fake"
)

func newClient() (*docker.Client, *fake.Recorder) {
	r := &fake.Recorder{}
	return &docker.Client{R: r}, r
}

func TestArgv(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		do   func(c *docker.Client) error
		want []string
	}{
		{"build", func(c *docker.Client) error {
			_, err := c.Build(ctx, docker.BuildSpec{Tag: "aide-x:base", Dir: "/tmp/ctx", NoCache: true,
				BuildArgs: map[string]string{"AGENT_UID": "501", "AGENT_GID": "20"},
				Labels:    map[string]string{"aide.stacks": "base", "aide.fp": "abc"}})
			return err
		}, []string{
			"docker build -t aide-x:base --no-cache --build-arg AGENT_GID=20 --build-arg AGENT_UID=501 --label aide.fp=abc --label aide.stacks=base /tmp/ctx",
			"docker image inspect -f '{{.Id}}' aide-x:base",
		}},
		{"run", func(c *docker.Client) error {
			_, err := c.RunContainer(ctx, docker.RunSpec{
				Name: "aide-x", Hostname: "x", Image: "aide-x:snap", Publish: "127.0.0.1:8081:8080",
				Mounts: []docker.MountSpec{
					{Type: "volume", Source: "aide-x-creds", Target: "/home/agent/.credentials"},
					{Type: "bind", Source: "/Users/p/src", Target: "/home/agent/workspace/src", ReadOnly: true},
				},
				CapAdd: []string{"SYS_ADMIN"}, SecurityOpt: []string{"apparmor=unconfined"}, Devices: []string{"/dev/fuse"},
				Env:    map[string]string{"Z": "1", "CODE_SERVER_PASSWORD": "pw"},
				Labels: map[string]string{"aide.namespace": "x"}, StopTimeout: 30, Init: true,
			})
			return err
		}, []string{
			"docker run -d --name aide-x --hostname x --init --stop-timeout 30 --label aide.namespace=x --publish 127.0.0.1:8081:8080" +
				" --mount type=volume,src=aide-x-creds,dst=/home/agent/.credentials" +
				" --mount type=bind,src=/Users/p/src,dst=/home/agent/workspace/src,readonly" +
				" --cap-add SYS_ADMIN --security-opt apparmor=unconfined --device /dev/fuse" +
				" --env CODE_SERVER_PASSWORD=pw --env Z=1 aide-x:snap",
		}},
		{"start", func(c *docker.Client) error { return c.Start(ctx, "aide-x") }, []string{"docker start aide-x"}},
		{"stop", func(c *docker.Client) error { return c.Stop(ctx, "aide-x", 30) }, []string{"docker stop -t 30 aide-x"}},
		{"rm", func(c *docker.Client) error { return c.Remove(ctx, "aide-x", false) }, []string{"docker rm aide-x"}},
		{"rm -f", func(c *docker.Client) error { return c.Remove(ctx, "aide-x", true) }, []string{"docker rm -f aide-x"}},
		{"commit", func(c *docker.Client) error { _, err := c.Commit(ctx, "aide-x", "aide-x:snap", false); return err }, []string{
			"docker commit --pause=false aide-x aide-x:snap",
			"docker image inspect -f '{{.Id}}' aide-x:snap",
		}},
		{"commit pause", func(c *docker.Client) error { _, err := c.Commit(ctx, "aide-x", "aide-x:snap", true); return err }, []string{
			"docker commit aide-x aide-x:snap",
			"docker image inspect -f '{{.Id}}' aide-x:snap",
		}},
		{"container", func(c *docker.Client) error { _, err := c.Container(ctx, "aide-x"); return err },
			[]string{"docker container inspect -f '{{json .}}' aide-x"}},
		{"image", func(c *docker.Client) error { _, err := c.Image(ctx, "aide-x:base"); return err },
			[]string{"docker image inspect -f '{{json .}}' aide-x:base"}},
		{"tag", func(c *docker.Client) error { return c.Tag(ctx, "a:snap", "b:snap") }, []string{"docker tag a:snap b:snap"}},
		{"rmi", func(c *docker.Client) error { return c.RemoveImage(ctx, "a:snap") }, []string{"docker image rm a:snap"}},
		{"volume create", func(c *docker.Client) error { return c.VolumeCreate(ctx, "v") }, []string{"docker volume create v"}},
		{"volume rm", func(c *docker.Client) error { return c.VolumeRemove(ctx, "v") }, []string{"docker volume rm v"}},
		{"volume inspect", func(c *docker.Client) error { _, err := c.VolumeExists(ctx, "v"); return err }, []string{"docker volume inspect v"}},
		{"volume clone", func(c *docker.Client) error { return c.VolumeClone(ctx, "a-creds", "b-creds", "aide-a:base") }, []string{
			"docker run --rm --entrypoint cp --mount type=volume,src=a-creds,dst=/from,readonly --mount type=volume,src=b-creds,dst=/to aide-a:base -a /from/. /to/",
		}},
		{"flatten", func(c *docker.Client) error {
			_, err := c.Flatten(ctx, "aide-x", "aide-x:snap", docker.ImageConfig{User: "root", ExposedPorts: []string{"8080/tcp", "53/udp"}}, nil)
			return err
		}, []string{
			"docker export aide-x | docker import --change 'USER root' --change 'EXPOSE 8080' --change 'EXPOSE 53/udp' - aide-x:snap",
			"docker image inspect -f '{{.Id}}' aide-x:snap",
		}},
		{"exec", func(c *docker.Client) error { _, err := c.Exec(ctx, "aide-x", "sh", "-c", "echo hi"); return err },
			[]string{"docker exec aide-x sh -c 'echo hi'"}},
		{"processes", func(c *docker.Client) error { _, err := c.Processes(ctx, "aide-x"); return err },
			[]string{"docker exec aide-x ps -eo user:32,pid,ppid,etimes,args --no-headers"}},
		{"info", func(c *docker.Client) error { _, err := c.Info(ctx); return err },
			[]string{"docker info --format '{{json .}}'"}},
		{"ports", func(c *docker.Client) error { _, err := c.PublishedPorts(ctx); return err },
			[]string{"docker ps --format '{{.Ports}}'"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, r := newClient()
			// JSON-parsing methods need valid JSON.
			r.On([]string{"container", "inspect"}, "{}", 0)
			r.On([]string{"image", "inspect", "-f", "{{json .}}"}, "{}", 0)
			r.On([]string{"info"}, "{}", 0)
			if err := tt.do(c); err != nil {
				t.Fatal(err)
			}
			if got := r.Transcript(); !slices.Equal(got, tt.want) {
				t.Errorf("transcript\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

func TestReturnsIDs(t *testing.T) {
	ctx := context.Background()
	c, r := newClient()
	r.On([]string{"run"}, "abc123\n", 0)
	r.On([]string{"image", "inspect", "-f", "{{.Id}}"}, "sha256:feed\n", 0)
	if id, err := c.RunContainer(ctx, docker.RunSpec{Name: "n", Image: "i"}); err != nil || id != "abc123" {
		t.Errorf("run id = %q, %v", id, err)
	}
	if id, err := c.Commit(ctx, "n", "i:snap", false); err != nil || id != "sha256:feed" {
		t.Errorf("commit id = %q, %v", id, err)
	}
	r.On([]string{"container", "inspect"}, `{"Id":"abc123","Image":"sha256:feed","State":{"Running":true,"Status":"running"}}`, 0)
	ci, err := c.Container(ctx, "n")
	if err != nil || *ci != (docker.ContainerInfo{ID: "abc123", Running: true, Image: "sha256:feed"}) {
		t.Errorf("container = %+v, %v", ci, err)
	}
}

func TestMountCSVQuoting(t *testing.T) {
	src := `/Users/p/a,b "c"`
	arg := docker.MountArg(docker.MountSpec{Type: "bind", Source: src, Target: "/w", ReadOnly: true})
	want := `type=bind,"src=/Users/p/a,b ""c""",dst=/w,readonly`
	if arg != want {
		t.Fatalf("MountArg = %s, want %s", arg, want)
	}
	// docker reads --mount with encoding/csv; check the round trip.
	fields, err := csv.NewReader(strings.NewReader(arg)).Read()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fields, []string{"type=bind", "src=" + src, "dst=/w", "readonly"}) {
		t.Errorf("csv fields = %q", fields)
	}
	c, r := newClient()
	if _, err := c.RunContainer(context.Background(), docker.RunSpec{Name: "n", Image: "i",
		Mounts: []docker.MountSpec{{Type: "bind", Source: src, Target: "/w", ReadOnly: true}}}); err != nil {
		t.Fatal(err)
	}
	if got := r.Calls()[0].Args; !slices.Contains(got, want) {
		t.Errorf("run argv %q lacks %s", got, want)
	}
}

func TestNotFound(t *testing.T) {
	ctx := context.Background()
	c, r := newClient()
	r.OnError([]string{"container", "inspect"}, "Error: No such container: x", 1)
	r.OnError([]string{"image", "inspect"}, "Error response from daemon: No such image: x:snap", 1)
	r.OnError([]string{"volume", "inspect"}, "Error response from daemon: get v: no such volume", 1)
	r.OnError([]string{"stop"}, "Error response from daemon: permission denied", 1)

	_, err := c.Container(ctx, "x")
	if !errors.Is(err, docker.ErrNotFound) {
		t.Errorf("Container err = %v, want ErrNotFound", err)
	}
	var ee *docker.ExitError
	if !errors.As(err, &ee) || ee.Code != 1 || ee.Stderr != "Error: No such container: x" {
		t.Errorf("ExitError = %+v", ee)
	}
	if _, err := c.Image(ctx, "x:snap"); !errors.Is(err, docker.ErrNotFound) {
		t.Errorf("Image err = %v", err)
	}
	if ok, err := c.VolumeExists(ctx, "v"); ok || err != nil {
		t.Errorf("VolumeExists = %v, %v", ok, err)
	}
	if err := c.Stop(ctx, "x", 1); err == nil || errors.Is(err, docker.ErrNotFound) {
		t.Errorf("Stop err = %v, want a non-NotFound error", err)
	}
}

func TestImageParse(t *testing.T) {
	data, err := os.ReadFile("testdata/image_inspect.json")
	if err != nil {
		t.Fatal(err)
	}
	c, r := newClient()
	r.On([]string{"image", "inspect"}, string(data), 0)
	info, err := c.Image(context.Background(), "ai-dev-env:latest")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "sha256:0f045615e4ccc1065e33e9a9835cf2497fe2377f498bdfafc03417abdfa1065f" {
		t.Errorf("ID = %s", info.ID)
	}
	if info.Layers != 5 {
		t.Errorf("Layers = %d", info.Layers)
	}
	cfg := info.Config
	if !slices.Equal(cfg.ExposedPorts, []string{"15641/tcp", "8080/tcp", "9000/udp"}) {
		t.Errorf("ExposedPorts = %q", cfg.ExposedPorts)
	}
	if !slices.Equal(cfg.Entrypoint, []string{"/usr/local/bin/entrypoint.sh"}) ||
		!slices.Equal(cfg.Cmd, []string{"--bind-addr", "0.0.0.0:8080"}) {
		t.Errorf("Entrypoint/Cmd = %q %q", cfg.Entrypoint, cfg.Cmd)
	}
	if len(cfg.Env) != 19 || cfg.Env[1] != "APP_UID=1654" {
		t.Errorf("Env = %q", cfg.Env)
	}
	if cfg.User != "root" || cfg.WorkingDir != "/home/agent/workspace" || cfg.StopSignal != "SIGTERM" {
		t.Errorf("User/WorkingDir/StopSignal = %q %q %q", cfg.User, cfg.WorkingDir, cfg.StopSignal)
	}
	if cfg.Labels["aide.stacks"] != "base,dotnet" || cfg.Labels["org.opencontainers.image.version"] != "24.04" {
		t.Errorf("Labels = %v", cfg.Labels)
	}
}

func TestProcesses(t *testing.T) {
	out := "" +
		"root                                   1       0     3600 /sbin/docker-init -- /usr/local/bin/entrypoint.sh\n" +
		"agent                                 42       1      120 node /usr/lib/code-server/out/node/entry.js --bind-addr 0.0.0.0:8080  --auth password\n" +
		"agent                                 77      42        5 bash\n" +
		"\n" +
		"garbage line\n" +
		"agent  x  1  2  bad pid\n"
	c, r := newClient()
	r.On([]string{"exec", "aide-x", "ps"}, out, 0)
	procs, err := c.Processes(context.Background(), "aide-x")
	if err != nil {
		t.Fatal(err)
	}
	want := []docker.Proc{
		{User: "root", PID: 1, PPID: 0, Elapsed: 3600, Args: "/sbin/docker-init -- /usr/local/bin/entrypoint.sh"},
		{User: "agent", PID: 42, PPID: 1, Elapsed: 120, Args: "node /usr/lib/code-server/out/node/entry.js --bind-addr 0.0.0.0:8080  --auth password"},
		{User: "agent", PID: 77, PPID: 42, Elapsed: 5, Args: "bash"},
	}
	if !slices.Equal(procs, want) {
		t.Errorf("procs\n got %+v\nwant %+v", procs, want)
	}
}

func TestPublishedPorts(t *testing.T) {
	c, r := newClient()
	r.On([]string{"ps"}, "127.0.0.1:8081->8080/tcp, :::8082->8080/tcp\n\n0.0.0.0:8081->8080/tcp, [::]:8081->8080/tcp, 5432/tcp\n0.0.0.0:9000-9001->9000-9001/tcp\n", 0)
	ports, err := c.PublishedPorts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{8081, 8082, 9000, 9001}; !slices.Equal(ports, want) {
		t.Errorf("ports = %v, want %v", ports, want)
	}
}

func TestFlattenChanges(t *testing.T) {
	cfg := docker.ImageConfig{
		Entrypoint:   []string{"/usr/local/bin/entrypoint.sh"},
		Cmd:          []string{"sh", "-c", "a && b <x>"},
		Env:          []string{"PATH=/usr/bin:/bin", "OPTS=--a=1 --b=2 $HOME \"q\" \\"},
		User:         "agent",
		WorkingDir:   "/home/agent/workspace",
		ExposedPorts: []string{"8080/tcp", "53/udp"},
		Labels:       map[string]string{"b": "two words", "a": "1"},
		StopSignal:   "SIGTERM",
	}
	want := []string{
		`ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]`,
		`CMD ["sh","-c","a && b <x>"]`,
		`ENV PATH="/usr/bin:/bin"`,
		`ENV OPTS="--a=1 --b=2 \$HOME \"q\" \\"`,
		`USER agent`,
		`WORKDIR /home/agent/workspace`,
		`EXPOSE 8080`,
		`EXPOSE 53/udp`,
		`LABEL "a"="1"`,
		`LABEL "b"="two words"`,
		`STOPSIGNAL SIGTERM`,
	}
	if got := docker.FlattenChanges(cfg); !slices.Equal(got, want) {
		t.Errorf("changes\n got %q\nwant %q", got, want)
	}
	if got := docker.FlattenChanges(docker.ImageConfig{}); len(got) != 0 {
		t.Errorf("empty config changes = %q", got)
	}

	c, r := newClient()
	r.On([]string{"image", "inspect"}, "sha256:new\n", 0)
	id, err := c.Flatten(context.Background(), "aide-x", "aide-x:snap", cfg, nil)
	if err != nil || id != "sha256:new" {
		t.Fatalf("Flatten = %q, %v", id, err)
	}
	call := r.Calls()[0]
	if !call.Pipe || !slices.Equal(call.Args, []string{"export", "aide-x"}) {
		t.Errorf("pipe source = %+v", call)
	}
	wantTo := []string{"import"}
	for _, ch := range want {
		wantTo = append(wantTo, "--change", ch)
	}
	wantTo = append(wantTo, "-", "aide-x:snap")
	if !slices.Equal(call.To, wantTo) {
		t.Errorf("pipe sink\n got %q\nwant %q", call.To, wantTo)
	}
}

func TestFlattenFailure(t *testing.T) {
	c, r := newClient()
	r.OnError([]string{"export"}, "Error response from daemon: No such container: aide-x", 1)
	_, err := c.Flatten(context.Background(), "aide-x", "r", docker.ImageConfig{}, nil)
	if !errors.Is(err, docker.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	if len(r.Transcript()) != 1 {
		t.Errorf("inspect ran after failed pipe: %q", r.Transcript())
	}
}

func TestRealDocker(t *testing.T) {
	if os.Getenv("AIDE_DOCKER_TEST") != "1" {
		t.Skip("set AIDE_DOCKER_TEST=1 to run against the real docker CLI")
	}
	var verbose strings.Builder
	e := &docker.Exec{Bin: os.Getenv("AIDE_DOCKER"), Verbose: &verbose}
	res, err := e.Run(context.Background(), []string{"version", "--format", "{{.Server.Version}}"}, docker.IO{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(res.Stdout)) == "" {
		t.Error("empty server version")
	}
	if verbose.String() != "+ docker version --format '{{.Server.Version}}'\n" {
		t.Errorf("verbose = %q", verbose.String())
	}
	_, err = (&docker.Client{R: e}).Container(context.Background(), "aide-test-no-such-container-zz")
	if !errors.Is(err, docker.ErrNotFound) {
		t.Errorf("missing container err = %v", err)
	}
}
