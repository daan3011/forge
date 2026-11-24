package wizard

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/clipboard"
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/doctor"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/sessions/tmux/tmuxtest"
	"github.com/daan3011/forge/internal/setup"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/testutil"
	"github.com/daan3011/forge/internal/ui/anim"
)

// --- fakes -----------------------------------------------------------------

type memStore struct {
	mu        sync.Mutex
	kv        map[string]string
	completed int
}

func (m *memStore) Get(_ context.Context, k string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.kv[k]
	if !ok {
		return "", store.ErrNotFound
	}
	return v, nil
}

func (m *memStore) Set(_ context.Context, k, v string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.kv[k] = v
	return nil
}

func (m *memStore) MarkSetupCompleted(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completed++
	return nil
}

type fakeProjects struct {
	mu  sync.Mutex
	set map[string]string
}

func (f *fakeProjects) List(context.Context) ([]store.Project, error) { return nil, nil }
func (f *fakeProjects) SetPlane(_ context.Context, repo, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.set[repo] = id
	return nil
}

// fakeDoctor fails the named checks.
type fakeDoctor struct {
	mu    sync.Mutex
	fail  map[string]string // name -> fix
	calls int
}

func (f *fakeDoctor) run(_ context.Context, names ...string) []doctor.Check {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	var out []doctor.Check
	for _, n := range names {
		c := doctor.Check{Name: n, Detail: "ok"}
		if fix, ok := f.fail[n]; ok {
			c.Status, c.Detail, c.Fix = doctor.Fail, "broken", fix
		}
		out = append(out, c)
	}
	return out
}

// fakeSmoke scripts the throwaway session's statuses, one per Observe.
type fakeSmoke struct {
	mu       sync.Mutex
	script   []setup.Observation
	i        int
	cleanups int
	attached int
	lines    []string
}

func (f *fakeSmoke) Start(context.Context) (setup.SmokeRun, error) { return f, nil }

func (f *fakeSmoke) Observe(context.Context) (setup.Observation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := f.script[min(f.i, len(f.script)-1)]
	f.i++
	return o, nil
}

func (f *fakeSmoke) Preview(context.Context, int) (string, error) {
	return "Do you trust the files in this folder?\n❯ 1. Yes, proceed", nil
}

func (f *fakeSmoke) Attach(context.Context) (tmux.Attachment, error) {
	f.mu.Lock()
	f.attached++
	f.mu.Unlock()
	return tmux.Attachment{Mode: tmux.AttachSwitched}, nil
}

func (f *fakeSmoke) Checks(context.Context) []string { return f.lines }

func (f *fakeSmoke) Cleanup(context.Context) error {
	f.mu.Lock()
	f.cleanups++
	f.mu.Unlock()
	return nil
}

type env struct {
	t        *testing.T
	m        *Model
	store    *memStore
	doc      *fakeDoctor
	projects *fakeProjects
	tmux     *tmuxtest.Fake
	clip     *clipboard.Fake
	smoke    *fakeSmoke
	vars     map[string]string
	conf     string // config.yaml
	tmuxConf string
	clock    time.Time
	quit     bool
}

func newEnv(t *testing.T, only setup.StepID) *env {
	t.Helper()
	home := testutil.IsolateHome(t)
	dir := t.TempDir()
	e := &env{
		t: t, store: &memStore{kv: map[string]string{}}, doc: &fakeDoctor{fail: map[string]string{}},
		projects: &fakeProjects{set: map[string]string{}}, tmux: tmuxtest.NewFake(), clip: &clipboard.Fake{},
		smoke: &fakeSmoke{
			script: []setup.Observation{
				{Status: store.StatusStarting},
				{Status: store.StatusIdle, Event: "SessionStart"},
				{Status: store.StatusWorking, Event: "UserPromptSubmit"},
				{Status: store.StatusIdle, Event: "Stop"},
			},
			lines: []string{"✓ hooks: all 8 events registered", "✓ forge MCP server: registered", "✓ skills: synced"},
		},
		vars:     map[string]string{"SHELL": "/bin/zsh", "TERM_PROGRAM": "iTerm.app", "MY_PLANE_KEY": "secret"},
		conf:     filepath.Join(dir, "config.yaml"),
		tmuxConf: filepath.Join(dir, "tmux.conf"),
		clock:    time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC),
	}
	if err := os.WriteFile(e.conf, config.Template(), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(e.conf)
	if err != nil {
		t.Fatal(err)
	}
	fp := &testutil.FakePlane{MeUser: plane.User{DisplayName: "daan"}, ProjectList: []plane.Project{{ID: "p1", Name: "Forge", Identifier: "FORGE"}}}
	e.m = New(Deps{
		Config: cfg, ConfigPath: e.conf,
		Anim:   anim.Resolve(config.AnimationsOff, nil),
		Doctor: e.doc.run,
		Getenv: func(k string) string { return e.vars[k] },
		NewPlane: func(config.Plane, string) (plane.API, error) {
			return fp, nil
		},
		Store: e.store, Projects: e.projects, Cwd: home, Home: home,
		TmuxConf: e.tmuxConf, TmuxColors: tmux.ConfigColors{Bg: "#000000", Fg: "#ffffff", Accent: "#ff7a1a", Muted: "#888888"},
		NewTmux:   func(string, string) tmux.Driver { return e.tmux },
		Clipboard: e.clip, Smoke: e.smoke,
		Now:       func() time.Time { return e.clock },
		SmokePoll: time.Millisecond,
		PID:       99,
		Only:      only,
	})
	e.drive(e.m.Init())
	e.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	return e
}

var cmdType = reflect.TypeFor[tea.Cmd]()

// run executes cmd and flattens batches and sequences. Commands still
// running after a while (ticks longer than the smoke poll) are dropped.
func run(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(2 * time.Second):
		return nil
	}
	if msg == nil {
		return nil
	}
	if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice && v.Type().Elem() == cmdType {
		var out []tea.Msg
		for i := range v.Len() {
			out = append(out, run(v.Index(i).Interface().(tea.Cmd))...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// drive feeds the messages of cmd back into the model until none are left.
func (e *env) drive(cmd tea.Cmd) {
	e.t.Helper()
	queue := run(cmd)
	for i := 0; len(queue) > 0; i++ {
		if i > 500 {
			e.t.Fatal("drive: too many messages")
		}
		msg := queue[0]
		queue = queue[1:]
		if _, ok := msg.(anim.FrameMsg); ok {
			continue // no clock in tests
		}
		if _, ok := msg.(tea.QuitMsg); ok {
			e.quit = true
			continue
		}
		if _, ok := msg.(smokePollMsg); ok {
			e.clock = e.clock.Add(setup.SmokePoll)
		}
		_, c := e.m.Update(msg)
		queue = append(queue, run(c)...)
	}
}

func (e *env) send(msg tea.Msg) {
	e.t.Helper()
	_, c := e.m.Update(msg)
	e.drive(c)
}

func (e *env) press(keys ...string) {
	e.t.Helper()
	for _, k := range keys {
		e.send(keyMsg(k))
	}
}

func keyMsg(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "alt+d":
		return tea.KeyPressMsg{Code: 'd', Mod: tea.ModAlt}
	case "∂":
		return tea.KeyPressMsg{Code: '∂', Text: "∂"}
	}
	r := []rune(k)[0]
	return tea.KeyPressMsg{Code: r, Text: k}
}

func (e *env) screen() string { return ansi.Strip(e.m.render()) }

func (e *env) step() setup.StepID {
	s, _ := e.m.current()
	return s.ID
}

func (e *env) wantStep(id setup.StepID) {
	e.t.Helper()
	if got := e.step(); got != id || e.m.summary {
		e.t.Fatalf("on step %s (summary %v), want %s\n%s", got, e.m.summary, id, e.screen())
	}
}

func (e *env) wantScreen(parts ...string) {
	e.t.Helper()
	s := e.screen()
	for _, p := range parts {
		if !strings.Contains(s, p) {
			e.t.Fatalf("screen lacks %q:\n%s", p, s)
		}
	}
}

// --- tests -----------------------------------------------------------------

// Skipping every step ends on the summary listing all of them with the
// command to fix each, and records setup as completed.
func TestSkipEveryStep(t *testing.T) {
	e := newEnv(t, "")
	e.wantStep(setup.StepDeps)
	e.press("s")           // deps
	e.press("s")           // github
	e.press("esc", "s")    // plane: leave the form, skip
	e.press("s")           // projects
	e.press("esc", "s")    // tmux: skip the key test, skip
	e.press("s", "s", "s") // animations, capture, smoke
	if !e.m.summary {
		t.Fatalf("not on the summary:\n%s", e.screen())
	}
	for _, s := range setup.Steps {
		if e.m.results[s.ID].Status != setup.Skipped {
			t.Errorf("%s = %v", s.ID, e.m.results[s.ID].Status)
		}
	}
	e.wantScreen("These steps need another look", "forge setup plane", "forge setup smoke")
	if e.store.completed != 1 {
		t.Errorf("setup_completed marked %d times", e.store.completed)
	}
	// Results are persisted per step.
	if rs := setup.LoadResults(context.Background(), e.store); len(rs) != len(setup.Steps) {
		t.Errorf("saved %d results", len(rs))
	}
	e.press("enter")
	if !e.quit {
		t.Error("enter on the summary didn't quit")
	}
}

// A failed step can be re-run and then passes.
func TestRerunFailedStep(t *testing.T) {
	e := newEnv(t, "")
	e.doc.fail["tmux"] = "brew install tmux"
	e.press("r")
	e.wantScreen("fix: brew install tmux")
	if r := e.m.results[setup.StepDeps]; r.Status != setup.Failed || r.Fix != "brew install tmux" {
		t.Fatalf("deps = %+v", r)
	}
	delete(e.doc.fail, "tmux")
	e.press("r")
	if r := e.m.results[setup.StepDeps]; r.Status != setup.Done {
		t.Fatalf("after re-run deps = %+v", r)
	}
	e.press("enter")
	e.wantStep(setup.StepGitHub)

	// Jump back to deps from the list and re-run it again.
	e.press("tab", "k", "enter")
	e.wantStep(setup.StepDeps)
	calls := e.doc.calls
	e.press("r")
	if e.doc.calls != calls+1 {
		t.Error("re-run didn't check again")
	}
}

func TestGitHubLoginOffered(t *testing.T) {
	e := newEnv(t, setup.StepGitHub)
	e.doc.fail["gh"] = "gh auth login"
	e.press("r")
	e.wantScreen("gh auth login", "l gh auth login")
	if r := e.m.results[setup.StepGitHub]; r.Status != setup.Failed {
		t.Errorf("github = %+v", r)
	}
}

// `forge setup plane`: only the Plane step runs; the settings are merged
// into config.yaml (comments kept, no key stored) and setup_completed is
// left alone.
func TestSinglePlaneStep(t *testing.T) {
	e := newEnv(t, setup.StepPlane)
	if len(e.m.steps) != 1 {
		t.Fatalf("steps = %v", e.m.steps)
	}
	ps, _ := e.m.models[setup.StepPlane].(*planeStep)
	if ps.form == nil {
		t.Fatal("the Plane form didn't open")
	}
	// Fill the form through its bound values and complete it.
	ps.form = nil
	ps.cloud = "self"
	ps.in = setup.PlaneInput{BaseURL: "https://plane.example.com", Workspace: "acme", KeyEnv: "MY_PLANE_KEY"}
	e.drive(ps.test())
	e.wantScreen("connected as daan", "FORGE  Forge")
	if r := e.m.results[setup.StepPlane]; r.Status != setup.Done {
		t.Fatalf("plane = %+v", r)
	}
	data, _ := os.ReadFile(e.conf)
	s := string(data)
	for _, want := range []string{"base_url: https://plane.example.com", "workspace_slug: acme", "api_key_env: MY_PLANE_KEY", "# forge config"} {
		if !strings.Contains(s, want) {
			t.Errorf("config lacks %q", want)
		}
	}
	if strings.Contains(s, "secret") {
		t.Error("the API key was written to the config")
	}
	e.press("enter")
	if !e.m.summary || e.store.completed != 0 {
		t.Errorf("summary %v, completed %d", e.m.summary, e.store.completed)
	}
	e.wantScreen("Everything is set up")
}

// A missing key variable shows the exact shell line.
func TestPlaneMissingKey(t *testing.T) {
	e := newEnv(t, setup.StepPlane)
	ps := e.m.models[setup.StepPlane].(*planeStep)
	ps.form = nil
	ps.cloud = "cloud"
	ps.in = setup.PlaneInput{Workspace: "acme", KeyEnv: "NOT_SET_KEY"}
	e.drive(ps.test())
	e.wantScreen(`export NOT_SET_KEY="<your Plane API key>"`, "~/.zshrc")
	r := e.m.results[setup.StepPlane]
	if r.Status != setup.Failed || !strings.Contains(r.Fix, "~/.zshrc") {
		t.Errorf("plane = %+v", r)
	}
	// The valid settings are written anyway.
	data, _ := os.ReadFile(e.conf)
	if !strings.Contains(string(data), "workspace_slug: acme") {
		t.Error("settings not written")
	}
}

// The key test reads the configured detach key, and only an Alt key gets
// Option-as-Meta help for the detected terminal.
func TestTmuxKeyTest(t *testing.T) {
	e := newEnv(t, setup.StepTmux)
	if _, err := os.Stat(e.tmuxConf); err != nil {
		t.Fatalf("tmux.conf not written: %v", err)
	}
	e.wantScreen("wrote forge's tmux.conf", "Press Alt+d now")
	e.press("∂")
	e.wantScreen("isn't sending Option as Alt", "Left Option key")
	if r := e.m.results[setup.StepTmux]; r.Status != setup.Failed || !strings.Contains(r.Fix, "iTerm2") {
		t.Errorf("tmux = %+v", r)
	}
	e.press("r", "alt+d")
	e.wantScreen("Alt+d arrives as Alt+d")
	if r := e.m.results[setup.StepTmux]; r.Status != setup.Done {
		t.Errorf("tmux = %+v", r)
	}

	// The popup test runs on its own socket and comes back (as a popup of
	// the user's tmux here: ExecProcess needs a real program).
	e.tmux.Popup = true
	e.press("t")
	e.wantScreen("popup test: Alt+d brought you back")
	if ts, _ := e.tmux.ListAll(context.Background()); len(ts) != 0 {
		t.Errorf("popup windows left: %v", ts)
	}
}

func TestTmuxCustomDetachKey(t *testing.T) {
	e := newEnv(t, setup.StepTmux)
	conf := strings.Replace(string(tmux.DefaultConfig(e.m.d.TmuxColors)), "bind -n M-d detach-client", "bind -n C-] detach-client", 1)
	if err := os.WriteFile(e.tmuxConf, []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	e.press("esc", "r")
	e.wantScreen("Press Ctrl+] now")
	e.press("∂") // not an answer: asked again
	e.send(tea.KeyPressMsg{Code: ']'})
	s := e.screen()
	if strings.Contains(s, "Option") {
		t.Errorf("Option-as-Meta help for a Ctrl key:\n%s", s)
	}
}

func TestTmuxOutdatedReset(t *testing.T) {
	e := newEnv(t, "")
	if err := os.WriteFile(e.tmuxConf, []byte("# forge-tmux-config v1\nbind -n M-d detach-client\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	_ = e.m.jump(4)
	e.drive(e.m.models[setup.StepTmux].enter())
	e.press("esc")
	e.wantScreen("is v1; forge ships", "R reset tmux.conf")
	e.press("R")
	e.wantScreen("reset; your old file is at")
	if st, _ := setup.TmuxConfState(e.tmuxConf); st != setup.ConfCurrent {
		t.Errorf("after reset: %v", st)
	}
}

func TestAnimationsPreviewAndChoice(t *testing.T) {
	e := newEnv(t, setup.StepAnimations)
	e.wantScreen("Preview", "loading pull requests")
	e.press("j") // reduced
	e.press("enter")
	data, _ := os.ReadFile(e.conf)
	if !strings.Contains(string(data), "animations: reduced") {
		t.Error("animations not written")
	}
	if e.m.eng.Settings().Level != config.AnimationsReduced {
		t.Error("the wizard didn't follow the choice")
	}
}

func TestCaptureCopy(t *testing.T) {
	e := newEnv(t, setup.StepCapture)
	e.wantScreen(`bind -n M-c display-popup -E -w 76 -h 8 "forge capture"`)
	e.press("c")
	if got := e.clip.Copied(); len(got) != 1 || got[0] != setup.CaptureSnippet {
		t.Errorf("copied %q", got)
	}
	e.wantScreen("copied to the clipboard")
}

func TestSmokePasses(t *testing.T) {
	e := newEnv(t, setup.StepSmoke)
	e.wantScreen("a few tokens")
	e.press("enter")
	r := e.m.results[setup.StepSmoke]
	if r.Status != setup.Done || len(r.Lines) != 3 {
		t.Fatalf("smoke = %+v\n%s", r, e.screen())
	}
	if e.smoke.cleanups != 1 {
		t.Errorf("cleanups = %d", e.smoke.cleanups)
	}
	e.wantScreen("✓ forge MCP server", "✓ skills")
}

// No progress before the prompt: the pane is shown with the attach hint;
// attaching keeps the test going.
func TestSmokeNeedsInput(t *testing.T) {
	e := newEnv(t, setup.StepSmoke)
	stuck := make([]setup.Observation, 25) // 12.5s of "starting"
	for i := range stuck {
		stuck[i] = setup.Observation{Status: store.StatusStarting}
	}
	stuck = append(stuck,
		setup.Observation{Status: store.StatusWorking, Event: "UserPromptSubmit"},
		setup.Observation{Status: store.StatusIdle, Event: "Stop"})
	e.smoke.script = stuck
	sm := e.m.models[setup.StepSmoke].(*smokeStep)
	// Stop once the state machine asks for input.
	e.drive(sm.start())
	if sm.state.Phase != setup.SmokePassed {
		t.Fatalf("phase = %v", sm.state.Phase)
	}
	if sm.preview == "" {
		t.Error("no pane preview while claude was asking")
	}
}

func TestSmokeTimeoutReportsLastEvent(t *testing.T) {
	e := newEnv(t, setup.StepSmoke)
	e.smoke.script = []setup.Observation{{Status: store.StatusIdle, Event: "SessionStart"}}
	e.press("enter")
	r := e.m.results[setup.StepSmoke]
	if r.Status != setup.Failed || !strings.Contains(r.Detail, "last event: SessionStart") {
		t.Fatalf("smoke = %+v", r)
	}
	e.wantScreen("Do you trust the files") // the pane it was stuck on
	if e.smoke.cleanups != 1 {
		t.Errorf("cleanups = %d", e.smoke.cleanups)
	}
}

func TestSmokeMissingRegistrationsFail(t *testing.T) {
	e := newEnv(t, setup.StepSmoke)
	e.smoke.lines = []string{"✓ hooks", "✗ forge MCP server: no mcp.json for the session", "✓ skills"}
	e.press("enter")
	if r := e.m.results[setup.StepSmoke]; r.Status != setup.Failed || r.Fix != "forge doctor --fix" {
		t.Errorf("smoke = %+v", r)
	}
}

func TestProjectsStep(t *testing.T) {
	e := newEnv(t, setup.StepProjects)
	home := e.m.d.Home
	root := filepath.Join(home, "work")
	for name, repo := range map[string]string{"forge": "daan/forge", "api": "daan/api"} {
		dir := filepath.Join(root, name, ".git")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config"), []byte("[remote \"origin\"]\n\turl = https://github.com/"+repo+".git\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e.m.planeProjects = []plane.Project{{ID: "p1", Name: "Forge", Identifier: "FORGE"}}
	ps := e.m.models[setup.StepProjects].(*projectsStep)
	// What picking ~/work in the folder picker does.
	ps.roots = setup.AddRoot(nil, root, home)
	e.drive(tea.Batch(e.m.merge(setup.RootSets(ps.roots)...), ps.discover()))
	e.wantScreen("~/work", "daan/forge", "Forge (matched by name)", "daan/api")
	data, _ := os.ReadFile(e.conf)
	if !strings.Contains(string(data), "project_roots:\n  - ~/work") {
		t.Errorf("roots not written:\n%s", data)
	}
	e.press("enter")
	if e.projects.set["daan/forge"] != "p1" {
		t.Errorf("match not saved: %v", e.projects.set)
	}
	if r := e.m.results[setup.StepProjects]; r.Status != setup.Done || !strings.Contains(r.Detail, "2 repo(s)") {
		t.Errorf("projects = %+v", r)
	}
}

// Quitting mid smoke test removes the throwaway session.
func TestQuitCleansUpSmoke(t *testing.T) {
	e := newEnv(t, setup.StepSmoke)
	e.smoke.script = []setup.Observation{{Status: store.StatusStarting}}
	sm := e.m.models[setup.StepSmoke].(*smokeStep)
	_, c := e.m.Update(keyMsg("enter"))
	for _, msg := range run(c) {
		if s, ok := msg.(smokeStartedMsg); ok {
			e.m.Update(s)
		}
	}
	if sm.run == nil {
		t.Fatal("not running")
	}
	e.press("q")
	if !e.quit || e.smoke.cleanups != 1 {
		t.Errorf("quit %v, cleanups %d", e.quit, e.smoke.cleanups)
	}
}

// The real program: renders, and q quits.
func TestQuitFlow(t *testing.T) {
	e := newEnv(t, setup.StepCapture)
	tm := teatest.NewTestModel(t, e.m, teatest.WithInitialTermSize(100, 30))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("Quick capture"))
	}, teatest.WithDuration(3*time.Second))
	tm.Send(tea.KeyPressMsg{Code: 'q', Text: "q"})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

func TestHelpLineFollowsKeymap(t *testing.T) {
	e := newEnv(t, setup.StepCapture)
	e.wantScreen("c copy", "enter continue", "s skip", "tab steps / step", "q/ctrl+c quit")
	if !slices.Contains(e.m.keys.Setup.Copy.Keys(), "c") {
		t.Error("keymap")
	}
}
