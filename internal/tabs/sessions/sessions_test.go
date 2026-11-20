package sessions

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/config"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/worktree"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
)

// fakeSvc is an in-memory agent.API.
type fakeSvc struct {
	mu       sync.Mutex
	rows     []agent.Row
	output   map[string]string
	starts   []agent.StartOpts
	kills    []agent.KillOpts
	previews int
	dirty    bool
}

func newFakeSvc(rows ...agent.Row) *fakeSvc {
	return &fakeSvc{rows: rows, output: map[string]string{}}
}

func row(name string, st store.Status) agent.Row {
	return agent.Row{Session: store.Session{
		ID: int64(len(name)), Name: name, Project: "forge", Branch: "agent/" + name, BaseBranch: "main",
		Worktree: "/wt/" + name, TmuxSession: "forge", TmuxWindow: name, Status: st,
		ClaudeSessionID: "uuid-" + name,
	}}
}

func (f *fakeSvc) Start(_ context.Context, o agent.StartOpts) (agent.Started, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, o)
	r := row(o.Name, store.StatusStarting)
	r.Project, r.Template = o.Project, o.Template
	f.rows = append(f.rows, r)
	f.output[o.Name] = "claude ready> " + o.Prompt
	return agent.Started{Session: r.Session}, nil
}

func (f *fakeSvc) List(_ context.Context, project string, archived bool) ([]agent.Row, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []agent.Row
	for _, r := range f.rows {
		if (project == "" || r.Project == project) && (r.Status == store.StatusArchived) == archived {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeSvc) Preview(_ context.Context, x store.Session, _ int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.previews++
	return f.output[x.Name], nil
}

func (f *fakeSvc) Kill(_ context.Context, x store.Session, o agent.KillOpts) (agent.Killed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kills = append(f.kills, o)
	for i := range f.rows {
		if f.rows[i].Name != x.Name {
			continue
		}
		f.rows[i].Status = store.StatusDead
		if !o.RemoveWorktree {
			return agent.Killed{}, nil
		}
		if f.dirty && !o.Force {
			return agent.Killed{}, &worktree.DirtyError{Path: x.Worktree, Files: []string{" M a.go", "?? b.go"}}
		}
		f.rows[i].Status, f.rows[i].Worktree, f.rows[i].TmuxWindow = store.StatusArchived, "", ""
		return agent.Killed{Archived: true}, nil
	}
	return agent.Killed{}, errors.New("no such session")
}

func (f *fakeSvc) Delete(_ context.Context, x store.Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.rows {
		if f.rows[i].Name == x.Name {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return errors.New("no such session")
}

func (f *fakeSvc) snapshot() (starts []agent.StartOpts, kills []agent.KillOpts, rows []agent.Row) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agent.StartOpts(nil), f.starts...), append([]agent.KillOpts(nil), f.kills...), append([]agent.Row(nil), f.rows...)
}

func testConfig() *config.Config {
	cfg := config.Default()
	cfg.Projects = []config.Project{
		{Name: "forge", Repo: "daan/forge", Path: "/code/forge"},
		{Name: "other", Repo: "daan/other", Path: "/code/other"},
	}
	return cfg
}

func newTab(svc agent.API) *Tab {
	keys := ui.DefaultKeyMap()
	tab := New(tabkit.Deps{Theme: ui.NewTheme(true, nil), Keys: keys, Config: testConfig(), Sessions: svc})
	tab.SetProject(&tab.cfg.Projects[0])
	tab.SetSize(120, 30)
	return tab
}

// host runs the tab as a program, standing in for the root model. It keeps
// the last full frame and every toast so tests can wait on them (the
// terminal output only carries changed cells, so it can't be matched).
type host struct {
	tab *Tab

	mu     sync.Mutex
	frame  string
	toasts []string
}

func (h *host) Init() tea.Cmd { return tea.Batch(h.tab.Init(), h.tab.SetVisible(true)) }

func (h *host) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	toast := ""
	switch msg := msg.(type) {
	case ui.SuccessMsg:
		toast = "ok: " + msg.Text
	case ui.InfoMsg:
		toast = "info: " + msg.Text
	case ui.ErrMsg:
		toast = "err: " + msg.Err.Error()
	case tea.WindowSizeMsg:
		return h, nil
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return h, tea.Quit
		}
	}
	if toast != "" {
		h.mu.Lock()
		h.toasts = append(h.toasts, toast)
		h.mu.Unlock()
		return h, nil
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

// screen drives a host under teatest.
type screen struct {
	tm *teatest.TestModel
	h  *host
}

func newScreen(t *testing.T, h *host) *screen {
	return &screen{tm: teatest.NewTestModel(t, h, teatest.WithInitialTermSize(120, 32)), h: h}
}

func (s *screen) until(t *testing.T, what string, cond func(frame string, toasts []string) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.h.mu.Lock()
		frame, toasts := s.h.frame, append([]string(nil), s.h.toasts...)
		s.h.mu.Unlock()
		if cond(frame, toasts) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s\nframe:\n%s\ntoasts: %q", what, frame, toasts)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// wait blocks until the current frame contains every want.
func (s *screen) wait(t *testing.T, want ...string) {
	t.Helper()
	s.until(t, strings.Join(want, " + "), func(frame string, _ []string) bool {
		for _, w := range want {
			if !strings.Contains(frame, w) {
				return false
			}
		}
		return true
	})
}

// toast blocks until a toast starting with want was shown.
func (s *screen) toast(t *testing.T, want string) {
	t.Helper()
	s.until(t, "toast "+want, func(_ string, toasts []string) bool {
		return slices.ContainsFunc(toasts, func(x string) bool { return strings.HasPrefix(x, want) })
	})
}

func press(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

func TestNewSessionFlow(t *testing.T) {
	svc := newFakeSvc()
	tab := newTab(svc)
	sc := newScreen(t, &host{tab: tab})
	tm := sc.tm
	sc.wait(t, "no sessions yet")

	tm.Send(press("n"))
	sc.wait(t, "New session", "First prompt")
	tm.Type("fix-ci")
	tm.Send(press("enter")) // name
	tm.Send(press("enter")) // project: the active one
	tm.Send(press("enter")) // base: main
	// Huh moves focus with a command round-trip; keys typed before the
	// prompt field has focus would land elsewhere.
	time.Sleep(200 * time.Millisecond)
	tm.Type("hello")
	tm.Send(press("enter")) // prompt: submit
	sc.toast(t, "ok: session fix-ci started")
	sc.wait(t, "claude ready> hello")

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	h := tm.FinalModel(t, teatest.WithFinalTimeout(3*time.Second)).(*host)
	starts, _, _ := svc.snapshot()
	want := agent.StartOpts{Name: "fix-ci", Project: "forge", Base: "main", Prompt: "hello"}
	if len(starts) != 1 || starts[0] != want {
		t.Fatalf("starts = %+v, want %+v", starts, want)
	}
	if r, ok := h.tab.current(); !ok || r.Name != "fix-ci" {
		t.Errorf("selected %+v, want the new session", r)
	}
	if frame := h.frame; !strings.Contains(frame, "Name") || !strings.Contains(frame, "fix-ci   ") {
		t.Errorf("name column missing:\n%s", frame)
	}
}

func TestNewSessionFormValidatesName(t *testing.T) {
	svc := newFakeSvc(row("taken", store.StatusIdle))
	sc := newScreen(t, &host{tab: newTab(svc)})
	tm := sc.tm
	sc.wait(t, "taken")
	tm.Send(press("n"))
	sc.wait(t, "New session")
	tm.Type("taken")
	tm.Send(press("enter"))
	sc.wait(t, "a session called taken already exists")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	sc.wait(t, "agent/taken") // back on the list
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	if starts, _, _ := svc.snapshot(); len(starts) != 0 {
		t.Fatalf("started: %+v", starts)
	}
}

func TestKillArchiveDeleteFlow(t *testing.T) {
	svc := newFakeSvc(row("alpha", store.StatusWorking))
	svc.output["alpha"] = "working on it"
	sc := newScreen(t, &host{tab: newTab(svc)})
	tm := sc.tm
	sc.wait(t, "alpha", "working on it")

	tm.Send(press("x"))
	sc.wait(t, "Kill session alpha?")
	tm.Send(press("down")) // kill and remove worktree
	tm.Send(press("enter"))
	sc.toast(t, "ok: alpha archived")
	sc.wait(t, "no sessions yet")

	tm.Send(press("a"))
	sc.wait(t, "alpha", "no tmux window")
	tm.Send(press("D"))
	sc.wait(t, "Delete session alpha for good?")
	tm.Send(press("y"))
	sc.toast(t, "ok: deleted alpha")

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	_, kills, rows := svc.snapshot()
	if len(kills) != 1 || !kills[0].RemoveWorktree || kills[0].Force {
		t.Errorf("kills = %+v", kills)
	}
	if len(rows) != 0 {
		t.Errorf("rows = %+v", rows)
	}
}

func TestKillDirtyAsksToForce(t *testing.T) {
	svc := newFakeSvc(row("dirty", store.StatusIdle))
	svc.dirty = true
	sc := newScreen(t, &host{tab: newTab(svc)})
	tm := sc.tm
	sc.wait(t, "dirty")
	tm.Send(press("x"))
	sc.wait(t, "Kill session dirty?")
	tm.Send(press("down"))
	tm.Send(press("enter"))
	sc.wait(t, "2 uncommitted change(s)")
	tm.Send(press("y"))
	sc.toast(t, "ok: dirty archived")
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	if _, kills, _ := svc.snapshot(); len(kills) != 2 || !kills[1].Force {
		t.Errorf("kills = %+v", kills)
	}
}

func TestDeleteOnlyInArchivedView(t *testing.T) {
	tab := newTab(newFakeSvc(row("alpha", store.StatusIdle)))
	tab.Update(tab.Refresh()())
	_, cmd := tab.Update(press("D"))
	if tab.dialog != nil {
		t.Fatal("delete dialog opened in the active view")
	}
	if msg, ok := cmd().(ui.InfoMsg); !ok || !strings.Contains(msg.Text, "only archived") {
		t.Errorf("msg = %#v", cmd())
	}
}

// tickRecorder replaces tea.Tick.
type tickRecorder struct{ ds []time.Duration }

func (r *tickRecorder) tick(d time.Duration, f func(time.Time) tea.Msg) tea.Cmd {
	r.ds = append(r.ds, d)
	return func() tea.Msg { return f(time.Time{}) }
}

func TestPreviewTicksOnlyWhileVisibleAndLive(t *testing.T) {
	svc := newFakeSvc(row("live", store.StatusWorking), row("dead", store.StatusDead))
	svc.output["live"] = "line1\nline2"
	tab := newTab(svc)
	rec := &tickRecorder{}
	tab.tick = rec.tick
	tab.Update(tab.Refresh()())

	// Hidden: no capture at all.
	if cmd := tab.selectionChanged(); cmd != nil {
		t.Fatal("captured while hidden")
	}
	// Shown: capture now, then schedule the next one 2s out.
	capture := tab.SetVisible(true)
	if capture == nil {
		t.Fatal("no capture on show")
	}
	_, next := tab.Update(capture())
	if next == nil || len(rec.ds) != 1 || rec.ds[0] != PreviewInterval {
		t.Fatalf("ticks = %v", rec.ds)
	}
	if !strings.Contains(ansi.Strip(tab.View()), "line2") {
		t.Errorf("preview not shown:\n%s", ansi.Strip(tab.View()))
	}
	tickMsg := next()
	_, again := tab.Update(tickMsg) // the tick captures again
	if again == nil {
		t.Fatal("tick did not capture")
	}
	pending := again()

	// Hidden again: the in-flight capture and its tick are dropped.
	tab.SetVisible(false)
	if _, cmd := tab.Update(pending); cmd != nil {
		t.Error("stale capture rescheduled")
	}
	if _, cmd := tab.Update(tickMsg); cmd != nil {
		t.Error("stale tick captured")
	}

	// A dead session is captured once and not polled.
	tab.SetVisible(true)
	_, cmd := tab.Update(press("j"))
	if r, _ := tab.current(); r.Name != "dead" {
		t.Fatalf("selected %q", r.Name)
	}
	ticks := len(rec.ds)
	if cmd == nil {
		t.Fatal("no capture for new selection")
	}
	if _, next := tab.Update(cmd()); next != nil || len(rec.ds) != ticks {
		t.Error("dead session is polled")
	}
	// Run: on show, on the tick (dropped in flight), and for the dead one.
	// (The capture SetVisible returned the second time was never run.)
	if svc.previews != 3 {
		t.Errorf("previews = %d, want 3", svc.previews)
	}
}

func TestNoServiceExplains(t *testing.T) {
	tab := newTab(nil)
	if cmd := tab.Refresh(); cmd != nil {
		t.Error("refresh without service")
	}
	if !strings.Contains(ansi.Strip(tab.View()), "unavailable") {
		t.Errorf("view:\n%s", ansi.Strip(tab.View()))
	}
	_, cmd := tab.Update(press("n"))
	if msg, ok := cmd().(ui.ErrMsg); !ok || !errors.Is(msg.Err, errUnavailable) {
		t.Errorf("n = %#v", cmd())
	}
}

func TestAgo(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "now"},
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{3 * time.Hour, "3h"},
		{72 * time.Hour, "3d"},
	}
	for _, tt := range tests {
		if got := Ago(now, now.Add(-tt.d)); got != tt.want {
			t.Errorf("Ago(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
	if Ago(now, time.Time{}) != "—" {
		t.Error("zero time")
	}
}
