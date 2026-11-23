package home

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
)

var now = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

// --- fakes -------------------------------------------------------------------

type fakeSessions struct {
	sessions.API
	rows     []sessions.Row
	err      error
	projects []string // List calls
}

func (f *fakeSessions) List(_ context.Context, project string, _ bool) ([]sessions.Row, error) {
	f.projects = append(f.projects, project)
	return f.rows, f.err
}

type fakeGitHub struct {
	github.PRClient
	reviews, mine []github.PR
	err           error
	mu            sync.Mutex
	queries       []string
}

func (f *fakeGitHub) SearchPRs(_ context.Context, q string, _ int) (github.SearchResult, error) {
	f.mu.Lock()
	f.queries = append(f.queries, q)
	f.mu.Unlock()
	if f.err != nil {
		return github.SearchResult{}, f.err
	}
	if strings.Contains(q, "review-requested:@me") {
		return github.SearchResult{PRs: f.reviews}, nil
	}
	return github.SearchResult{PRs: f.mine}, nil
}

type fakePlane struct {
	plane.API
	items   []plane.WorkItem
	noCycle bool
	filters []plane.WorkItemFilter
}

func (*fakePlane) Me(context.Context) (plane.User, error) { return plane.User{ID: "u-me"}, nil }

func (f *fakePlane) CurrentCycle(context.Context, string) (plane.Cycle, bool, error) {
	return plane.Cycle{ID: "c1"}, !f.noCycle, nil
}

func (f *fakePlane) WorkItems(_ context.Context, _ string, flt plane.WorkItemFilter) ([]plane.WorkItem, error) {
	f.filters = append(f.filters, flt)
	return f.items, nil
}

type fakeArtifacts struct {
	tabs.ArtifactIndex
	list []store.Artifact
}

func (f *fakeArtifacts) List(context.Context, string) ([]store.Artifact, error) { return f.list, nil }

// --- harness -------------------------------------------------------------------

type fixture struct {
	sess  *fakeSessions
	gh    *fakeGitHub
	pl    *fakePlane
	arts  *fakeArtifacts
	notes string
	cfg   *config.Config
}

func newFixture(t *testing.T) *fixture {
	cfg := config.Default()
	cfg.Projects = []config.Project{{Name: "forge", Repo: "daan/forge", PlaneProjectID: "pp1"}}
	cfg.Plane.BaseURL, cfg.Plane.Workspace = "https://plane.example", "ws"
	return &fixture{
		sess: &fakeSessions{}, gh: &fakeGitHub{}, pl: &fakePlane{}, arts: &fakeArtifacts{},
		notes: t.TempDir(), cfg: cfg,
	}
}

func (f *fixture) tab() *Tab {
	d := tabkit.Deps{
		Theme: ui.NewTheme(true, nil), Keys: ui.DefaultKeyMap(), Config: f.cfg,
		Sessions: f.sess, GitHub: f.gh, Plane: f.pl, Artifacts: f.arts, NotesDir: f.notes,
	}
	t := newTab(d)
	t.now = func() time.Time { return now }
	t.SetProject(&f.cfg.Projects[0])
	t.SetSize(140, 24)
	return t
}

func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, runCmd(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

// pump runs cmd and feeds its messages back until none are left.
func pump(t *Tab, cmd tea.Cmd) []tea.Msg {
	var out []tea.Msg
	queue := runCmd(cmd)
	for len(queue) > 0 {
		msg := queue[0]
		queue = queue[1:]
		out = append(out, msg)
		_, next := t.Update(msg)
		queue = append(queue, runCmd(next)...)
	}
	return out
}

func view(t *Tab) string { return ansi.Strip(t.View()) }

func press(t *Tab, s string) []tea.Msg {
	var k tea.KeyPressMsg
	switch s {
	case "enter":
		k = tea.KeyPressMsg{Code: tea.KeyEnter}
	case "down":
		k = tea.KeyPressMsg{Code: tea.KeyDown}
	default:
		k = tea.KeyPressMsg{Code: rune(s[0]), Text: s}
	}
	_, cmd := t.Update(k)
	return runCmd(cmd)
}

func (f *fixture) full() {
	f.sess.rows = []sessions.Row{
		{Session: store.Session{Name: "fix-login", Status: store.StatusWaiting, StatusChangedAt: now.Add(-3 * time.Minute)}},
		{Session: store.Session{Name: "docs-sync", Status: store.StatusWorking}},
	}
	f.gh.reviews = []github.PR{{Repo: "daan/forge", Number: 212, Title: "Add webhook retry", State: github.StateOpen, URL: "https://gh/212"}}
	f.gh.mine = []github.PR{
		{Repo: "daan/forge", Number: 198, Title: "Lint config", State: github.StateOpen, CI: github.CIFailure},
		{Repo: "daan/forge", Number: 190, Title: "Bump deps", State: github.StateOpen, CI: github.CISuccess, ReviewDecision: github.ReviewApproved},
	}
	f.pl.items = []plane.WorkItem{{ID: "uuid-41", Identifier: "PROJ-41", Name: "Rate limiter", State: &plane.State{Name: "In Progress", Group: plane.GroupStarted}}}
	f.arts.list = []store.Artifact{{Path: "/x/arch.svg", Pinned: true}, {Path: "/x/other.png"}}
}

// --- tests -------------------------------------------------------------------

func TestHomeListsEverythingInOrder(t *testing.T) {
	f := newFixture(t)
	f.full()
	if err := os.WriteFile(filepath.Join(f.notes, "2026-11-23.md"), []byte("- follow up on #198\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tab := f.tab()
	pump(tab, tab.Init())

	if got := ids(tab.list); strings.Join(got, ",") != "agent:fix-login,pr:daan/forge#212,pr:daan/forge#198,pr:daan/forge#190,plane:uuid-41" {
		t.Fatalf("list = %v", got)
	}
	v := view(tab)
	for _, want := range []string{
		"fix-login", "waiting 3m", "enter attach",
		"#212 Add webhook retry", "enter diff",
		"#198 Lint config", "CI failing", "F fix",
		"#190 Bump deps", "m merge",
		"PROJ-41 Rate limiter", "In Progress", "enter open",
		"Agents 2", "docs-sync", "Pinned 1", "arch.svg", "Today", "follow up on #198",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "other.png") {
		t.Error("unpinned artifact on the side panel")
	}
	if got := tab.StatusDetail(); got != "5 need you" {
		t.Errorf("status detail = %q", got)
	}
	// The searches are limited to the project's repo; Plane asks for my
	// items in the current cycle.
	for _, q := range f.gh.queries {
		if !strings.Contains(q, "repo:daan/forge") {
			t.Errorf("query %q isn't limited to the repo", q)
		}
	}
	if len(f.pl.filters) != 1 || f.pl.filters[0].CycleID != "c1" || f.pl.filters[0].AssigneeIDs[0] != "u-me" {
		t.Errorf("plane filters = %+v", f.pl.filters)
	}
}

func TestHomeColdForge(t *testing.T) {
	f := newFixture(t)
	tab := f.tab()
	if v := view(tab); !strings.Contains(v, "heating up") {
		t.Errorf("before loading:\n%s", v)
	}
	pump(tab, tab.Init())
	v := view(tab)
	if !strings.Contains(v, coldCaption) || !strings.Contains(v, "no note for today") || !strings.Contains(v, "nothing pinned") {
		t.Errorf("empty state:\n%s", v)
	}
	if got := tab.StatusDetail(); got != "all clear" {
		t.Errorf("status detail = %q", got)
	}
}

func TestHomeOneSourceFailing(t *testing.T) {
	f := newFixture(t)
	f.full()
	f.gh.err = errors.New("boom")
	tab := f.tab()
	pump(tab, tab.Init())
	v := view(tab)
	if !strings.Contains(v, "GitHub: boom") {
		t.Errorf("no GitHub problem line:\n%s", v)
	}
	if !strings.Contains(v, "fix-login") || !strings.Contains(v, "PROJ-41") {
		t.Errorf("other sources should still show:\n%s", v)
	}
	if strings.Contains(v, coldCaption) {
		t.Error("cold forge while items show")
	}
}

// An empty list with a failing source doesn't claim all is clear.
func TestHomeEmptyButFailing(t *testing.T) {
	f := newFixture(t)
	f.gh.err = errors.New("boom")
	tab := f.tab()
	pump(tab, tab.Init())
	v := view(tab)
	if strings.Contains(v, coldCaption) || !strings.Contains(v, partCaption) {
		t.Errorf("empty state with a failing source:\n%s", v)
	}
	if got := tab.StatusDetail(); got == "all clear" {
		t.Errorf("status detail = %q", got)
	}
}

func TestHomeNoCycleListsNoIssues(t *testing.T) {
	f := newFixture(t)
	f.full()
	f.pl.noCycle = true
	tab := f.tab()
	pump(tab, tab.Init())
	if strings.Contains(view(tab), "PROJ-41") || len(f.pl.filters) != 0 {
		t.Error("items listed without a current cycle")
	}
}

func TestHomeDropsLoadsForAnOldProject(t *testing.T) {
	f := newFixture(t)
	f.full()
	tab := f.tab()
	msgs := runCmd(tab.loadSessions())
	tab.SetProject(nil) // switched before the load landed
	for _, m := range msgs {
		tab.Update(m)
	}
	if len(tab.rows) != 0 {
		t.Error("a load for the old project was applied")
	}
	pump(tab, tab.Refresh())
	if f.sess.projects[len(f.sess.projects)-1] != "" || len(tab.rows) == 0 {
		t.Errorf("all projects should list every session: %v", f.sess.projects)
	}
}

func TestHomeActions(t *testing.T) {
	f := newFixture(t)
	f.full()
	tab := f.tab()
	pump(tab, tab.Init())

	want := func(msgs []tea.Msg, check func(tea.Msg) bool, what string) {
		t.Helper()
		if len(msgs) != 1 || !check(msgs[0]) {
			t.Errorf("%s: got %#v", what, msgs)
		}
	}
	// Row 1: the waiting agent.
	want(press(tab, "enter"), func(m tea.Msg) bool { return m == tabs.AttachSessionMsg{Name: "fix-login"} }, "enter on agent")
	want(press(tab, "F"), isInfo, "F on agent")
	// Row 2: the review request.
	press(tab, "down")
	want(press(tab, "enter"), isPRAction(212, tabs.PRDiff), "enter on review")
	// Row 3: my failing PR.
	press(tab, "down")
	want(press(tab, "F"), isPRAction(198, tabs.PRFix), "F on failing PR")
	want(press(tab, "m"), isInfo, "m on failing PR")
	// Row 4: approved and green.
	press(tab, "down")
	want(press(tab, "m"), isPRAction(190, tabs.PRMerge), "m on green PR")
	// Row 5: the Plane item.
	press(tab, "down")
	want(press(tab, "enter"), func(m tea.Msg) bool {
		return m == tabs.SelectEntityMsg{Tab: "Plane", ID: "uuid-41"}
	}, "enter on issue")
}

func isInfo(m tea.Msg) bool { _, ok := m.(ui.InfoMsg); return ok }

func isPRAction(n int, a tabs.PRAction) func(tea.Msg) bool {
	return func(m tea.Msg) bool {
		pa, ok := m.(tabs.PRActionMsg)
		return ok && pa.PR.Number == n && pa.Action == a
	}
}

func TestHomeKeepsCursorOnRerank(t *testing.T) {
	f := newFixture(t)
	f.full()
	tab := f.tab()
	pump(tab, tab.Init())
	press(tab, "down") // the review
	f.sess.rows = nil  // the agent stopped waiting
	pump(tab, tab.SessionsChanged())
	if it, ok := tab.selected(); !ok || it.id != "pr:daan/forge#212" {
		t.Errorf("selected = %v", it.id)
	}
}

func TestHomeCommandsCoverKeys(t *testing.T) {
	tab := newFixture(t).tab()
	got := map[string]bool{}
	for _, c := range tab.Commands() {
		got[c.ID] = true
	}
	for _, id := range []string{"home_open", "home_fix", "home_merge", "home_browse"} {
		if !got[id] {
			t.Errorf("no palette command %s", id)
		}
	}
}

// --- teatest flow ----------------------------------------------------------------

type host struct {
	tab  *Tab
	mu   sync.Mutex
	sent []tea.Msg // cross-tab messages Home asked for
	text string
}

func (h *host) Init() tea.Cmd { return h.tab.Init() }

func (h *host) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return h, nil
	case tabs.AttachSessionMsg, tabs.PRActionMsg:
		h.mu.Lock()
		h.sent = append(h.sent, msg)
		h.mu.Unlock()
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
	h.text = ansi.Strip(v)
	h.mu.Unlock()
	return tea.NewView(v)
}

func (h *host) wait(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			h.mu.Lock()
			defer h.mu.Unlock()
			t.Fatalf("timed out; sent %v\n%s", h.sent, h.text)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestHomeFlow: open forge, see what to do next, and do it with one key.
func TestHomeFlow(t *testing.T) {
	f := newFixture(t)
	f.full()
	h := &host{tab: f.tab()}
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(140, 24))
	h.wait(t, func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return strings.Contains(h.text, "fix-login") && strings.Contains(h.text, "PROJ-41") &&
			strings.Contains(h.text, "#212") && strings.Contains(h.text, "#190")
	})
	// The sources load in any order and the cursor follows its row across
	// reranks, so go to the top before acting.
	tm.Send(tea.KeyPressMsg{Code: tea.KeyHome})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	tm.Send(tea.KeyPressMsg{Code: 'F', Text: "F"})
	h.wait(t, func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return len(h.sent) == 2
	})
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))
	if h.sent[0] != (tabs.AttachSessionMsg{Name: "fix-login"}) || !isPRAction(198, tabs.PRFix)(h.sent[1]) {
		t.Errorf("sent = %#v", h.sent)
	}
}
