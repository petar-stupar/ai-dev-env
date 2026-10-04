package namespace

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/petar-stupar/ai-dev-env/internal/config"
	"github.com/petar-stupar/ai-dev-env/internal/docker"
	"github.com/petar-stupar/ai-dev-env/internal/docker/fake"
	"github.com/petar-stupar/ai-dev-env/internal/platform"
	"github.com/petar-stupar/ai-dev-env/internal/stacks"
)

var update = flag.Bool("update", false, "rewrite testdata/transcripts")

// fakePlat is a platform with fixed answers.
type fakePlat struct {
	unshared   string // host paths under it fail CheckShared
	noAppArmor bool
}

func (p *fakePlat) Kind() platform.Kind { return platform.Linux }
func (p *fakePlat) CheckShared(h string) error {
	if p.unshared != "" && (h == p.unshared || strings.HasPrefix(h, p.unshared+"/")) {
		return fmt.Errorf("host path %s is not shared with Docker; add it under Settings > Resources > File sharing", h)
	}
	return nil
}
func (p *fakePlat) UIDGID() (int, int) { return 501, 20 }
func (p *fakePlat) FreePort(exclude []int) (int, error) {
	for port := 8080; port < 8200; port++ {
		if !slices.Contains(exclude, port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free port")
}
func (p *fakePlat) FilterSecurityOpt(opts []string) []string {
	if !p.noAppArmor {
		return opts
	}
	var out []string
	for _, o := range opts {
		if !strings.HasPrefix(o, "apparmor=") {
			out = append(out, o)
		}
	}
	return out
}

// testCatalog is a synthetic catalog, independent of the embedded stacks.
func testCatalog() map[string]*stacks.Stack {
	mk := func(name string, order int, f func(s *stacks.Stack)) *stacks.Stack {
		s := &stacks.Stack{Name: name, Order: order, Fragment: "RUN echo " + name + "\n",
			Hooks: map[string]stacks.File{}, Files: map[string]stacks.File{}, Contrib: map[string][]byte{}}
		if name != "base" {
			s.Requires = []string{"base"}
		}
		if f != nil {
			f(s)
		}
		return s
	}
	cat := map[string]*stacks.Stack{}
	for _, s := range []*stacks.Stack{
		mk("base", 0, func(s *stacks.Stack) { s.Cache = []string{"xdg"} }),
		mk("tfs", 50, func(s *stacks.Stack) {
			s.Run = stacks.RunReq{CapAdd: []string{"SYS_ADMIN"}, SecurityOpt: []string{"apparmor=unconfined"},
				Env: map[string]string{"TERMINALFS_CLAUDE_STRICT": "1"}}
		}),
		mk("claude", 60, nil),
		mk("oc", 70, func(s *stacks.Stack) { s.Conflicts = []string{"ocfs"} }),
		mk("ocfs", 71, func(s *stacks.Stack) { s.Requires = []string{"tfs"}; s.Conflicts = []string{"oc"} }),
	} {
		cat[s.Name] = s
	}
	return cat
}

// fingerprint is what Build would record for the selection.
func fingerprint(t *testing.T, sel ...string) (*config.Image, *stacks.Resolution) {
	t.Helper()
	res, err := stacks.Resolve(testCatalog(), sel)
	if err != nil {
		t.Fatal(err)
	}
	bc, err := stacks.Generate(res)
	if err != nil {
		t.Fatal(err)
	}
	return &config.Image{ID: "sha256:base1", Stacks: res.Names(), Fingerprint: bc.Fingerprint}, res
}

type harness struct {
	t        *testing.T
	rec      *fake.Recorder
	m        *Manager
	plat     *fakePlat
	out, err bytes.Buffer
	tmp      string // symlink-resolved temp root
	rawTmp   string
	home     string
	prompts  []string
	answer   bool
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	raw := t.TempDir()
	tmp, err := filepath.EvalSymlinks(raw)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, rec: &fake.Recorder{Strict: true}, plat: &fakePlat{}, tmp: tmp, rawTmp: raw, home: tmp}
	for _, d := range []string{"src/a", "src/b", "src/c"} {
		if err := os.MkdirAll(filepath.Join(tmp, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, "src", "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	h.m = &Manager{
		Store:   &config.Store{Root: filepath.Join(tmp, "config")},
		Docker:  &docker.Client{R: h.rec},
		Plat:    h.plat,
		Out:     &h.out,
		Err:     &h.err,
		Catalog: func() (map[string]*stacks.Stack, error) { return testCatalog(), nil },
		Confirm: func(p string) bool {
			h.prompts = append(h.prompts, p)
			return h.answer
		},
	}
	return h
}

func (h *harness) src(name string) string { return filepath.Join(h.tmp, "src", name) }

func (h *harness) mnt(src, ctr string) config.Mount {
	return config.Mount{Host: h.src(src), Container: "/home/agent/workspace/" + ctr}
}

// save writes a namespace's state directly.
func (h *harness) save(ns string, st *config.State) {
	h.t.Helper()
	st.Version = config.CurrentVersion
	if st.Password == "" {
		st.Password = "pw0000000000000" + ns[:1]
	}
	if st.Port == 0 {
		st.Port = 8080
	}
	if err := h.m.Store.Save(ns, st); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) load(ns string) *config.State {
	h.t.Helper()
	st, err := h.m.Store.Load(ns)
	if err != nil {
		h.t.Fatal(err)
	}
	return st
}

func imageJSON(id string, layers int) string {
	ls := make([]string, layers)
	for i := range ls {
		ls[i] = fmt.Sprintf("sha256:%064x", i+1)
	}
	v := map[string]any{
		"Id":       id,
		"RepoTags": []string{},
		"RootFS":   map[string]any{"Type": "layers", "Layers": ls},
		"Config": map[string]any{
			"User":         "root",
			"Env":          []string{"PATH=/home/agent/.local/bin:/opt/node/current/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "AIDE_STACKS=base tfs", "WORKSPACE=/home/agent/workspace"},
			"Entrypoint":   []string{"/etc/aide/entrypoint.sh"},
			"Cmd":          nil,
			"WorkingDir":   "/home/agent/workspace",
			"ExposedPorts": map[string]any{"8080/tcp": map[string]any{}},
			"Labels":       map[string]string{"aide.namespace": "web", "org.opencontainers.image.version": "24.04"},
		},
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func containerJSON(running bool) string {
	status := "exited"
	if running {
		status = "running"
	}
	return fmt.Sprintf(`{"Id":"c0ffee0123456789","Created":"2026-10-01T10:00:00Z","Image":"sha256:base1","State":{"Status":%q,"Running":%t,"Paused":false,"ExitCode":0},"Name":"/aide-web"}`, status, running)
}

// image scripts both inspect forms of ref; repeated calls queue answers.
func (h *harness) image(ref, id string, layers int) {
	h.rec.On([]string{"image", "inspect", "-f", "{{json .}}", ref}, imageJSON(id, layers), 0)
	h.rec.On([]string{"image", "inspect", "-f", "{{.Id}}", ref}, id+"\n", 0)
}

func (h *harness) imageID(ref, id string) {
	h.rec.On([]string{"image", "inspect", "-f", "{{.Id}}", ref}, id+"\n", 0)
}

func (h *harness) noImage(ref string) {
	h.rec.OnError([]string{"image", "inspect", "-f", "{{json .}}", ref}, "Error response from daemon: No such image: "+ref, 1)
}

func (h *harness) container(name string, running bool) {
	h.rec.On([]string{"container", "inspect", "-f", "{{json .}}", name}, containerJSON(running), 0)
}

func (h *harness) noContainer(name string) {
	h.rec.OnError([]string{"container", "inspect", "-f", "{{json .}}", name}, "Error response from daemon: No such container: "+name, 1)
}

// phase scripts Observe's view of ns.
func (h *harness) phase(ns string, p Phase, snap string) {
	if p == NoImage {
		h.noImage(ImageBase(ns))
	} else {
		h.rec.On([]string{"image", "inspect", "-f", "{{json .}}", ImageBase(ns)}, imageJSON("sha256:base1", 30), 0)
	}
	if snap != "" {
		h.rec.On([]string{"image", "inspect", "-f", "{{json .}}", ImageSnap(ns)}, imageJSON(snap, 31), 0)
	}
	switch p {
	case Built:
		h.noContainer(ContainerName(ns))
	case Stopped:
		h.container(ContainerName(ns), false)
	case Running:
		h.container(ContainerName(ns), true)
	}
}

// ok scripts successful calls for each prefix (space separated).
func (h *harness) ok(prefixes ...string) {
	for _, p := range prefixes {
		h.rec.On(strings.Fields(p), "", 0)
	}
}

func (h *harness) fail(prefix, stderr string) {
	h.rec.OnError(strings.Fields(prefix), stderr, 1)
}

const psOutput = `root                                    1       0  3600 /sbin/docker-init -- /etc/aide/entrypoint.sh
agent                                   7       1  3600 /usr/lib/code-server/lib/node /usr/lib/code-server --bind-addr 0.0.0.0:8080 --auth password --disable-telemetry --disable-update-check /home/agent/workspace
agent                                  45       7  3598 /usr/lib/code-server/lib/node /usr/lib/code-server/out/node/entry
agent                                  80      45  3500 /usr/lib/code-server/lib/node --dns-result-order=ipv4first /usr/lib/code-server/lib/vscode/out/bootstrap-fork --type=extensionHost --transformURIs --useHostProxy=false
agent                                  95      45  3400 /usr/lib/code-server/lib/node /usr/lib/code-server/lib/vscode/out/bootstrap-fork --type=ptyHost --logsPath /home/agent/.local/share/code-server/logs
agent                                 120      95  3000 /bin/bash --init-file /usr/lib/code-server/lib/vscode/out/vs/workbench/contrib/terminal/common/scripts/shellIntegration-bash.sh
agent                                 121      95  2900 -bash
agent                                 122      95  2800 bash -l
agent                                 200     120  1200 claude
agent                                 230     200  1100 terminalfs session serve --root /home/agent/mnt/terminalfs-sessions/terminalfs --session 5f1c2a
agent                                 300       1  3590 dotnetdoc --mount --path /home/agent/mnt/dotnetdocfs --port 15640
agent                                 310     121   800 /home/agent/.opencode/bin/opencode
agent                                 400     122    10 sleep 30
agent                                 401     122     5 bash -c npm test
root                                  500       0     0 ps -eo user:32,pid,ppid,etimes,args --no-headers
`

func (h *harness) ps(ns string, out string) {
	h.rec.On([]string{"exec", ContainerName(ns), "ps"}, out, 0)
}

var buildDirRE = regexp.MustCompile(`\S*aide-build-\d+`)

// golden compares the docker transcript, the error and the final state of
// the namespaces with testdata/transcripts/<name>.txt.
func (h *harness) golden(name string, err error, namespaces ...string) {
	h.t.Helper()
	var b strings.Builder
	b.WriteString("# docker\n")
	for _, l := range h.rec.Transcript() {
		b.WriteString(l + "\n")
	}
	b.WriteString("# error\n")
	if err != nil {
		b.WriteString(err.Error() + "\n")
	} else {
		b.WriteString("none\n")
	}
	for _, ns := range namespaces {
		fmt.Fprintf(&b, "# state %s\n", ns)
		data, rerr := os.ReadFile(filepath.Join(h.m.Store.Root, "namespaces", ns, "state.json"))
		if rerr != nil {
			b.WriteString("(none)\n")
			continue
		}
		var st config.State
		if jerr := json.Unmarshal(data, &st); jerr == nil && st.Password != "" {
			data = bytes.ReplaceAll(data, []byte(st.Password), []byte("<password>"))
		}
		b.Write(data)
	}
	got := b.String()
	got = buildDirRE.ReplaceAllString(got, "<build-context>")
	got = strings.ReplaceAll(got, h.tmp, "$TMP")
	got = strings.ReplaceAll(got, h.rawTmp, "$TMP")
	got = regexp.MustCompile(`"fingerprint": "sha256:[0-9a-f]{64}"`).ReplaceAllString(got, `"fingerprint": "<fingerprint>"`)
	got = regexp.MustCompile(`CODE_SERVER_PASSWORD=[a-z0-9]{16}`).ReplaceAllString(got, "CODE_SERVER_PASSWORD=<password>")

	path := filepath.Join("testdata", "transcripts", name+".txt")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			h.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			h.t.Fatal(err)
		}
		return
	}
	want, rerr := os.ReadFile(path)
	if rerr != nil {
		h.t.Fatalf("%v (run with -update)", rerr)
	}
	if got != string(want) {
		h.t.Errorf("%s differs from %s (run with -update to accept)\n--- got\n%s--- want\n%s", name, path, got, want)
	}
}

var ctx = context.Background()
