package app

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/customcmd"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/sessions/tmux/tmuxtest"
	"github.com/daan3011/forge/internal/sessions/worktree"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

// fakeRunner records custom command runs.
type fakeRunner struct {
	mu     sync.Mutex
	calls  []runCall
	result customcmd.Result
	cmd    string // what Cmd runs: "true" or "false"
}

type runCall struct {
	dir, script string
	env         []string
	exec        bool
}

func (f *fakeRunner) Run(_ context.Context, dir, script string, env []string) customcmd.Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, runCall{dir: dir, script: script, env: env})
	return f.result
}

func (f *fakeRunner) Cmd(dir, script string, env []string) *exec.Cmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, runCall{dir: dir, script: script, env: env, exec: true})
	bin := f.cmd
	if bin == "" {
		bin = "true"
	}
	return exec.CommandContext(context.Background(), bin)
}

func (f *fakeRunner) last(t *testing.T) runCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("no command ran")
	}
	return f.calls[len(f.calls)-1]
}

func (f *fakeRunner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// TestOpenInEditorOnSession is P6.3's done-when: a custom "open in editor"
// command, bound to ctrl+e on the Sessions tab, runs on the selected
// session's worktree.
func TestOpenInEditorOnSession(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	ctx := context.Background()
	dir := t.TempDir()
	checkout := filepath.Join(dir, "forge")
	origin := filepath.Join(dir, "origin.git")
	git(t, dir, "init", "-q", "--bare", "-b", "main", origin)
	git(t, dir, "clone", "-q", origin, checkout)
	git(t, checkout, "commit", "-q", "--allow-empty", "-m", "init")
	git(t, checkout, "push", "-q", "origin", "HEAD:main")

	st, err := store.Open(ctx, filepath.Join(dir, "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := config.Default()
	cfg.Projects = []config.Project{{Name: "forge", Repo: "daan/forge", Path: checkout}}
	cfg.CustomCommands = []config.CustomCommand{{
		Name: "open-in-editor", Key: "ctrl+e", Tabs: []string{"sessions"},
		Command: "code {worktree}", Description: "Open the worktree in VS Code",
	}}
	svc := agent.New(agent.Config{
		Tmux: tmuxtest.NewFake(), Worktrees: worktree.New(filepath.Join(dir, "wt"), worktree.NewExec()),
		Store: agent.StoreFrom(st), Projects: cfg.Projects, DataDir: filepath.Join(dir, "data"), ClaudeBin: "claude",
		ProjectsDir: filepath.Join(dir, "claude"), StartupCheck: time.Nanosecond,
	})
	started, err := svc.Start(ctx, agent.StartOpts{Name: "fix-login", Project: "forge", Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	wt := started.Session.Worktree

	runner := &fakeRunner{result: customcmd.Result{Took: 300 * time.Millisecond}}
	m := mustNew(t, cfg, WithSessions(svc), WithStore(st.Cache()), WithCommandRunner(runner))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(160, 30))
	var seen []byte
	wait := func(s string) {
		t.Helper()
		teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
			if !bytes.Contains(append(slices.Clip(seen), b...), []byte(s)) {
				return false
			}
			seen = append(seen, b...)
			return true
		}, teatest.WithDuration(10*time.Second))
	}
	wait("forge")
	tm.Send(press("5")) // Sessions
	wait("fix-login")
	tm.Send(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	wait("open-in-editor done")

	call := runner.last(t)
	if call.script != "code "+shellQuote(wt) || call.dir != wt || call.exec {
		t.Errorf("ran %q in %q (exec %v), want code %s in it", call.script, call.dir, call.exec, wt)
	}
	for _, want := range []string{"FORGE_SESSION=fix-login", "FORGE_WORKTREE=" + wt, "FORGE_REPO=daan/forge", "FORGE_BRANCH=" + started.Session.Branch} {
		if !slices.Contains(call.env, want) {
			t.Errorf("env %v lacks %s", call.env, want)
		}
	}
	tm.Send(press("q"))
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// selTab is a fake tab with a selection.
type selTab struct {
	fakeTab
	sel tabs.Selection
}

func (s *selTab) Selection() tabs.Selection { return s.sel }

func (s *selTab) Update(msg tea.Msg) (tabs.Tab, tea.Cmd) {
	_, cmd := s.fakeTab.Update(msg)
	return s, cmd
}

// customModel builds a model with two fake tabs, Sessions (selecting a
// session) and PRs (selecting nothing), and the given commands.
func customModel(t *testing.T, cmds []config.CustomCommand, opts ...Option) (Model, *fakeRunner) {
	t.Helper()
	cfg := config.Default()
	cfg.Projects = []config.Project{{Name: "forge", Repo: "daan/forge", Path: "/code/forge"}}
	cfg.CustomCommands = cmds
	keys := ui.DefaultKeyMap()
	if err := keys.SetCustom(customKeys(cmds), config.TabNames); err != nil {
		t.Fatal(err)
	}
	sess := &selTab{fakeTab: fakeTab{title: "Sessions"}, sel: tabs.Selection{Repo: "daan/forge", Branch: "fix", Worktree: "/wt/fix", Session: "fix", Path: "/wt/fix"}}
	prs := &selTab{fakeTab: fakeTab{title: "PRs"}}
	m := newModel(cfg, ui.NewTheme(true, nil), keys, []tabs.Tab{sess, prs})
	runner := &fakeRunner{}
	m.custom.runner = runner
	m.custom.exec = func(c *exec.Cmd, cb tea.ExecCallback) tea.Cmd {
		return func() tea.Msg { return cb(c.Run()) }
	}
	for _, o := range opts {
		o(&m)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return next.(Model), runner
}

// pumpCustom runs cmd, feeding custom command and toast messages back in.
func pumpCustom(t *testing.T, m Model, cmd tea.Cmd) (Model, []tea.Msg) {
	t.Helper()
	var seen []tea.Msg
	queue := runAll(cmd)
	for len(queue) > 0 {
		msg := queue[0]
		queue = queue[1:]
		seen = append(seen, msg)
		switch msg.(type) {
		case customDoneMsg, customExecMsg, customWindowMsg, customDetachedMsg, ui.SuccessMsg, ui.InfoMsg, ui.ErrMsg:
			next, c := m.Update(msg)
			m = next.(Model)
			queue = append(queue, runAll(c)...)
		}
	}
	return m, seen
}

func ctrlE() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl} }

func TestCustomKeyOnlyOnItsTabs(t *testing.T) {
	m, runner := customModel(t, []config.CustomCommand{{Name: "edit", Key: "ctrl+e", Tabs: []string{"sessions"}, Command: "code {worktree}"}})
	next, cmd := m.Update(ctrlE())
	m, _ = pumpCustom(t, next.(Model), cmd)
	if got := runner.last(t); got.script != "code '/wt/fix'" || got.dir != "/wt/fix" {
		t.Fatalf("ran %+v", got)
	}
	if !strings.Contains(screen(m), "edit done") {
		t.Fatalf("no success toast:\n%s", screen(m))
	}

	m, _ = keyPress(m, t, press("2")) // PRs: the key goes to the tab
	next, cmd = m.Update(ctrlE())
	_, _ = pumpCustom(t, next.(Model), cmd)
	if runner.count() != 1 {
		t.Fatalf("ran on PRs: %+v", runner.calls)
	}
	if prs := m.tabs[1].(*selTab); !slices.Contains(prs.keys, "ctrl+e") {
		t.Fatalf("PRs tab didn't get ctrl+e: %v", prs.keys)
	}
}

func TestCustomPaletteAndHelp(t *testing.T) {
	m, runner := customModel(t, []config.CustomCommand{
		{Name: "edit", Key: "ctrl+e", Tabs: []string{"sessions"}, Command: "code {worktree}", Description: "Open in VS Code"},
		{Name: "repo-web", Command: "gh repo view --web {repo}"},
	})
	ids := func(m Model) []string {
		var out []string
		for _, it := range m.paletteItems() {
			if strings.HasPrefix(it.id, "custom") {
				out = append(out, it.id)
			}
		}
		return out
	}
	if got := ids(m); !slices.Equal(got, []string{"custom:edit", "custom:repo-web", "custom_output"}) {
		t.Fatalf("Sessions palette = %v", got)
	}
	m.openHelp()
	if v := screen(m); !strings.Contains(v, "Custom commands") || !strings.Contains(v, "Open in VS Code") || !strings.Contains(v, "Command output") {
		t.Fatalf("help lacks the command:\n%s", v)
	}
	m.helpOpen = false

	m, _ = keyPress(m, t, press("2")) // PRs
	if got := ids(m); !slices.Equal(got, []string{"custom:repo-web", "custom_output"}) {
		t.Fatalf("PRs palette = %v", got)
	}
	// The PRs tab selects nothing: {repo} comes from the project.
	next, cmd := runPalette(t, m, "repo-web")
	_, _ = pumpCustom(t, next.(Model), cmd)
	if got := runner.last(t); got.script != "gh repo view --web 'daan/forge'" || got.dir != "/code/forge" {
		t.Fatalf("ran %+v", got)
	}
}

func TestCustomMissingVariable(t *testing.T) {
	m, runner := customModel(t, []config.CustomCommand{{Name: "review", Key: "ctrl+e", Command: "gh pr checkout {pr}"}})
	next, cmd := m.Update(ctrlE())
	m, _ = pumpCustom(t, next.(Model), cmd)
	if runner.count() != 0 {
		t.Fatal("ran without {pr}")
	}
	if v := screen(m); !strings.Contains(v, "review: needs {pr}") {
		t.Fatalf("no error toast:\n%s", v)
	}
}

func TestCustomFailureAndOutput(t *testing.T) {
	m, runner := customModel(t, []config.CustomCommand{{Name: "deploy", Key: "ctrl+e", Command: "make deploy"}})
	runner.result = customcmd.Result{Exit: 2, Output: []byte("building\nerror: no target\n"), Took: time.Second}
	next, cmd := m.Update(ctrlE())
	m, _ = pumpCustom(t, next.(Model), cmd)
	if v := screen(m); !strings.Contains(v, "deploy exited 2") || !strings.Contains(v, "Command output") {
		t.Fatalf("no failure toast:\n%s", v)
	}

	next, _ = runPalette(t, m, "Command output")
	m = next.(Model)
	if m.overlay != overlayOutputs || len(m.shown) != 1 || !strings.Contains(m.shown[0].detail, "exit 2") {
		t.Fatalf("picker = %v %+v", m.overlay, m.shown)
	}
	next, _ = m.Update(enter)
	m = next.(Model)
	v := screen(m)
	if !m.outputOpen || !strings.Contains(v, "error: no target") || !strings.Contains(v, "$ make deploy") {
		t.Fatalf("viewer:\n%s", v)
	}
	next, _ = m.Update(esc)
	if next.(Model).outputOpen {
		t.Fatal("esc should close the viewer")
	}
}

func TestCustomOutputKeepsTen(t *testing.T) {
	m, _ := customModel(t, []config.CustomCommand{{Name: "x", Command: "true"}})
	if _, cmd := m.openOutputs(); cmd == nil {
		t.Fatal("want a toast with no runs")
	}
	for i := range 12 {
		m, _ = m.handleCustomDone(customDoneMsg{run: ui.CommandRun{Name: "x", Exit: i}})
	}
	if len(m.custom.runs) != customRunsKept || m.custom.runs[0].Exit != 11 {
		t.Fatalf("runs = %d, newest exit %d", len(m.custom.runs), m.custom.runs[0].Exit)
	}
}

func TestCustomExec(t *testing.T) {
	m, runner := customModel(t, []config.CustomCommand{{Name: "nvim", Key: "ctrl+e", Mode: config.ModeExec, Command: "nvim ."}})
	next, cmd := m.Update(ctrlE())
	m, seen := pumpCustom(t, next.(Model), cmd)
	if got := runner.last(t); !got.exec || got.script != "nvim ." || got.dir != "/wt/fix" {
		t.Fatalf("ran %+v", got)
	}
	if strings.Contains(screen(m), "nvim exited") || m.tabs[0].(*selTab).refreshes != 1 {
		t.Fatalf("after a clean exit: %v, refreshes %d", seen, m.tabs[0].(*selTab).refreshes)
	}

	runner.cmd = "false"
	next, cmd = m.Update(ctrlE())
	m, _ = pumpCustom(t, next.(Model), cmd)
	if v := screen(m); !strings.Contains(v, "nvim exited 1") {
		t.Fatalf("no failure toast:\n%s", v)
	}
}

func TestCustomTmuxWindow(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	fake := tmuxtest.NewFake()
	svc := agent.New(agent.Config{Tmux: fake, Store: agent.StoreFrom(st), DataDir: dir, ClaudeBin: "claude"})
	no := false
	m, _ := customModel(t, []config.CustomCommand{
		{Name: "lazygit", Key: "ctrl+e", Mode: config.ModeTmuxWindow, Command: "lazygit"},
		{Name: "dev", Mode: config.ModeTmuxWindow, Attach: &no, Command: "npm run dev"},
	}, WithSessions(svc))

	// Attach: started in the worktree on the project's tmux session, shown.
	next, cmd := m.Update(ctrlE())
	m, _ = pumpCustom(t, next.(Model), cmd)
	lg := tmux.Target{Session: "forge", Window: "cmd-lazygit-fix"}
	wdir, argv, env, ok := fake.Started(lg)
	if !ok || wdir != "/wt/fix" || !slices.Equal(argv, []string{"sh", "-c", "lazygit"}) || env["FORGE_SESSION"] != "fix" {
		t.Fatalf("window %v %q %v %v", ok, wdir, argv, env)
	}
	if got := fake.Attached(); !slices.Equal(got, []tmux.Target{lg}) {
		t.Fatalf("attached %v", got)
	}

	// attach: false starts it detached with a toast; running it again
	// goes to the window instead of starting a second one.
	next, cmd = runPalette(t, m, "dev")
	m, _ = pumpCustom(t, next.(Model), cmd)
	if v := screen(m); !strings.Contains(v, "dev started in tmux window forge:cmd-dev-fix") {
		t.Fatalf("no toast:\n%s", v)
	}
	next, cmd = runPalette(t, m, "dev")
	_, _ = pumpCustom(t, next.(Model), cmd)
	dev := tmux.Target{Session: "forge", Window: "cmd-dev-fix"}
	if got := fake.Attached(); !slices.Equal(got, []tmux.Target{lg, dev}) {
		t.Fatalf("attached %v", got)
	}
}

func TestCustomTmuxUnavailable(t *testing.T) {
	m, _ := customModel(t, []config.CustomCommand{{Name: "lg", Key: "ctrl+e", Mode: config.ModeTmuxWindow, Command: "lazygit"}})
	next, cmd := m.Update(ctrlE())
	m, _ = pumpCustom(t, next.(Model), cmd)
	if v := screen(m); !strings.Contains(v, "lg: tmux is unavailable") {
		t.Fatalf("no toast:\n%s", v)
	}
}

func TestCustomKeyConflictFailsStartup(t *testing.T) {
	cfg := config.Default()
	cfg.CustomCommands = []config.CustomCommand{{Name: "x", Key: "x", Tabs: []string{"sessions"}, Command: "true"}}
	if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), `custom_commands[0].key: "x" is already bound to session_kill`) {
		t.Fatalf("err = %v", err)
	}
}

// TestTabNamesMatchTabs keeps config.TabNames in step with the tabs.
func TestTabNamesMatchTabs(t *testing.T) {
	m := mustNew(t, config.Default())
	var got []string
	for _, tab := range m.tabs {
		got = append(got, strings.ToLower(tab.Title()))
		if _, ok := tab.(tabs.Selector); !ok {
			t.Errorf("tab %s has no Selection", tab.Title())
		}
	}
	if !slices.Equal(got, config.TabNames) {
		t.Fatalf("tabs %v, config.TabNames %v", got, config.TabNames)
	}
}
