package sessions

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/config"
	docfs "github.com/daan3011/forge/internal/docs"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/sessions/transcript"
	"github.com/daan3011/forge/internal/sessions/worktree"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
)

// fakeSvc is an in-memory agent.API.
type fakeSvc struct {
	mu       sync.Mutex
	rows     []agent.Row
	listErr  error // every List fails with it
	output   map[string]string
	starts   []agent.StartOpts
	kills    []agent.KillOpts
	previews int
	dirty    bool
	clears   []string // ClearWaiting calls

	windows  []tmux.Target
	restarts int
	conf     string
	resets   int

	// attach & resume
	plans     map[string]agent.ResumePlan
	inspects  []string
	resumes   []agent.ResumeOpts
	attaches  []string
	attachAs  tmux.Attachment
	resumeErr error // returned once by Resume

	// links (P1.9)
	startErr error                 // returned once by Start
	pulls    map[string]store.Link // branch -> PR SyncPRs links
	syncs    int

	// fix this (P2.8)
	sent    map[string][]string // SendPrompt texts by session name
	sendErr error               // returned once by SendPrompt

	// send docs (P4.3)
	attached  map[string][]docfs.Attachment // AttachDocs by session name
	attachErr error                         // returned once by AttachDocs
	attachOut []docfs.Attachment            // AttachDocs result; nil echoes the input

	// transcripts by session name; a missing name means no transcript.
	transcripts     map[string]transcript.Transcript
	transcriptLoads int
}

// AttachDocs records the docs and returns attachOut when set, else the
// docs as given; attachErr is returned once.
func (f *fakeSvc) AttachDocs(_ context.Context, x store.Session, docs []docfs.Attachment) ([]docfs.Attachment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.attachErr; err != nil {
		f.attachErr = nil
		return nil, err
	}
	if f.attached == nil {
		f.attached = map[string][]docfs.Attachment{}
	}
	f.attached[x.Name] = append(f.attached[x.Name], docs...)
	if f.attachOut != nil {
		return f.attachOut, nil
	}
	return docs, nil
}

func (f *fakeSvc) Transcript(_ context.Context, x store.Session) (transcript.Transcript, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.transcriptLoads++
	tr, ok := f.transcripts[x.Name]
	if !ok {
		return transcript.Transcript{}, transcript.ErrNoTranscript
	}
	return tr, nil
}

func (f *fakeSvc) setTranscript(name string, tr transcript.Transcript) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.transcripts == nil {
		f.transcripts = map[string]transcript.Transcript{}
	}
	f.transcripts[name] = tr
}

func (f *fakeSvc) TmuxWindows(context.Context) ([]tmux.Target, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.windows), nil
}

func (f *fakeSvc) RestartTmux(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarts++
	f.windows = nil
	return nil
}

func (f *fakeSvc) TmuxConfig() string { return f.conf }

func (f *fakeSvc) ResetTmuxConfig() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resets++
	return f.conf + ".bak.20261120-140509", nil
}

func (f *fakeSvc) OpenWindow(context.Context, agent.WindowOpts) (agent.WindowResult, error) {
	return agent.WindowResult{}, errors.New("fakeSvc: OpenWindow not supported")
}

func (f *fakeSvc) tmuxCounts() (restarts, resets int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.restarts, f.resets
}

func newFakeSvc(rows ...agent.Row) *fakeSvc {
	return &fakeSvc{
		rows: rows, output: map[string]string{}, plans: map[string]agent.ResumePlan{},
		attachAs: tmux.Attachment{Mode: tmux.AttachSwitched},
	}
}

func (f *fakeSvc) Inspect(_ context.Context, x store.Session) (agent.ResumePlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inspects = append(f.inspects, x.Name)
	return f.plans[x.Name], nil
}

func (f *fakeSvc) Resume(_ context.Context, x store.Session, o agent.ResumeOpts) (agent.Resumed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumes = append(f.resumes, o)
	if err := f.resumeErr; err != nil {
		f.resumeErr = nil
		return agent.Resumed{}, err
	}
	p := f.plans[x.Name]
	res := agent.Resumed{Fresh: !p.HasTranscript, WorktreeRecreated: o.RecreateWorktree}
	for i := range f.rows {
		if f.rows[i].Name == x.Name {
			f.rows[i].Status = store.StatusStarting
			res.Session = f.rows[i].Session
		}
	}
	p.Alive = true
	f.plans[x.Name] = p
	return res, nil
}

func (f *fakeSvc) Attach(_ context.Context, x store.Session) (tmux.Attachment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attaches = append(f.attaches, x.Name)
	return f.attachAs, nil
}

func (f *fakeSvc) attachCalls() (inspects []string, resumes []agent.ResumeOpts, attaches []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.inspects), slices.Clone(f.resumes), slices.Clone(f.attaches)
}

func row(name string, st store.Status) agent.Row {
	r := agent.Row{Session: store.Session{
		ID: int64(len(name)), Name: name, Project: "forge", Branch: "agent/" + name, BaseBranch: "main",
		Worktree: "/wt/" + name, TmuxSession: "forge", TmuxWindow: name, Status: st,
		ClaudeSessionID: "uuid-" + name,
	}}
	if st == store.StatusArchived { // worktree removed, as Kill leaves it
		r.Worktree, r.TmuxWindow, r.ArchivedAt = "", "", archiveAt
	}
	return r
}

func (f *fakeSvc) Start(_ context.Context, o agent.StartOpts) (agent.Started, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, o)
	if err := f.startErr; err != nil {
		f.startErr = nil
		return agent.Started{}, err
	}
	r := row(o.Name, store.StatusStarting)
	r.Project, r.Template, r.BranchCreated = o.Project, o.Template, true
	if o.Branch != "" {
		r.Branch, r.BranchCreated = o.Branch, false
	}
	if o.Worktree != "" {
		r.Worktree, r.WorktreeExternal = o.Worktree, true
	}
	for _, l := range o.Links {
		r.LinkList = append(r.LinkList, store.Link{Other: l.To, LinkMeta: l.Meta})
	}
	f.rows = append(f.rows, r)
	f.output[o.Name] = "claude ready> " + o.Prompt
	return agent.Started{Session: r.Session}, nil
}

func (f *fakeSvc) List(_ context.Context, project string, archived bool) ([]agent.Row, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []agent.Row
	for _, r := range f.rows {
		inArchive := r.Archived() || r.Status == store.StatusArchived
		if (project == "" || r.Project == project) && inArchive == archived {
			// Copies, as the real service reads fresh rows from the
			// database: SyncPRs edits f.rows' links in place.
			r.LinkList, r.Links = slices.Clone(r.LinkList), slices.Clone(r.Links)
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
		if !f.rows[i].Archived() {
			f.archiveLocked(i) // the real Kill writes a summary too
		}
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

func (f *fakeSvc) ClearWaiting(_ context.Context, x store.Session) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clears = append(f.clears, x.Name)
	for i := range f.rows {
		if f.rows[i].Name == x.Name && f.rows[i].Status == store.StatusWaiting {
			f.rows[i].Status = store.StatusWorking
			return true, nil
		}
	}
	return false, nil
}

// setStatus changes a row's status, as a hook writing the database would.
func (f *fakeSvc) setStatus(name string, st store.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.rows {
		if f.rows[i].Name == name {
			f.rows[i].Status = st
		}
	}
}

func (f *fakeSvc) clearCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.clears)
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
	case runMsg:
		return h, msg()
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

// lastFrame is the last rendered frame; View writes it from the program's
// goroutine, so tests read it under the lock.
func (h *host) lastFrame() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.frame
}

func (h *host) View() tea.View {
	v := h.tab.View()
	h.mu.Lock()
	h.frame = ansi.Strip(v)
	h.mu.Unlock()
	return tea.NewView(v)
}

// runMsg makes the host run a command, standing in for the palette.
type runMsg func() tea.Cmd

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
	tm.Send(press("enter")) // branch: empty = agent/<name>
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
	if len(starts) != 1 || !reflect.DeepEqual(starts[0], want) {
		t.Fatalf("starts = %+v, want %+v", starts, want)
	}
	if r, ok := h.tab.current(); !ok || r.Name != "fix-ci" {
		t.Errorf("selected %+v, want the new session", r)
	}
	if frame := h.lastFrame(); !strings.Contains(frame, "Name") || !strings.Contains(frame, "fix-ci   ") {
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
	sc.wait(t, "alpha", "── summary", "summary of alpha")
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

func tmuxCommand(t *testing.T, tab *Tab, id string) func() tea.Cmd {
	t.Helper()
	for _, c := range tab.Commands() {
		if c.ID == id {
			return c.Run
		}
	}
	t.Fatalf("no palette command %q", id)
	return nil
}

func TestTmuxRestartAsksWhenAgentsRun(t *testing.T) {
	svc := newFakeSvc(row("alpha", store.StatusWorking))
	svc.conf = "/cfg/forge/tmux.conf"
	svc.windows = []tmux.Target{{Session: "forge", Window: "alpha"}, {Session: "forge", Window: "beta"}}
	tab := newTab(svc)
	sc := newScreen(t, &host{tab: tab})
	tm := sc.tm
	sc.wait(t, "alpha")

	tm.Send(runMsg(tmuxCommand(t, tab, "tmux_restart")))
	sc.wait(t, "Restart forge's tmux server?", "alpha, beta")
	tm.Send(press("n"))
	sc.until(t, "dialog to close", func(frame string, _ []string) bool {
		return !strings.Contains(frame, "Restart forge's tmux server?")
	})
	tm.Send(runMsg(tmuxCommand(t, tab, "tmux_restart")))
	sc.wait(t, "Restart forge's tmux server?")
	tm.Send(press("y"))
	sc.toast(t, "ok: forge tmux server restarted — stopped sessions resume")

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	if restarts, _ := svc.tmuxCounts(); restarts != 1 {
		t.Errorf("restarts = %d, want 1 (declined once)", restarts)
	}
}

func TestTmuxRestartWithoutAgentsJustRestarts(t *testing.T) {
	svc := newFakeSvc()
	svc.conf = "/cfg/forge/tmux.conf"
	tab := newTab(svc)
	tab.Update(tab.Refresh()())
	msg := tmuxCommand(t, tab, "tmux_restart")()()
	_, cmd := tab.Update(msg)
	if tab.dialog != nil {
		t.Fatal("asked although no agent runs")
	}
	_, cmd = tab.Update(cmd())
	if restarts, _ := svc.tmuxCounts(); restarts != 1 {
		t.Errorf("restarts = %d", restarts)
	}
	found := false
	for _, m := range drain(cmd) {
		if s, ok := m.(ui.SuccessMsg); ok && strings.Contains(s.Text, "/cfg/forge/tmux.conf") {
			found = true
		}
	}
	if !found {
		t.Error("no success toast naming the config")
	}
}

func TestTmuxEditConfigOffersRestart(t *testing.T) {
	svc := newFakeSvc(row("alpha", store.StatusWorking))
	svc.conf = "/cfg/forge/tmux.conf"
	svc.windows = []tmux.Target{{Session: "forge", Window: "alpha"}}
	tab := newTab(svc)
	if cmd := tmuxCommand(t, tab, "tmux_config")(); cmd == nil {
		t.Fatal("edit should exec the editor")
	}
	sc := newScreen(t, &host{tab: tab})
	tm := sc.tm
	sc.wait(t, "alpha")
	// tea.ExecProcess reports back with tmuxEditedMsg once the editor exits.
	tm.Send(tmuxEditedMsg{})
	sc.wait(t, "to apply the new config?")
	tm.Send(press("n"))
	sc.toast(t, "info: new tmux config applies after a restart")
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	if restarts, _ := svc.tmuxCounts(); restarts != 0 {
		t.Errorf("restarts = %d", restarts)
	}
}

func TestTmuxResetConfig(t *testing.T) {
	svc := newFakeSvc(row("alpha", store.StatusWorking))
	svc.conf = "/cfg/forge/tmux.conf"
	tab := newTab(svc)
	sc := newScreen(t, &host{tab: tab})
	tm := sc.tm
	sc.wait(t, "alpha")
	tm.Send(runMsg(tmuxCommand(t, tab, "tmux_config_reset")))
	sc.wait(t, "Reset forge's tmux config to the default?")
	tm.Send(press("y"))
	sc.toast(t, "info: tmux config reset to forge's default — old file kept as tmux.conf.bak.20261120-140509")
	// No agents run: the server restarts without asking.
	sc.toast(t, "ok: tmux config saved")
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	if restarts, resets := svc.tmuxCounts(); restarts != 1 || resets != 1 {
		t.Errorf("restarts, resets = %d, %d", restarts, resets)
	}
}

// drain runs cmd and any batch it returns, collecting the messages.
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

// TestListLoadStates (P6.4): before the first list the pane says it is
// loading (not "no agents"), a failed list says so with the retry key,
// and a retry fills it.
func TestListLoadStates(t *testing.T) {
	svc := newFakeSvc(row("alpha", store.StatusIdle))
	svc.listErr = errors.New("database is locked")
	tab := newTab(svc)
	cmd := tab.Refresh()
	v := ansi.Strip(tab.View())
	if !strings.Contains(v, "heating up…") || strings.Contains(v, "no agents yet") {
		t.Fatalf("before the first list:\n%s", v)
	}
	var toasts []string
	for _, m := range drain(cmd) {
		if _, ok := m.(listedMsg); !ok {
			continue
		}
		_, next := tab.Update(m)
		for _, m := range drain(next) {
			if e, ok := m.(ui.ErrMsg); ok {
				toasts = append(toasts, e.Err.Error())
			}
		}
	}
	if len(toasts) != 1 || !strings.Contains(toasts[0], "database is locked · r to retry") {
		t.Fatalf("toasts = %q", toasts)
	}
	v = ansi.Strip(tab.View())
	if !strings.Contains(v, "couldn't list sessions") || !strings.Contains(v, "r to retry") {
		t.Fatalf("after a failed list:\n%s", v)
	}
	if tab.Animating() {
		t.Error("a failed list keeps no clock running")
	}

	svc.mu.Lock()
	svc.listErr = nil
	svc.mu.Unlock()
	for _, m := range drain(tab.Refresh()) {
		if _, ok := m.(listedMsg); ok {
			tab.Update(m)
		}
	}
	v = ansi.Strip(tab.View())
	if !strings.Contains(v, "alpha") || strings.Contains(v, "couldn't list") {
		t.Fatalf("after retry:\n%s", v)
	}

	empty := newTab(newFakeSvc())
	for _, m := range drain(empty.Refresh()) {
		if _, ok := m.(listedMsg); ok {
			empty.Update(m)
		}
	}
	if v := ansi.Strip(empty.View()); !strings.Contains(v, "no agents yet · n starts one") {
		t.Fatalf("no sessions:\n%s", v)
	}
	if v := ansi.Strip(newTab(nil).View()); !strings.Contains(v, "agent sessions aren't available") {
		t.Fatalf("no service:\n%s", v)
	}
}
