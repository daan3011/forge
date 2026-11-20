package sessions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/sessions/claude"
	"github.com/daan3011/forge/internal/sessions/hooks"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/sessions/tmux/tmuxtest"
	"github.com/daan3011/forge/internal/sessions/worktree"
	"github.com/daan3011/forge/internal/store"
)

const testID = "0b5f3c3e-8a1d-4c2b-9f00-1234567890ab"

var t0 = time.Date(2026, 11, 20, 9, 0, 0, 0, time.Local)

// fakeWT is an in-memory worktree.Manager.
type fakeWT struct {
	root       string
	trees      map[string]bool   // session -> exists
	branches   map[string]bool   // branch -> exists locally
	remote     map[string]bool   // branch -> exists only on origin
	checkedOut map[string]string // branch -> path of a worktree outside forge
	dirty      map[string]bool
	createErr  error
	created    []worktree.CreateOpts
	removed    []worktree.RemoveOpts
	deleted    []string // branches
	excluded   []string // dir/rel passed to EnsureExcluded
	excludeErr error
}

func newFakeWT(root string) *fakeWT {
	return &fakeWT{
		root: root, trees: map[string]bool{}, branches: map[string]bool{}, remote: map[string]bool{},
		checkedOut: map[string]string{}, dirty: map[string]bool{},
	}
}

func (f *fakeWT) Create(_ context.Context, o worktree.CreateOpts) (worktree.Created, error) {
	if f.createErr != nil {
		return worktree.Created{}, f.createErr
	}
	branch, custom := worktree.Branch(o.Session), o.Branch != ""
	if custom {
		branch = o.Branch
	}
	if p, ok := f.checkedOut[branch]; ok {
		return worktree.Created{}, &worktree.BranchCheckedOutError{Branch: branch, Path: p}
	}
	if f.branches[branch] && !o.ReuseBranch && !custom {
		return worktree.Created{}, &worktree.BranchExistsError{Branch: branch}
	}
	res := worktree.Created{Branch: branch, Reused: f.branches[branch]}
	switch {
	case res.Reused:
	case custom && f.remote[branch]:
		res.Tracking = "origin/" + branch
	default:
		res.BranchCreated = true
	}
	f.trees[o.Session], f.branches[branch] = true, true
	f.created = append(f.created, o)
	res.Path = worktree.Path(f.root, o.Project, o.Session)
	// A real directory, so code that checks the worktree exists sees it.
	if err := os.MkdirAll(res.Path, 0o755); err != nil {
		return worktree.Created{}, err
	}
	return res, nil
}

func (f *fakeWT) List(context.Context, config.Project) ([]worktree.Worktree, error) { return nil, nil }

func (f *fakeWT) Remove(_ context.Context, o worktree.RemoveOpts) (worktree.Removed, error) {
	f.removed = append(f.removed, o)
	if !f.trees[o.Session] {
		return worktree.Removed{}, worktree.ErrNotFound
	}
	if f.dirty[o.Session] && !o.Force {
		return worktree.Removed{}, &worktree.DirtyError{Path: o.Session, Files: []string{" M a.go"}}
	}
	delete(f.trees, o.Session)
	_ = os.RemoveAll(worktree.Path(f.root, o.Project, o.Session))
	return worktree.Removed{Branch: worktree.Branch(o.Session), BranchMerged: true}, nil
}

func (f *fakeWT) EnsureExcluded(_ context.Context, dir, rel string) error {
	f.excluded = append(f.excluded, filepath.Join(dir, rel))
	return f.excludeErr
}

func (f *fakeWT) DeleteBranch(_ context.Context, _ config.Project, branch string, _ bool) error {
	f.deleted = append(f.deleted, branch)
	delete(f.branches, branch)
	return nil
}

// failingStore makes Create fail after the real store checks the name.
type failingStore struct{ Store }

func (failingStore) Create(context.Context, store.Session) (store.Session, error) {
	return store.Session{}, errors.New("disk full")
}

type env struct {
	svc  *Service
	tmux *tmuxtest.Fake
	wt   *fakeWT
	st   *store.Store
	data string
	proj config.Project
	// claude is the fake ~/.claude/projects.
	claude string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "forge.db"), store.WithClock(func() time.Time { return t0 }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e := &env{
		tmux:   tmuxtest.NewFake(),
		wt:     newFakeWT(filepath.Join(dir, "wt")),
		st:     st,
		data:   filepath.Join(dir, "data"),
		claude: filepath.Join(dir, "claude-projects"),
		proj:   config.Project{Name: "forge", Repo: "daan/forge", Path: filepath.Join(dir, "repo")},
	}
	e.svc = New(Config{
		Tmux: e.tmux, Worktrees: e.wt, Store: StoreFrom(st),
		Projects: []config.Project{e.proj, {Name: "nopath", Repo: "daan/x"}},
		DataDir:  e.data, ClaudeBin: "claude",
		NewID: func() (string, error) { return testID, nil },
		Now:   func() time.Time { return t0.Add(time.Hour) },

		ProjectsDir: e.claude, StartupCheck: time.Nanosecond,
	})
	return e
}

func (e *env) start(t *testing.T, name string) store.Session {
	t.Helper()
	res, err := e.svc.Start(context.Background(), StartOpts{Name: name, Project: "forge", Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	return res.Session
}

func TestStartHappyPath(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prompt := "fix \"ci\"\n$HOME `x`"
	res, err := e.svc.Start(ctx, StartOpts{Name: "fix-ci", Project: "forge", Template: "bugfix", Prompt: prompt})
	if err != nil {
		t.Fatal(err)
	}
	x := res.Session
	wantPath := worktree.Path(e.wt.root, e.proj, "fix-ci")
	if x.Name != "fix-ci" || x.ClaudeSessionID != testID || x.Project != "forge" || x.Repo != "daan/forge" ||
		x.BaseBranch != "main" || x.Branch != "agent/fix-ci" || x.Worktree != wantPath ||
		x.TmuxSession != "forge" || x.TmuxWindow != "fix-ci" || x.Template != "bugfix" ||
		x.Status != store.StatusStarting || x.ID == 0 || x.CreatedAt.IsZero() {
		t.Errorf("session = %+v", x)
	}

	got, err := e.st.Sessions().GetByName(ctx, "fix-ci")
	if err != nil || got.ID != x.ID {
		t.Fatalf("stored = %+v, %v", got, err)
	}

	dir, argv, envv, ok := e.tmux.Started(tmux.Target{Session: "forge", Window: "fix-ci"})
	if !ok {
		t.Fatal("no window started")
	}
	if dir != wantPath {
		t.Errorf("window dir = %q, want %q", dir, wantPath)
	}
	if envv[EnvSessionName] != "fix-ci" {
		t.Errorf("env = %v", envv)
	}
	file := filepath.Join(e.data, "sessions", "fix-ci", "prompt.md")
	want, _ := claude.Launch{Bin: "claude", SessionID: testID, PromptFile: file}.Argv()
	if !slices.Equal(argv, want) {
		t.Errorf("argv = %q, want %q", argv, want)
	}
	if strings.Contains(strings.Join(argv, " "), "fix \"ci\"") {
		t.Error("prompt leaked into argv")
	}
	b, err := os.ReadFile(file)
	if err != nil || string(b) != prompt {
		t.Errorf("prompt file = %q, %v", b, err)
	}
	if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o600 {
		t.Errorf("prompt file mode = %v", fi.Mode())
	}
	if sent := e.tmux.Sent(tmux.Target{Session: "forge", Window: "fix-ci"}); len(sent) != 0 {
		t.Errorf("keys sent: %q", sent)
	}
}

func TestStartWithoutPromptWritesNoFile(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.Start(context.Background(), StartOpts{Name: "bare", Project: "forge", Base: "develop"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.data, "sessions", "bare")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("session dir exists: %v", err)
	}
	_, argv, _, _ := e.tmux.Started(tmux.Target{Session: "forge", Window: "bare"})
	if strings.Contains(argv[2], "cat") {
		t.Errorf("argv = %q", argv)
	}
}

func TestStartValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.start(t, "taken")
	tests := []struct {
		name string
		o    StartOpts
		want error
	}{
		{"bad name", StartOpts{Name: "no spaces", Project: "forge"}, worktree.ErrInvalidName},
		{"taken", StartOpts{Name: "taken", Project: "forge"}, ErrNameTaken},
		{"unknown project", StartOpts{Name: "a", Project: "nope"}, ErrUnknownProject},
		{"no path", StartOpts{Name: "a", Project: "nopath"}, ErrNoProjectPath},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := e.svc.Start(ctx, tt.o); !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
	// The taken name was checked before any worktree work.
	if len(e.wt.removed) != 0 {
		t.Errorf("removed = %+v", e.wt.removed)
	}
}

func TestStartBranchExists(t *testing.T) {
	e := newEnv(t)
	e.wt.branches["agent/old"] = true
	_, err := e.svc.Start(context.Background(), StartOpts{Name: "old", Project: "forge"})
	if !errors.Is(err, worktree.ErrBranchExists) {
		t.Fatalf("err = %v", err)
	}
	if len(e.wt.deleted) != 0 {
		t.Errorf("deleted pre-existing branch: %v", e.wt.deleted)
	}
}

func TestStartRollsBackWhenWindowFails(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	// A window with the same name already exists in the project session.
	tsess, _ := e.tmux.EnsureSession(ctx, "forge")
	if _, err := e.tmux.NewWindow(ctx, tsess, "clash", "", []string{"sh"}, nil); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Start(ctx, StartOpts{Name: "clash", Project: "forge", Prompt: "p"})
	if !errors.Is(err, tmux.ErrWindowExists) {
		t.Fatalf("err = %v", err)
	}
	if e.wt.trees["clash"] || e.wt.branches["agent/clash"] {
		t.Error("worktree or branch left behind")
	}
	if _, err := os.Stat(e.svc.SessionDir("clash")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("session dir left behind: %v", err)
	}
	if _, err := e.st.Sessions().GetByName(ctx, "clash"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("row saved: %v", err)
	}
}

func TestStartRollsBackWhenStoreFails(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.c.Store = failingStore{StoreFrom(e.st)}
	_, err := e.svc.Start(ctx, StartOpts{Name: "doomed", Project: "forge", Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v", err)
	}
	if alive, _ := e.tmux.WindowAlive(ctx, tmux.Target{Session: "forge", Window: "doomed"}); alive {
		t.Error("window left running")
	}
	if e.wt.trees["doomed"] || e.wt.branches["agent/doomed"] {
		t.Error("worktree or branch left behind")
	}
}

func TestListSweepsDeadWindows(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.start(t, "alive")
	e.svc.c.NewID = func() (string, error) { return "1b5f3c3e-8a1d-4c2b-9f00-1234567890ab", nil }
	e.start(t, "exited")
	e.svc.c.NewID = func() (string, error) { return "2b5f3c3e-8a1d-4c2b-9f00-1234567890ab", nil }
	gone := e.start(t, "gone")
	e.tmux.Exit(tmux.Target{Session: "forge", Window: "exited"}, 1)
	if err := e.tmux.KillWindow(ctx, tmux.Target{Session: "forge", Window: "gone"}); err != nil {
		t.Fatal(err)
	}
	if err := e.st.Links().Add(ctx, store.SessionRef(gone.ID), store.PRRef("daan/forge", 12), store.LinkMeta{}); err != nil {
		t.Fatal(err)
	}

	rows, err := e.svc.List(ctx, "forge", false)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Row{}
	for _, r := range rows {
		got[r.Name] = r
	}
	if len(rows) != 3 || got["alive"].Status != store.StatusStarting ||
		got["exited"].Status != store.StatusDead || got["gone"].Status != store.StatusDead {
		t.Errorf("rows = %+v", rows)
	}
	if got["gone"].Link != "#12" || got["alive"].Link != "" {
		t.Errorf("links = %q %q", got["gone"].Link, got["alive"].Link)
	}

	other, err := e.svc.List(ctx, "other", false)
	if err != nil || len(other) != 0 {
		t.Errorf("other project = %+v, %v", other, err)
	}
}

func TestPreview(t *testing.T) {
	e := newEnv(t)
	x := e.start(t, "p")
	e.tmux.SetOutput(tmux.Target{Session: "forge", Window: "p"}, "a\nb\nc\n\n")
	got, err := e.svc.Preview(context.Background(), x, 2)
	if err != nil || got != "b\nc" {
		t.Errorf("Preview = %q, %v", got, err)
	}
	x.TmuxWindow = ""
	if _, err := e.svc.Preview(context.Background(), x, 2); !errors.Is(err, ErrNoWindow) {
		t.Errorf("err = %v", err)
	}
}

func TestKillKeepsRowAsDead(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	x := e.start(t, "k")
	res, err := e.svc.Kill(ctx, x, KillOpts{})
	if err != nil || res.Archived {
		t.Fatalf("Kill = %+v, %v", res, err)
	}
	if alive, _ := e.tmux.WindowAlive(ctx, tmux.Target{Session: "forge", Window: "k"}); alive {
		t.Error("window still alive")
	}
	got, _ := e.st.Sessions().Get(ctx, x.ID)
	if got.Status != store.StatusDead || got.Worktree == "" || got.Archived() {
		t.Errorf("after kill = %+v", got)
	}
	if !e.wt.trees["k"] {
		t.Error("worktree removed")
	}
}

func TestKillRemoveWorktreeArchives(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	x := e.start(t, "k")
	if err := e.st.Links().Add(ctx, store.SessionRef(x.ID), store.IssueRef("PLANE-7"), store.LinkMeta{}); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.Kill(ctx, x, KillOpts{RemoveWorktree: true})
	if err != nil || !res.Archived || !res.BranchMerged {
		t.Fatalf("Kill = %+v, %v", res, err)
	}
	if e.wt.trees["k"] || !e.wt.branches["agent/k"] {
		t.Errorf("trees=%v branches=%v, want worktree gone and branch kept", e.wt.trees, e.wt.branches)
	}
	if o := e.wt.removed[len(e.wt.removed)-1]; o.Base != "main" || o.Force {
		t.Errorf("remove opts = %+v", o)
	}
	got, _ := e.st.Sessions().Get(ctx, x.ID)
	if got.Status != store.StatusArchived || got.Worktree != "" || got.ClaudeSessionID != testID || got.Branch != "agent/k" {
		t.Errorf("after kill = %+v", got)
	}
	links, _ := e.st.Links().For(ctx, store.SessionRef(x.ID))
	if len(links) != 1 {
		t.Errorf("links = %+v", links)
	}

	active, _ := e.svc.List(ctx, "", false)
	archived, _ := e.svc.List(ctx, "", true)
	if len(active) != 0 || len(archived) != 1 || archived[0].Link != "PLANE-7" {
		t.Errorf("active=%+v archived=%+v", active, archived)
	}
	if _, err := e.svc.Kill(ctx, got, KillOpts{}); !errors.Is(err, ErrArchived) {
		t.Errorf("kill archived err = %v", err)
	}
}

func TestKillDirtyWorktree(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	x := e.start(t, "dirty")
	e.wt.dirty["dirty"] = true
	_, err := e.svc.Kill(ctx, x, KillOpts{RemoveWorktree: true})
	var de *worktree.DirtyError
	if !errors.As(err, &de) {
		t.Fatalf("err = %v, want DirtyError", err)
	}
	got, _ := e.st.Sessions().Get(ctx, x.ID)
	if got.Status != store.StatusDead || !e.wt.trees["dirty"] {
		t.Errorf("after dirty kill: status=%s tree=%v", got.Status, e.wt.trees["dirty"])
	}
	// Retrying with Force on the now-dead session archives it.
	res, err := e.svc.Kill(ctx, got, KillOpts{RemoveWorktree: true, Force: true})
	if err != nil || !res.Archived || e.wt.trees["dirty"] {
		t.Errorf("forced kill = %+v, %v", res, err)
	}
}

func TestKillRemoveWorktreeAlreadyGone(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	x := e.start(t, "g")
	delete(e.wt.trees, "g")
	if res, err := e.svc.Kill(ctx, x, KillOpts{RemoveWorktree: true}); err != nil || !res.Archived {
		t.Errorf("Kill = %+v, %v", res, err)
	}
}

func TestDelete(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	x := e.start(t, "d")
	if err := e.svc.Delete(ctx, x); !errors.Is(err, ErrNotArchived) {
		t.Fatalf("delete active err = %v", err)
	}
	if _, err := e.svc.Kill(ctx, x, KillOpts{RemoveWorktree: true}); err != nil {
		t.Fatal(err)
	}
	x, _ = e.st.Sessions().Get(ctx, x.ID)
	if err := e.svc.Delete(ctx, x); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Sessions().Get(ctx, x.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("row still there: %v", err)
	}
	if _, err := os.Stat(e.svc.SessionDir("d")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("session dir left: %v", err)
	}
	if !e.wt.branches["agent/d"] {
		t.Error("branch deleted")
	}
}

func TestTmuxControl(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	x := e.start(t, "alpha")

	ws, err := e.svc.TmuxWindows(ctx)
	if err != nil || len(ws) != 1 || ws[0].Window != x.TmuxWindow {
		t.Fatalf("TmuxWindows = %v, %v", ws, err)
	}
	if err := e.svc.RestartTmux(ctx); err != nil {
		t.Fatal(err)
	}
	if ws, _ := e.svc.TmuxWindows(ctx); len(ws) != 0 {
		t.Errorf("after restart = %v", ws)
	}
	rows, err := e.svc.List(ctx, "", false)
	if err != nil || len(rows) != 1 || rows[0].Status != store.StatusDead {
		t.Errorf("rows after restart = %+v, %v", rows, err)
	}

	if _, err := e.svc.ResetTmuxConfig(); err == nil {
		t.Error("reset without a path: want error")
	}
	conf := filepath.Join(t.TempDir(), "tmux.conf")
	if err := os.WriteFile(conf, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.svc.c.TmuxConf = conf
	if e.svc.TmuxConfig() != conf {
		t.Errorf("TmuxConfig = %q", e.svc.TmuxConfig())
	}
	backup, err := e.svc.ResetTmuxConfig()
	if err != nil || backup != conf+".bak."+t0.Add(time.Hour).Format("20060102-150405") {
		t.Errorf("reset = %q, %v", backup, err)
	}
}

func TestStartInstallsHooks(t *testing.T) {
	e := newEnv(t)
	e.svc.c.ForgeBin = "/opt/bin/forge"
	x := e.start(t, "hooked")
	b, err := os.ReadFile(filepath.Join(x.Worktree, hooks.SettingsRel))
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range hooks.Events {
		if !strings.Contains(string(b), hooks.Command("/opt/bin/forge", ev)) {
			t.Errorf("no %s hook in\n%s", ev, b)
		}
	}
	if want := filepath.Join(x.Worktree, hooks.SettingsRel); !slices.Equal(e.wt.excluded, []string{want}) {
		t.Errorf("excluded %q, want %q", e.wt.excluded, want)
	}
}

func TestStartWithoutForgeBinInstallsNoHooks(t *testing.T) {
	e := newEnv(t)
	x := e.start(t, "plain")
	if _, err := os.Stat(filepath.Join(x.Worktree, hooks.SettingsRel)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("settings written: %v", err)
	}
	if len(e.wt.excluded) != 0 {
		t.Errorf("excluded %q", e.wt.excluded)
	}
}

func TestStartRollsBackWhenExcludeFails(t *testing.T) {
	e := newEnv(t)
	e.svc.c.ForgeBin = "/opt/bin/forge"
	e.wt.excludeErr = errors.New("read-only .git")
	if _, err := e.svc.Start(context.Background(), StartOpts{Name: "ro", Project: "forge"}); err == nil {
		t.Fatal("want an error")
	}
	if e.wt.trees["ro"] || e.wt.branches["agent/ro"] {
		t.Error("worktree or branch left behind")
	}
	if _, _, _, ok := e.tmux.Started(tmux.Target{Session: "forge", Window: "ro"}); ok {
		t.Error("window started")
	}
}

func TestClearWaiting(t *testing.T) {
	ctx := context.Background()
	notif := func(typ string) store.Event {
		return store.Event{
			ClaudeSessionID: testID, Event: hooks.Notification,
			Payload: `{"hook_event_name":"Notification","notification_type":"` + typ + `"}`,
		}
	}
	tests := []struct {
		name   string
		status store.Status
		event  *store.Event
		want   bool
		after  store.Status
	}{
		{"permission prompt answered", store.StatusWaiting, ptr(notif("permission_prompt")), true, store.StatusWorking},
		{"elicitation stays waiting", store.StatusWaiting, ptr(notif("elicitation_dialog")), false, store.StatusWaiting},
		{"no events", store.StatusWaiting, nil, false, store.StatusWaiting},
		{"later event", store.StatusWaiting, &store.Event{ClaudeSessionID: testID, Event: hooks.PreToolUse}, false, store.StatusWaiting},
		{"not waiting", store.StatusIdle, ptr(notif("permission_prompt")), false, store.StatusIdle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			x := e.start(t, "w")
			if err := e.st.Sessions().SetStatus(ctx, x.ID, tt.status, t0); err != nil {
				t.Fatal(err)
			}
			if tt.event != nil {
				if _, err := e.st.Events().Append(ctx, *tt.event); err != nil {
					t.Fatal(err)
				}
			}
			x.Status = tt.status
			ok, err := e.svc.ClearWaiting(ctx, x)
			if err != nil || ok != tt.want {
				t.Fatalf("ClearWaiting = %v, %v; want %v", ok, err, tt.want)
			}
			got, _ := e.st.Sessions().Get(ctx, x.ID)
			if got.Status != tt.after {
				t.Errorf("status %s, want %s", got.Status, tt.after)
			}
		})
	}
}

func TestClearWaitingStaleView(t *testing.T) {
	// The tab saw "waiting", but a hook already moved the session on.
	ctx := context.Background()
	e := newEnv(t)
	x := e.start(t, "w")
	_, _ = e.st.Events().Append(ctx, store.Event{
		ClaudeSessionID: testID, Event: hooks.Notification,
		Payload: `{"notification_type":"permission_prompt"}`,
	})
	_ = e.st.Sessions().SetStatus(ctx, x.ID, store.StatusIdle, t0)
	x.Status = store.StatusWaiting
	if ok, err := e.svc.ClearWaiting(ctx, x); ok || err != nil {
		t.Fatalf("ClearWaiting = %v, %v", ok, err)
	}
	if got, _ := e.st.Sessions().Get(ctx, x.ID); got.Status != store.StatusIdle {
		t.Errorf("status %s, want idle", got.Status)
	}
}

func ptr[T any](v T) *T { return &v }
