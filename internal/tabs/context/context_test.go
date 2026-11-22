package context

import (
	stdctx "context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/memory"
	"github.com/daan3011/forge/internal/sessions/worktree"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

type fakeGit struct {
	wts []worktree.Worktree
	err error
}

func (g fakeGit) WorktreeList(stdctx.Context, string) ([]worktree.Worktree, error) {
	return g.wts, g.err
}

type fixture struct {
	cfg        *config.Config
	home, repo string
	wt         string
	git        fakeGit

	mu     sync.Mutex
	edited []string
	// onEdit runs inside the fake editor (to change the file).
	onEdit func(path string)
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		cfg:  config.Default(),
		home: filepath.Join(base, "home"),
		repo: filepath.Join(base, "src", "proj"),
		wt:   filepath.Join(base, "worktrees", "proj", "fix-ci"),
	}
	f.cfg.Projects = []config.Project{{Name: "proj", Repo: "o/proj", Path: f.repo}}
	write(t, filepath.Join(f.home, ".claude", "CLAUDE.md"), "# User rules\n\nBe brief.\n")
	write(t, filepath.Join(f.repo, "CLAUDE.md"), "# Project\n\nSee @docs/SPEC.md\n")
	write(t, filepath.Join(f.repo, "docs", "SPEC.md"), "# Spec\n\n"+strings.Repeat("spec text ", 40)+"\n")
	write(t, filepath.Join(f.repo, "internal", "CLAUDE.md"), "# Internal\n")
	write(t, memory.AutoMemoryPath(f.home, f.repo), "- [note](note.md) — a memory\n")
	write(t, filepath.Join(f.wt, "CLAUDE.md"), "# Project on a branch\n")
	f.git = fakeGit{wts: []worktree.Worktree{{Path: f.repo, Branch: "main"}, {Path: f.wt, Branch: "agent/fix-ci"}}}
	return f
}

func (f *fixture) tab(t *testing.T) *Tab {
	t.Helper()
	tab := newTab(tabkit.Deps{Theme: ui.NewTheme(true, nil), Keys: ui.DefaultKeyMap(), Config: f.cfg})
	tab.home, tab.managed, tab.git = f.home, "", f.git
	tab.now = func() time.Time { return time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC) }
	tab.edit = func(path string) tea.Cmd {
		f.mu.Lock()
		f.edited = append(f.edited, path)
		on := f.onEdit
		f.mu.Unlock()
		return func() tea.Msg {
			if on != nil {
				on(path)
			}
			return editedMsg{path: path}
		}
	}
	tab.SetProject(&f.cfg.Projects[0])
	tab.SetSize(120, 30)
	return tab
}

func (f *fixture) editedPaths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.edited)
}

func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, drain(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// pump runs cmd and feeds the tab's own messages back until none are left;
// it returns the toasts.
func pump(tab *Tab, cmd tea.Cmd) (toasts []string) {
	queue := drain(cmd)
	for len(queue) > 0 {
		msg := queue[0]
		queue = queue[1:]
		switch m := msg.(type) {
		case ui.ErrMsg:
			toasts = append(toasts, "err: "+m.Err.Error())
		case tabs.TargetedMsg:
			_, next := tab.Update(m)
			queue = append(queue, drain(next)...)
		}
	}
	return toasts
}

func send(tab *Tab, s string) []string {
	var kp tea.KeyPressMsg
	switch s {
	case "enter":
		kp = tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		kp = tea.KeyPressMsg{Code: tea.KeyEscape}
	default:
		kp = tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
	}
	_, cmd := tab.Update(kp)
	return pump(tab, cmd)
}

func view(tab *Tab) string { return ansi.Strip(tab.View()) }

// selectPath moves the cursor to the row showing path.
func selectPath(t *testing.T, tab *Tab, path string) {
	t.Helper()
	for range tab.rows {
		if f, ok := tab.selected(); ok && f.Path == path {
			return
		}
		send(tab, "j")
	}
	t.Fatalf("no row for %s", path)
}

func TestListGroupsAndTotal(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(t)
	pump(tab, tab.Refresh())

	v := view(tab)
	for _, want := range []string{
		"Loaded at start", "~/.claude/CLAUDE.md", "CLAUDE.md", "↳ docs/SPEC.md", "MEMORY.md (auto memory)",
		"CLAUDE.local.md", "+ create",
		"On demand", "internal/CLAUDE.md",
		"Total ≈", "/ 8k tokens",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "over budget") {
		t.Error("small files shouldn't be over budget")
	}
	// The nested file isn't part of the total.
	total := memory.Total(tab.files)
	for _, file := range tab.files {
		if file.Source == memory.Nested && total == 0 {
			t.Error("total is zero")
		}
	}
	if !strings.Contains(tab.StatusDetail(), "tokens at start") {
		t.Errorf("status = %q", tab.StatusDetail())
	}
}

func TestOverBudgetIsFlagged(t *testing.T) {
	f := newFixture(t)
	f.cfg.Context.TokenBudget = 10
	tab := f.tab(t)
	pump(tab, tab.Refresh())
	if !tab.overBudget() {
		t.Fatal("expected over budget")
	}
	if v := view(tab); !strings.Contains(v, "⚠ Total") || !strings.Contains(v, "over budget") {
		t.Errorf("no warning in view:\n%s", v)
	}
	if !strings.Contains(tab.StatusDetail(), "over budget") {
		t.Errorf("status = %q", tab.StatusDetail())
	}
	f.cfg.Context.TokenBudget = 0
	if tab.overBudget() || strings.Contains(view(tab), "over budget") {
		t.Error("budget 0 should turn the warning off")
	}
}

func TestPreviewRenders(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(t)
	pump(tab, tab.Refresh())
	selectPath(t, tab, filepath.Join(f.repo, "docs", "SPEC.md"))
	pump(tab, tab.syncDetail())
	v := view(tab)
	for _, want := range []string{"import", "imported by CLAUDE.md", "spec text"} {
		if !strings.Contains(v, want) {
			t.Errorf("detail lacks %q:\n%s", want, v)
		}
	}
}

func TestEditOpensSelectedAndRescans(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(t)
	pump(tab, tab.Refresh())
	path := filepath.Join(f.repo, "CLAUDE.md")
	selectPath(t, tab, path)
	f.onEdit = func(p string) { write(t, p, "# Project\n\n"+strings.Repeat("grown ", 4000)) }
	before := memory.Total(tab.files)
	if toasts := send(tab, "e"); len(toasts) > 0 {
		t.Fatalf("toasts: %q", toasts)
	}
	if got := f.editedPaths(); !slices.Equal(got, []string{path}) {
		t.Fatalf("edited %q", got)
	}
	if after := memory.Total(tab.files); after <= before {
		t.Errorf("total %d -> %d, want a rescan after edit", before, after)
	}
	// The SPEC import is gone now, and the cursor stayed on CLAUDE.md.
	if file, _ := tab.selected(); file.Path != path {
		t.Errorf("selection moved to %s", file.Path)
	}
}

func TestEditCreatesMissingFile(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(t)
	pump(tab, tab.Refresh())
	path := filepath.Join(f.repo, "CLAUDE.local.md")
	selectPath(t, tab, path)
	send(tab, "e")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("not created: %v", err)
	}
	if got := f.editedPaths(); !slices.Equal(got, []string{path}) {
		t.Fatalf("edited %q", got)
	}
	if file, _ := tab.selected(); !file.Exists {
		t.Error("created file still shown as missing")
	}
}

func TestWorktreeMatrix(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(t)
	pump(tab, tab.Refresh())
	send(tab, "i")
	v := view(tab)
	for _, want := range []string{"Worktree inheritance", "1 checkout", "2 agent/fix-ci", "docs/SPEC.md"} {
		if !strings.Contains(v, want) {
			t.Errorf("matrix lacks %q:\n%s", want, v)
		}
	}
	cells := map[string][]memory.Cell{}
	for _, r := range tab.inherit {
		cells[filepath.Base(r.Path)] = r.Cells
	}
	if got := cells["CLAUDE.md"]; !slices.Equal(got, []memory.Cell{memory.Same, memory.Differs}) {
		t.Errorf("CLAUDE.md cells %v", got)
	}
	if got := cells["SPEC.md"]; !slices.Equal(got, []memory.Cell{memory.Same, memory.Absent}) {
		t.Errorf("SPEC.md cells %v", got)
	}
	if got := cells["MEMORY.md"]; !slices.Equal(got, []memory.Cell{memory.Shared, memory.Shared}) {
		t.Errorf("MEMORY.md cells %v", got)
	}
	send(tab, "i")
	if strings.Contains(view(tab), "Worktree inheritance") {
		t.Error("i should toggle the matrix off")
	}
}

func TestWorktreeErrorAndNoProject(t *testing.T) {
	f := newFixture(t)
	f.git = fakeGit{err: errors.New("not a git repository")}
	tab := f.tab(t)
	pump(tab, tab.Refresh())
	send(tab, "i")
	if v := view(tab); !strings.Contains(v, "Can't list worktrees: not a git repository") {
		t.Errorf("no error shown:\n%s", v)
	}

	tab.SetProject(nil)
	pump(tab, tab.Refresh())
	v := view(tab)
	if !strings.Contains(v, "~/.claude/CLAUDE.md") || strings.Contains(v, "CLAUDE.local.md") {
		t.Errorf("no-project view should list user files only:\n%s", v)
	}
	if !strings.Contains(v, "No checkout for all projects") {
		t.Errorf("matrix should explain no checkout:\n%s", v)
	}
}

func TestFocusAndScroll(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(t)
	pump(tab, tab.Refresh())
	send(tab, "enter")
	if !tab.focusDetail {
		t.Fatal("enter should focus the detail")
	}
	send(tab, "esc")
	if tab.focusDetail {
		t.Fatal("esc should go back to the list")
	}
}

func TestCommandsRegistered(t *testing.T) {
	tab := newFixture(t).tab(t)
	ids := map[string]bool{}
	for _, c := range tab.Commands() {
		ids[c.ID] = true
	}
	for _, id := range []string{"context_edit", "context_worktrees", "context_open", "context_back", "context_detail_down", "context_detail_up"} {
		if !ids[id] {
			t.Errorf("missing palette command %s", id)
		}
	}
	known := map[string]bool{}
	for _, id := range ui.ActionIDs() {
		known[id] = true
	}
	for id := range ids {
		if !known[id] {
			t.Errorf("palette command %s has no keymap action", id)
		}
	}
}

// --- teatest flow ----------------------------------------------------------------

type host struct {
	tab   *Tab
	mu    sync.Mutex
	frame string
}

func (h *host) Init() tea.Cmd { return h.tab.Refresh() }

func (h *host) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg, ui.ErrMsg:
		return h, nil
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return h, tea.Quit
		}
	}
	_, cmd := h.tab.Update(msg)
	return h, cmd
}

func (h *host) View() tea.View {
	v := h.tab.View()
	h.mu.Lock()
	h.frame = ansi.Strip(v)
	h.mu.Unlock()
	return tea.NewView(v)
}

func waitFor(t *testing.T, h *host, want ...string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.mu.Lock()
		frame := h.frame
		h.mu.Unlock()
		ok := true
		for _, w := range want {
			ok = ok && strings.Contains(frame, w)
		}
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q\nframe:\n%s", want, frame)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestEditFlow: select the imported spec, edit it, see the new content.
func TestEditFlow(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(t)
	f.onEdit = func(p string) { write(t, p, "# Spec\n\nREWRITTEN BY EDITOR\n") }
	h := &host{tab: tab}
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(120, 30))
	waitFor(t, h, "↳ docs/SPEC.md", "Total ≈")

	for range 2 { // user CLAUDE.md -> project CLAUDE.md -> SPEC import
		tm.Send(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	waitFor(t, h, "imported by CLAUDE.md", "spec text")
	tm.Send(tea.KeyPressMsg{Code: 'e', Text: "e"})
	waitFor(t, h, "REWRITTEN BY EDITOR")

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}
