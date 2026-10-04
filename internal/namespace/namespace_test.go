package namespace

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/petar-stupar/ai-dev-env/internal/config"
	"github.com/petar-stupar/ai-dev-env/internal/docker"
)

func wantErr(t *testing.T, err error, sub string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), sub) {
		t.Fatalf("error = %v; want one containing %q", err, sub)
	}
}

func TestNewBuildFirstStart(t *testing.T) {
	h := newHarness(t)
	aide := "# web\naide ns:any stack tfs claude\naide ns:any stack claude\naide ns:any mount ~/src/a /home/agent/workspace/a/ '~/src/b' /home/agent/workspace/b\n"
	if err := h.m.New(ctx, "web", strings.NewReader(aide), h.home); err != nil {
		t.Fatal(err)
	}
	st := h.load("web")
	if !slices.Equal(st.Stacks, []string{"tfs", "claude"}) || len(st.Mounts) != 2 || st.Port != 8080 || len(st.Password) != 16 || st.AppliedMounts != nil {
		t.Fatalf("state after new: %+v", st)
	}
	// build: no image yet; start: image built, no container.
	h.noImage(ImageBase("web"))
	h.image(ImageBase("web"), "sha256:base1", 30)
	h.noContainer(ContainerName("web"))
	h.ok("build", "volume create", "run")
	if err := h.m.Build(ctx, "web", false); err != nil {
		t.Fatal(err)
	}
	err := h.m.Start(ctx, "web", Options{})
	h.golden("new-build-start", err, "web")
	for _, s := range []string{"http://127.0.0.1:8080", "docker exec -it -u agent aide-web bash -l", st.Password} {
		if !strings.Contains(h.out.String(), s) {
			t.Errorf("output lacks %q:\n%s", s, h.out.String())
		}
	}
	if h.err.Len() != 0 {
		t.Errorf("unexpected warnings: %s", h.err.String())
	}
}

func TestNewErrors(t *testing.T) {
	for _, tc := range []struct{ name, ns, aide, want string }{
		{"bad name", "Web", "", "invalid namespace name"},
		{"unknown stack", "web", "aide ns:x stack nope\n", `unknown stack "nope"`},
		{"conflict", "web", "aide ns:x stack oc ocfs\n", "stacks oc and ocfs conflict"},
		{"parse", "web", "aide ns:x build\n", "line 1"},
		{"relative host", "web", "aide ns:x mount src/a /home/agent/workspace/a\n", "host path must be absolute"},
		{"protected", "web", "aide ns:x mount ~/src/a /etc/x\n", "inside /etc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			wantErr(t, h.m.New(ctx, tc.ns, strings.NewReader(tc.aide), h.home), tc.want)
			if names, _ := h.m.Store.List(); len(names) != 0 {
				t.Errorf("namespaces left behind: %v", names)
			}
		})
	}
	h := newHarness(t)
	h.save("web", &config.State{Stacks: []string{"base"}})
	wantErr(t, h.m.New(ctx, "web", strings.NewReader(""), h.home), "already exists")
}

func TestStartUnchanged(t *testing.T) {
	h := newHarness(t)
	img, _ := fingerprint(t, "base")
	ms := []config.Mount{h.mnt("a", "a")}
	h.save("web", &config.State{Stacks: []string{"base"}, Image: img, Mounts: ms, AppliedMounts: ms})
	h.phase("web", Stopped, "")
	h.ok("start")
	err := h.m.Start(ctx, "web", Options{})
	h.golden("start-unchanged", err, "web")
}

func TestStartChangedMountsWhileStopped(t *testing.T) {
	h := newHarness(t)
	img, _ := fingerprint(t, "base")
	h.save("web", &config.State{Stacks: []string{"base"}, Image: img,
		Mounts: []config.Mount{h.mnt("a", "a"), h.mnt("b", "b")}, AppliedMounts: []config.Mount{h.mnt("a", "a")}})
	h.phase("web", Stopped, "")
	h.ok("commit", "rm", "run")
	h.image(ImageSnap("web"), "sha256:snap1", 31)
	err := h.m.Start(ctx, "web", Options{})
	h.golden("start-changed-mounts-stopped", err, "web")
	if len(h.prompts) != 0 {
		t.Errorf("a stopped remount must not prompt: %q", h.prompts)
	}
}

func TestStartRunningIsNoop(t *testing.T) {
	h := newHarness(t)
	img, _ := fingerprint(t, "base")
	h.save("web", &config.State{Stacks: []string{"base"}, Image: img, AppliedMounts: []config.Mount{}})
	h.phase("web", Running, "")
	if err := h.m.Start(ctx, "web", Options{}); err != nil {
		t.Fatal(err)
	}
	if got := h.rec.Transcript(); len(got) != 2 {
		t.Errorf("start while running ran %q", got)
	}
	if !strings.Contains(h.out.String(), "already running") {
		t.Errorf("output: %s", h.out.String())
	}
}

func TestStartFromSnapshotWithoutContainer(t *testing.T) {
	h := newHarness(t)
	h.plat.noAppArmor = true
	img, _ := fingerprint(t, "tfs")
	h.save("web", &config.State{Stacks: []string{"tfs"}, Image: img, Snapshot: "sha256:snap1"})
	h.phase("web", Built, "sha256:snap1")
	h.ok("volume create", "run")
	err := h.m.Start(ctx, "web", Options{})
	h.golden("start-from-snapshot", err, "web")
}

func TestStaleImage(t *testing.T) {
	h := newHarness(t)
	img, _ := fingerprint(t, "base")
	h.save("web", &config.State{Stacks: []string{"base", "tfs"}, Image: img})
	h.phase("web", Built, "")
	err := h.m.Start(ctx, "web", Options{})
	wantErr(t, err, `image is stale (stacks changed); run "aide ns:web reset", then "aide ns:web build"`)
	h.golden("stale-image", err, "web")
}

func TestFingerprintDriftWarns(t *testing.T) {
	h := newHarness(t)
	img, _ := fingerprint(t, "base")
	img.Fingerprint = "sha256:old"
	h.save("web", &config.State{Stacks: []string{"base"}, Image: img})
	h.phase("web", Built, "")
	h.ok("volume create", "run")
	if err := h.m.Start(ctx, "web", Options{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.err.String(), "stack definitions changed") {
		t.Errorf("no drift warning: %q", h.err.String())
	}
}

func runningWeb(h *harness, snap string) {
	img, _ := fingerprint(h.t, "tfs")
	ms := []config.Mount{h.mnt("a", "a")}
	h.save("web", &config.State{Stacks: []string{"tfs"}, Image: img, Mounts: ms, AppliedMounts: ms, Snapshot: snap})
	h.phase("web", Running, snap)
}

func TestMountWhileRunning(t *testing.T) {
	h := newHarness(t)
	runningWeb(h, "sha256:snap0")
	h.ps("web", psOutput)
	h.ok("stop", "commit", "rm", "run", "image rm")
	h.imageID(ImageSnap("web"), "sha256:snap1")
	h.answer = true
	err := h.m.Mount(ctx, "web", []config.Mount{{Host: "~/src/b", Container: "/home/agent/workspace/b"}}, h.home, Options{})
	h.golden("mount-running", err, "web")
	if len(h.prompts) != 1 {
		t.Errorf("prompts: %q", h.prompts)
	}
	for _, s := range []string{"claude", "terminalfs session serve", "opencode", "stopped in", "committed in", "recreated in"} {
		if !strings.Contains(h.out.String(), s) {
			t.Errorf("output lacks %q:\n%s", s, h.out.String())
		}
	}
}

func TestMountWhileRunningDeclined(t *testing.T) {
	h := newHarness(t)
	runningWeb(h, "")
	h.ps("web", psOutput)
	err := h.m.Mount(ctx, "web", []config.Mount{{Host: h.src("b"), Container: "/home/agent/workspace/b"}}, h.home, Options{})
	wantErr(t, err, "cancelled; nothing changed")
	if got := h.rec.Transcript(); strings.Contains(strings.Join(got, "\n"), "docker stop") {
		t.Errorf("stopped after a declined prompt: %q", got)
	}
	if len(h.load("web").Mounts) != 1 {
		t.Error("mounts changed after a declined prompt")
	}
}

func TestMountWhileRunningIdleNoPrompt(t *testing.T) {
	h := newHarness(t)
	runningWeb(h, "")
	h.ps("web", "agent 7 1 100 /usr/lib/code-server/lib/node /usr/lib/code-server\nagent 9 7 50 -bash\n")
	h.ok("stop", "commit", "rm", "run")
	h.image(ImageSnap("web"), "sha256:snap1", 31)
	if err := h.m.Mount(ctx, "web", []config.Mount{{Host: h.src("b"), Container: "/home/agent/workspace/b"}}, h.home, Options{}); err != nil {
		t.Fatal(err)
	}
	if len(h.prompts) != 0 {
		t.Errorf("prompted with nothing busy: %q", h.prompts)
	}
}

func TestCommitFails(t *testing.T) {
	h := newHarness(t)
	runningWeb(h, "")
	before := h.load("web")
	h.ps("web", "")
	h.ok("stop", "start")
	h.fail("commit", "Error response from daemon: write /var/lib/docker/tmp: no space left on device")
	err := h.m.Mount(ctx, "web", []config.Mount{{Host: h.src("b"), Container: "/home/agent/workspace/b"}}, h.home, Options{Yes: true})
	wantErr(t, err, "commit the container")
	h.golden("commit-fails", err, "web")
	after := h.load("web")
	if !slices.Equal(after.Mounts, before.Mounts) || !slices.Equal(after.AppliedMounts, before.AppliedMounts) || after.Snapshot != "" {
		t.Errorf("state changed: %+v", after)
	}
}

func TestRunFailsRollsBack(t *testing.T) {
	h := newHarness(t)
	runningWeb(h, "")
	h.ps("web", "")
	h.ok("stop", "commit", "rm")
	h.image(ImageSnap("web"), "sha256:snap1", 31)
	h.fail("run", "docker: Error response from daemon: invalid mount config for type \"bind\": bind source path does not exist")
	h.ok("run")
	err := h.m.Mount(ctx, "web", []config.Mount{{Host: h.src("b"), Container: "/home/agent/workspace/b"}}, h.home, Options{Yes: true})
	wantErr(t, err, "the previous mounts are restored")
	h.golden("run-fails-rollback", err, "web")
}

func TestRollbackFails(t *testing.T) {
	h := newHarness(t)
	runningWeb(h, "")
	h.ps("web", "")
	h.ok("stop", "commit", "rm")
	h.image(ImageSnap("web"), "sha256:snap1", 31)
	h.fail("run", "docker: Error response from daemon: driver failed programming external connectivity: port is already allocated")
	err := h.m.Mount(ctx, "web", []config.Mount{{Host: h.src("b"), Container: "/home/agent/workspace/b"}}, h.home, Options{Yes: true})
	wantErr(t, err, "restoring the previous mounts also failed")
	h.golden("rollback-fails", err, "web")
	if st := h.load("web"); st.AppliedMounts != nil || st.Snapshot != "sha256:snap1" {
		t.Errorf("state: %+v", st)
	}
	if !strings.Contains(h.err.String(), `aide ns:web start`) {
		t.Errorf("no recovery hint: %q", h.err.String())
	}
}

func TestFlattenAboveThreshold(t *testing.T) {
	h := newHarness(t)
	h.m.FlattenThreshold = 40
	runningWeb(h, "sha256:snap0")
	h.ps("web", "")
	h.ok("stop", "commit", "rm", "run", "image rm", "export", "import")
	h.rec.On([]string{"image", "inspect", "-f", "{{json .}}", ImageSnap("web")}, imageJSON("sha256:snap1", 41), 0)
	h.imageID(ImageSnap("web"), "sha256:snap1")
	h.imageID(ImageSnap("web"), "sha256:flat1")
	err := h.m.Mount(ctx, "web", []config.Mount{{Host: h.src("b"), Container: "/home/agent/workspace/b"}}, h.home, Options{Yes: true})
	h.golden("flatten", err, "web")
	if !strings.Contains(h.out.String(), "flattened 41 layers") {
		t.Errorf("output: %s", h.out.String())
	}
}

func TestFlattenAtThresholdDoesNothing(t *testing.T) {
	h := newHarness(t)
	h.m.FlattenThreshold = 40
	runningWeb(h, "")
	h.ps("web", "")
	h.ok("stop", "commit", "rm", "run")
	h.rec.On([]string{"image", "inspect", "-f", "{{json .}}", ImageSnap("web")}, imageJSON("sha256:snap1", 40), 0)
	h.imageID(ImageSnap("web"), "sha256:snap1")
	if err := h.m.Mount(ctx, "web", []config.Mount{{Host: h.src("b"), Container: "/home/agent/workspace/b"}}, h.home, Options{Yes: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(h.rec.Transcript(), "\n"), "export") {
		t.Errorf("flattened at the threshold: %q", h.rec.Transcript())
	}
}

func TestFlattenFailureKeepsSnapshot(t *testing.T) {
	h := newHarness(t)
	h.m.FlattenThreshold = 40
	runningWeb(h, "")
	h.ps("web", "")
	h.ok("stop", "commit", "rm", "run", "export")
	h.fail("import", "Error: write /var/lib/docker: no space left on device")
	h.rec.On([]string{"image", "inspect", "-f", "{{json .}}", ImageSnap("web")}, imageJSON("sha256:snap1", 41), 0)
	h.imageID(ImageSnap("web"), "sha256:snap1")
	if err := h.m.Mount(ctx, "web", []config.Mount{{Host: h.src("b"), Container: "/home/agent/workspace/b"}}, h.home, Options{Yes: true}); err != nil {
		t.Fatal(err)
	}
	if st := h.load("web"); st.Snapshot != "sha256:snap1" || len(st.Mounts) != 2 {
		t.Errorf("state: %+v", st)
	}
	if !strings.Contains(h.err.String(), "flatten failed") {
		t.Errorf("no warning: %q", h.err.String())
	}
}

func TestUmountWhileRunning(t *testing.T) {
	h := newHarness(t)
	runningWeb(h, "")
	h.ps("web", "")
	h.ok("stop", "commit", "rm", "run")
	h.image(ImageSnap("web"), "sha256:snap1", 31)
	err := h.m.Umount(ctx, "web", []string{"/home/agent/workspace/a/"}, Options{Yes: true})
	h.golden("umount-running", err, "web")
	if st := h.load("web"); len(st.Mounts) != 0 || st.AppliedMounts == nil || len(st.AppliedMounts) != 0 {
		t.Errorf("state: %+v", st)
	}
}

func TestMountRecordedWithoutContainer(t *testing.T) {
	for _, p := range []Phase{NoImage, Built, Stopped} {
		t.Run(p.String(), func(t *testing.T) {
			h := newHarness(t)
			img, _ := fingerprint(t, "base")
			st := &config.State{Stacks: []string{"base"}, Image: img}
			if p == Stopped {
				st.AppliedMounts = []config.Mount{}
			}
			h.save("web", st)
			h.phase("web", p, "")
			if err := h.m.Mount(ctx, "web", []config.Mount{{Host: h.src("a"), Container: "/home/agent/workspace/a"}}, h.home, Options{}); err != nil {
				t.Fatal(err)
			}
			if got := h.load("web").Mounts; len(got) != 1 || got[0].Host != h.src("a") {
				t.Errorf("mounts: %+v", got)
			}
			if p == Stopped && !strings.Contains(h.out.String(), "applied on next start") {
				t.Errorf("output: %s", h.out.String())
			}
			// replacing the host of a mounted container path
			h.rec.Reset()
			if err := h.m.Mount(ctx, "web", []config.Mount{{Host: h.src("c"), Container: "/home/agent/workspace/a"}}, h.home, Options{}); err != nil {
				t.Fatal(err)
			}
			if got := h.load("web").Mounts; len(got) != 1 || got[0].Host != h.src("c") {
				t.Errorf("mounts after replace: %+v", got)
			}
			for _, l := range h.rec.Transcript() {
				if !strings.Contains(l, "inspect") {
					t.Errorf("recording a mount ran %q", l)
				}
			}
		})
	}
}

func TestMountValidation(t *testing.T) {
	h := newHarness(t)
	h.plat.unshared = h.src("c")
	h.save("web", &config.State{Stacks: []string{"base"}, Mounts: []config.Mount{h.mnt("a", "a")}})
	ws := "/home/agent/workspace/x"
	for _, tc := range []struct {
		name  string
		pairs []config.Mount
		want  string
	}{
		{"relative host", []config.Mount{{Host: "src/a", Container: ws}}, "host path must be absolute"},
		{"tilde user", []config.Mount{{Host: "~bob/src", Container: ws}}, "~user expansion is not supported"},
		{"missing host", []config.Mount{{Host: h.src("nope"), Container: ws}}, "does not exist"},
		{"host is a file", []config.Mount{{Host: h.src("file"), Container: ws}}, "is not a directory"},
		{"not shared", []config.Mount{{Host: h.src("c"), Container: ws}}, "not shared with Docker"},
		{"relative container", []config.Mount{{Host: h.src("b"), Container: "workspace/x"}}, "container path must be absolute"},
		{"root", []config.Mount{{Host: h.src("b"), Container: "/"}}, "cannot be /"},
		{"protected creds", []config.Mount{{Host: h.src("b"), Container: "/home/agent/.credentials"}}, "inside /home/agent/.credentials"},
		{"nested in cache", []config.Mount{{Host: h.src("b"), Container: "/var/cache/aide/../aide/npm"}}, "inside /var/cache/aide"},
		{"nested in mnt", []config.Mount{{Host: h.src("b"), Container: "/home/agent/mnt/terminalfs-sessions"}}, "inside /home/agent/mnt"},
		{"hides home", []config.Mount{{Host: h.src("b"), Container: "/home/agent"}}, "would hide /home/agent/.credentials"},
		{"system dir", []config.Mount{{Host: h.src("b"), Container: "/usr/local/src"}}, "inside /usr"},
		{"duplicate", []config.Mount{{Host: h.src("b"), Container: ws}, {Host: h.src("c"), Container: ws + "/"}}, "given twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h.rec.Reset()
			wantErr(t, h.m.Mount(ctx, "web", tc.pairs, h.home, Options{Yes: true}), tc.want)
			if got := h.rec.Transcript(); len(got) != 0 {
				t.Errorf("docker touched: %q", got)
			}
		})
	}
	h.phase("web", Built, "")
	wantErr(t, h.m.Umount(ctx, "web", []string{"/home/agent/workspace/zzz"}, Options{}), "nothing is mounted at /home/agent/workspace/zzz")
	if got := h.load("web").Mounts; len(got) != 1 {
		t.Errorf("mounts changed: %+v", got)
	}
}

func TestReset(t *testing.T) {
	for _, tc := range []struct {
		name  string
		phase Phase
		snap  string
		ok    []string
	}{
		{"no-image", NoImage, "", nil},
		{"built", Built, "", nil},
		{"built-snapshot", Built, "sha256:snap1", []string{"image rm"}},
		{"stopped", Stopped, "sha256:snap1", []string{"rm", "image rm"}},
		{"running", Running, "", []string{"stop", "rm", "image rm"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			img, _ := fingerprint(t, "base")
			st := &config.State{Stacks: []string{"base"}, Image: img, Snapshot: tc.snap, Mounts: []config.Mount{h.mnt("a", "a")}}
			if tc.phase >= Stopped {
				st.AppliedMounts = st.Mounts
			}
			h.save("web", st)
			h.phase("web", tc.phase, tc.snap)
			if tc.snap != "" {
				h.rec.OnError([]string{"image", "rm", ImageSnap("web")}, "Error response from daemon: No such image: aide-web:snap", 1)
			}
			h.ok(tc.ok...)
			err := h.m.Reset(ctx, "web", Options{Yes: true})
			h.golden("reset-"+tc.name, err, "web")
		})
	}
	t.Run("declined", func(t *testing.T) {
		h := newHarness(t)
		h.save("web", &config.State{Stacks: []string{"base"}, AppliedMounts: []config.Mount{}})
		h.phase("web", Running, "")
		wantErr(t, h.m.Reset(ctx, "web", Options{}), "cancelled")
		if len(h.prompts) != 1 || !strings.Contains(h.prompts[0], "discards everything inside the container") {
			t.Errorf("prompts: %q", h.prompts)
		}
		h.m.Confirm = nil
		wantErr(t, h.m.Reset(ctx, "web", Options{}), "cancelled")
	})
}

func TestRefusedVerbs(t *testing.T) {
	for _, tc := range []struct {
		verb  string
		phase Phase
		want  string
	}{
		{"stack", Stopped, `cannot add stacks while a container exists (stopped); run "aide ns:web reset" first`},
		{"stack", Running, `cannot add stacks while a container exists (running); run "aide ns:web reset" first`},
		{"build", Stopped, `cannot build while a container exists (stopped); run "aide ns:web reset" first`},
		{"build", Running, `cannot build while a container exists (running); run "aide ns:web reset" first`},
		{"start", NoImage, `cannot start while the namespace has no image; run "aide ns:web build" first`},
	} {
		t.Run(tc.verb+"-"+tc.phase.String(), func(t *testing.T) {
			h := newHarness(t)
			st := &config.State{Stacks: []string{"base"}}
			if tc.phase >= Stopped {
				st.AppliedMounts = []config.Mount{}
			}
			h.save("web", st)
			h.phase("web", tc.phase, "")
			var err error
			switch tc.verb {
			case "stack":
				err = h.m.AddStacks(ctx, "web", []string{"claude"})
			case "build":
				err = h.m.Build(ctx, "web", false)
			case "start":
				err = h.m.Start(ctx, "web", Options{})
			}
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v\nwant    %s", err, tc.want)
			}
			for _, l := range h.rec.Transcript() {
				if !strings.Contains(l, " inspect ") {
					t.Errorf("refused verb ran %q", l)
				}
			}
			if got := h.load("web").Stacks; !slices.Equal(got, []string{"base"}) {
				t.Errorf("stacks changed: %v", got)
			}
		})
	}
	t.Run("build-with-snapshot", func(t *testing.T) {
		h := newHarness(t)
		h.save("web", &config.State{Stacks: []string{"base"}, Snapshot: "sha256:snap1"})
		h.phase("web", Built, "sha256:snap1")
		wantErr(t, h.m.Build(ctx, "web", false), `run "aide ns:web reset" first`)
	})
}

func TestNoops(t *testing.T) {
	for _, p := range []Phase{NoImage, Built, Stopped} {
		t.Run("stop-"+p.String(), func(t *testing.T) {
			h := newHarness(t)
			h.save("web", &config.State{Stacks: []string{"base"}})
			h.phase("web", p, "")
			if err := h.m.Stop(ctx, "web"); err != nil {
				t.Fatal(err)
			}
			for _, l := range h.rec.Transcript() {
				if !strings.Contains(l, " inspect ") {
					t.Errorf("no-op stop ran %q", l)
				}
			}
		})
	}
	h := newHarness(t)
	h.save("web", &config.State{Stacks: []string{"base"}, AppliedMounts: []config.Mount{}})
	h.phase("web", Running, "")
	h.ok("stop")
	if err := h.m.Stop(ctx, "web"); err != nil {
		t.Fatal(err)
	}
	if got := h.rec.Transcript(); got[len(got)-1] != "docker stop -t 30 aide-web" {
		t.Errorf("stop: %q", got)
	}
}

func TestAddStacks(t *testing.T) {
	h := newHarness(t)
	img, _ := fingerprint(t, "base")
	h.save("web", &config.State{Stacks: []string{"base"}, Image: img})
	h.phase("web", Built, "")
	if err := h.m.AddStacks(ctx, "web", []string{"claude", "base", "tfs"}); err != nil {
		t.Fatal(err)
	}
	if got := h.load("web").Stacks; !slices.Equal(got, []string{"base", "claude", "tfs"}) {
		t.Errorf("stacks: %v", got)
	}
	if !strings.Contains(h.out.String(), "image is stale") {
		t.Errorf("output: %s", h.out.String())
	}
	wantErr(t, h.m.AddStacks(ctx, "web", []string{"oc", "ocfs"}), "conflict")
	wantErr(t, h.m.AddStacks(ctx, "web", []string{"nope"}), "unknown stack")
	if got := h.load("web").Stacks; len(got) != 3 {
		t.Errorf("stacks changed by a failed add: %v", got)
	}
}

func TestBuildNoCache(t *testing.T) {
	h := newHarness(t)
	h.save("web", &config.State{Stacks: []string{"tfs"}})
	h.phase("web", NoImage, "")
	h.ok("build")
	h.imageID(ImageBase("web"), "sha256:base2")
	err := h.m.Build(ctx, "web", true)
	h.golden("build-no-cache", err, "web")
}

func TestObserveRepairsDrift(t *testing.T) {
	h := newHarness(t)
	h.save("web", &config.State{Stacks: []string{"base"}, Snapshot: "sha256:gone", AppliedMounts: []config.Mount{}})
	h.image(ImageBase("web"), "sha256:base1", 30)
	h.noImage(ImageSnap("web"))
	h.noContainer(ContainerName("web"))
	st := h.load("web")
	p, err := h.m.Observe(ctx, "web", st)
	if err != nil || p != Built {
		t.Fatalf("phase %v, %v", p, err)
	}
	if st := h.load("web"); st.Snapshot != "" || st.AppliedMounts != nil {
		t.Errorf("drift not repaired: %+v", st)
	}
}

func TestClone(t *testing.T) {
	setup := func(t *testing.T, p Phase, snap string) *harness {
		h := newHarness(t)
		img, _ := fingerprint(t, "tfs")
		st := &config.State{Stacks: []string{"tfs"}, Image: img, Snapshot: snap, Mounts: []config.Mount{h.mnt("a", "a")}}
		if p >= Stopped {
			st.AppliedMounts = st.Mounts
		}
		h.save("web", st)
		h.phase("web", p, snap)
		return h
	}
	t.Run("running-refused", func(t *testing.T) {
		h := setup(t, Running, "")
		err := h.m.Clone(ctx, "web", "api", Options{})
		wantErr(t, err, "stop it first, or pass --yes to commit it paused")
		h.golden("clone-running-refused", err, "web", "api")
	})
	t.Run("running-yes", func(t *testing.T) {
		h := setup(t, Running, "sha256:snap0")
		h.ok("commit", "tag", "volume create", "run --rm")
		h.imageID(ImageSnap("api"), "sha256:snap9")
		h.rec.On([]string{"volume", "inspect", CredsVolume("web")}, "[{}]", 0)
		err := h.m.Clone(ctx, "web", "api", Options{Yes: true})
		h.golden("clone-running-yes", err, "web", "api")
	})
	t.Run("no-container", func(t *testing.T) {
		h := setup(t, Built, "sha256:snap0")
		h.ok("tag", "volume create", "run --rm")
		h.rec.On([]string{"volume", "inspect", CredsVolume("web")}, "[{}]", 0)
		err := h.m.Clone(ctx, "web", "api", Options{})
		h.golden("clone-no-container", err, "web", "api")
	})
	t.Run("no-image-no-volume", func(t *testing.T) {
		h := setup(t, NoImage, "")
		h.rec.OnError([]string{"volume", "inspect", CredsVolume("web")}, "Error response from daemon: get aide-web-creds: no such volume", 1)
		err := h.m.Clone(ctx, "web", "api", Options{})
		h.golden("clone-no-image", err, "web", "api")
	})
	t.Run("commit-fails-cleans-up", func(t *testing.T) {
		h := setup(t, Stopped, "")
		h.fail("commit", "Error response from daemon: no space left on device")
		wantErr(t, h.m.Clone(ctx, "web", "api", Options{}), "commit the container")
		if ok, _ := h.m.Store.Exists("api"); ok {
			t.Error("api state left behind")
		}
	})
	t.Run("target-exists", func(t *testing.T) {
		h := setup(t, Built, "")
		h.save("api", &config.State{Stacks: []string{"base"}})
		wantErr(t, h.m.Clone(ctx, "web", "api", Options{}), "namespace api already exists")
	})
}

func TestRemove(t *testing.T) {
	for _, keep := range []bool{false, true} {
		name := "remove"
		if keep {
			name = "remove-keep-volumes"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.save("web", &config.State{Stacks: []string{"base"}})
			h.ok("rm", "volume rm")
			h.rec.OnError([]string{"image", "rm", ImageSnap("web")}, "Error response from daemon: No such image: aide-web:snap", 1)
			h.ok("image rm " + ImageBase("web"))
			err := h.m.Remove(ctx, "web", keep, Options{Yes: true})
			h.golden(name, err, "web")
		})
	}
	t.Run("declined", func(t *testing.T) {
		h := newHarness(t)
		h.save("web", &config.State{Stacks: []string{"base"}})
		wantErr(t, h.m.Remove(ctx, "web", false, Options{}), "cancelled")
		if ok, _ := h.m.Store.Exists("web"); !ok {
			t.Error("removed after decline")
		}
	})
	t.Run("docker-error-keeps-state", func(t *testing.T) {
		h := newHarness(t)
		h.save("web", &config.State{Stacks: []string{"base"}})
		h.ok("rm", "volume rm", "image rm")
		h.fail("image rm "+ImageBase("web"), "Error response from daemon: conflict: unable to remove repository reference \"aide-web:base\" (must force) - container 123 is using its referenced image")
		wantErr(t, h.m.Remove(ctx, "web", false, Options{Yes: true}), "its state is kept")
		if ok, _ := h.m.Store.Exists("web"); !ok {
			t.Error("state removed despite an error")
		}
	})
	t.Run("missing", func(t *testing.T) {
		h := newHarness(t)
		err := h.m.Remove(ctx, "nope", false, Options{Yes: true})
		if !errors.Is(err, config.ErrNoNamespace) {
			t.Errorf("error = %v", err)
		}
	})
}

func TestListStateDockerfile(t *testing.T) {
	h := newHarness(t)
	h.save("api", &config.State{Stacks: []string{"base"}, Port: 8081})
	h.save("web", &config.State{Stacks: []string{"tfs", "claude"}, Mounts: []config.Mount{{Host: "/Users/p/my src", Container: "/home/agent/workspace/x"}}, AppliedMounts: []config.Mount{}})
	h.phase("api", NoImage, "")
	h.phase("web", Running, "")
	got, err := h.m.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "api" || got[0].Phase != NoImage || got[0].Port != 8081 ||
		got[1].Name != "web" || got[1].Phase != Running || got[1].Mounts != 1 || !slices.Equal(got[1].Stacks, []string{"tfs", "claude"}) {
		t.Errorf("list: %+v", got)
	}
	s, err := h.m.State(ctx, "web")
	if err != nil {
		t.Fatal(err)
	}
	want := "aide ns:web stack tfs claude\naide ns:web mount '/Users/p/my src' /home/agent/workspace/x\n"
	if s != want {
		t.Errorf("state:\n%s\nwant:\n%s", s, want)
	}
	df, err := h.m.Dockerfile(ctx, "web")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(df), "# Generated by aide. Stacks: base tfs claude") {
		t.Errorf("dockerfile:\n%s", df)
	}
}

func TestBusyProcesses(t *testing.T) {
	got := busyProcesses(docker.ParseProcesses(psOutput))
	var args []string
	for _, p := range got {
		args = append(args, p.Args)
	}
	want := []string{
		"claude",
		"terminalfs session serve --root /home/agent/mnt/terminalfs-sessions/terminalfs --session 5f1c2a",
		"/home/agent/.opencode/bin/opencode",
		"bash -c npm test",
	}
	if !slices.Equal(args, want) {
		t.Errorf("busy:\n got %q\nwant %q", args, want)
	}
	if busyProcesses(nil) != nil {
		t.Error("nil input")
	}
}

func TestRunSpec(t *testing.T) {
	h := newHarness(t)
	_, res := fingerprint(t, "tfs")
	st := &config.State{Port: 8085, Password: "secret"}
	spec := h.m.runSpec("web", st, res, ImageSnap("web"), []config.Mount{{Host: "/a,b", Container: "/home/agent/workspace/x"}})
	got := docker.CommandLine(docker.RunArgs(spec))
	want := "docker run -d --name aide-web --hostname web --init --stop-timeout 30 --label aide.namespace=web --publish 127.0.0.1:8085:8080" +
		" --mount type=volume,src=aide-web-creds,dst=/home/agent/.credentials --mount type=volume,src=aide-web-cache,dst=/var/cache/aide" +
		` --mount 'type=bind,"src=/a,b",dst=/home/agent/workspace/x' --cap-add SYS_ADMIN --security-opt apparmor=unconfined` +
		" --env CODE_SERVER_PASSWORD=secret --env TERMINALFS_CLAUDE_STRICT=1 aide-web:snap"
	if got != want {
		t.Errorf("run:\n got %s\nwant %s", got, want)
	}
	h.plat.noAppArmor = true
	if spec := h.m.runSpec("web", st, res, "x", nil); len(spec.SecurityOpt) != 0 {
		t.Errorf("apparmor not filtered: %v", spec.SecurityOpt)
	}
}
