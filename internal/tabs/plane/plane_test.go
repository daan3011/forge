package plane

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
)

var now = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

const (
	p1 = "11111111-0000-0000-0000-000000000001" // FORGE, forge project "app"
	p2 = "22222222-0000-0000-0000-000000000002" // LIB, forge project "lib"
)

var (
	me  = plane.User{ID: "u-me", DisplayName: "daan"}
	bob = plane.User{ID: "u-bob", DisplayName: "bob"}

	stTodo  = plane.State{ID: "s-todo", Name: "Todo", Group: plane.GroupUnstarted}
	stDoing = plane.State{ID: "s-doing", Name: "In Progress", Group: plane.GroupStarted}
	stDone  = plane.State{ID: "s-done", Name: "Done", Group: plane.GroupCompleted}

	lbBug = plane.Label{ID: "l-bug", Name: "bug", Color: "#d73a4a"}
	lbUI  = plane.Label{ID: "l-ui", Name: "ui", Color: "#0e8a16"}

	cyc12 = plane.Cycle{
		ID: "c-12", Name: "Sprint 12",
		StartDate: plane.Date{Time: now.Add(-72 * time.Hour)}, EndDate: plane.Date{Time: now.Add(72 * time.Hour)},
	}
)

func item(project, ident string, seq int, name string, st plane.State, prio plane.Priority, who []plane.User, labels []plane.Label, mod func(*plane.WorkItem)) plane.WorkItem {
	s := st
	w := plane.WorkItem{
		ID: fmt.Sprintf("w-%s-%d", ident, seq), Name: name, Identifier: fmt.Sprintf("%s-%d", ident, seq), SequenceID: seq,
		ProjectID: project, StateID: st.ID, State: &s, Priority: prio,
		Assignees: who, Labels: labels,
		CreatedAt: plane.Date{Time: now.Add(-48 * time.Hour)},
		UpdatedAt: plane.Date{Time: now.Add(-time.Duration(seq) * time.Hour)},
	}
	for _, u := range who {
		w.AssigneeIDs = append(w.AssigneeIDs, u.ID)
	}
	for _, l := range labels {
		w.LabelIDs = append(w.LabelIDs, l.ID)
	}
	if mod != nil {
		mod(&w)
	}
	return w
}

// fakePlane serves work items per project, filtered like Plane would, and
// caches lists like the real client. It records every list call.
type fakePlane struct {
	plane.API

	mu       sync.Mutex
	me       plane.User
	meErr    error
	items    map[string][]plane.WorkItem // project -> items
	listErr  error
	cache    map[string][]plane.WorkItem // filter key -> items
	calls    []string
	desc     map[string]string // item id -> description HTML
	comments map[string][]plane.Comment
	detail   []string // item ids fetched

	projectLists int // Projects calls
}

func samplePlane() *fakePlane {
	return &fakePlane{
		me: me,
		items: map[string][]plane.WorkItem{
			p1: {
				item(p1, "FORGE", 1, "Project switcher", stDoing, plane.PriorityHigh, []plane.User{me}, []plane.Label{lbUI}, func(w *plane.WorkItem) { w.CycleID = cyc12.ID }),
				item(p1, "FORGE", 2, "Palette ranking", stTodo, plane.PriorityUrgent, []plane.User{me, bob}, []plane.Label{lbBug, lbUI}, nil),
				item(p1, "FORGE", 3, "Store migrations", stDone, plane.PriorityLow, []plane.User{me}, nil, nil),
				item(p1, "FORGE", 4, "Bob's thing", stTodo, plane.PriorityNone, []plane.User{bob}, nil, nil),
			},
			p2: {
				item(p2, "LIB", 7, "Lib bug", stTodo, plane.PriorityMedium, []plane.User{me}, []plane.Label{lbBug}, nil),
			},
		},
		cache: map[string][]plane.WorkItem{},
		desc: map[string]string{
			"w-FORGE-1": "<h2>Goal</h2><p>Switch projects with <strong>ctrl+p</strong>.</p><ul><li><p>fuzzy</p></li></ul>",
		},
		comments: map[string][]plane.Comment{
			"w-FORGE-1": {
				{ID: "c2", ActorID: bob.ID, CommentHTML: "<p>second word</p>", CreatedAt: plane.Date{Time: now.Add(-time.Hour)}},
				{ID: "c1", ActorID: me.ID, CommentHTML: "<p>first word</p>", CreatedAt: plane.Date{Time: now.Add(-2 * time.Hour)}},
			},
		},
	}
}

func filterKey(project string, f plane.WorkItemFilter) string {
	return fmt.Sprintf("%s assignee=%s groups=%v label=%s cycle=%s",
		project, strings.Join(f.AssigneeIDs, ","), f.StateGroups, strings.Join(f.LabelIDs, ","), f.CycleID)
}

func (f *fakePlane) Me(context.Context) (plane.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.me, f.meErr
}

func (f *fakePlane) WorkItems(_ context.Context, project string, fl plane.WorkItemFilter) ([]plane.WorkItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := filterKey(project, fl)
	f.calls = append(f.calls, k)
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []plane.WorkItem
	for _, w := range f.items[project] {
		if len(fl.AssigneeIDs) > 0 && !slices.ContainsFunc(fl.AssigneeIDs, func(id string) bool { return slices.Contains(w.AssigneeIDs, id) }) {
			continue
		}
		if len(fl.StateGroups) > 0 && !slices.Contains(fl.StateGroups, w.State.Group) {
			continue
		}
		if len(fl.LabelIDs) > 0 && !slices.ContainsFunc(fl.LabelIDs, func(id string) bool { return slices.Contains(w.LabelIDs, id) }) {
			continue
		}
		if fl.CycleID != "" && w.CycleID != fl.CycleID {
			continue
		}
		out = append(out, w)
	}
	f.cache[k] = out
	return out, nil
}

func (f *fakePlane) CachedWorkItems(_ context.Context, project string, fl plane.WorkItemFilter) (plane.Cached[plane.WorkItem], bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items, ok := f.cache[filterKey(project, fl)]
	return plane.Cached[plane.WorkItem]{Items: items, FetchedAt: now.Add(-time.Hour)}, ok
}

func (f *fakePlane) Projects(context.Context) ([]plane.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projectLists++
	return []plane.Project{
		{ID: p2, Identifier: "LIB", Name: "Lib"},
		{ID: p1, Identifier: "FORGE", Name: "Forge"},
	}, nil
}

func (f *fakePlane) Project(_ context.Context, id string) (plane.Project, error) {
	if id == p2 {
		return plane.Project{ID: p2, Identifier: "LIB", Name: "Lib"}, nil
	}
	return plane.Project{ID: p1, Identifier: "FORGE", Name: "Forge"}, nil
}

func (f *fakePlane) States(context.Context, string) ([]plane.State, error) {
	return []plane.State{stTodo, stDoing, stDone}, nil
}

func (f *fakePlane) Labels(context.Context, string) ([]plane.Label, error) {
	return []plane.Label{lbBug, lbUI}, nil
}

func (f *fakePlane) Cycles(context.Context, string) ([]plane.Cycle, error) {
	return []plane.Cycle{cyc12}, nil
}

func (f *fakePlane) ProjectMembers(context.Context, string) ([]plane.Member, error) {
	return []plane.Member{
		{ID: "m1", MemberID: me.ID, Member: &me},
		{ID: "m2", MemberID: bob.ID, Member: &bob},
	}, nil
}

func (f *fakePlane) WorkItem(_ context.Context, project, id string) (plane.WorkItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.detail = append(f.detail, id)
	for _, w := range f.items[project] {
		if w.ID == id {
			w.DescriptionHTML = f.desc[id]
			return w, nil
		}
	}
	return plane.WorkItem{}, &plane.APIError{Status: http.StatusNotFound}
}

func (f *fakePlane) Comments(_ context.Context, _, id string) ([]plane.Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.comments[id], nil
}

func (f *fakePlane) listCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakePlane) setState(project, id string, st plane.State) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, w := range f.items[project] {
		if w.ID == id {
			s := st
			f.items[project][i].State, f.items[project][i].StateID = &s, st.ID
			f.items[project][i].UpdatedAt = plane.Date{Time: now}
		}
	}
}

// fakeSessions answers IssueLinks and records SetIssueStates.
type fakeSessions struct {
	sessions.API

	mu     sync.Mutex
	links  map[string][]store.Session
	prs    map[string][]store.Link
	states []map[string]string
}

func (f *fakeSessions) IssueLinks(_ context.Context, key string) ([]store.Session, []store.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.links[key], f.prs[key], nil
}

func (f *fakeSessions) SetIssueStates(_ context.Context, states map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states = append(f.states, states)
	return nil
}

func testConfig() *config.Config {
	cfg := config.Default()
	cfg.Plane = config.Plane{BaseURL: "https://plane.example", Workspace: "acme"}
	cfg.Projects = []config.Project{
		{Name: "app", Repo: "acme/app", PlaneProjectID: p1},
		{Name: "lib", Repo: "acme/lib", PlaneProjectID: p2},
		{Name: "notes"},
	}
	return cfg
}

type harness struct {
	*Tab
	linkedMsgs []tabs.PlaneLinkedMsg
}

func newHarness(api plane.API, ss sessions.API, project string) *harness {
	return newHarnessWith(api, ss, testConfig(), nil, project)
}

func newHarnessWith(api plane.API, ss sessions.API, cfg *config.Config, linker tabs.ProjectLinker, project string) *harness {
	d := tabkit.Deps{Theme: ui.NewTheme(true, nil), Keys: ui.DefaultKeyMap(), Config: cfg, Sessions: ss, ProjectLinks: linker}
	if api != nil {
		d.Plane = api
	}
	h := &harness{Tab: newTab(d)}
	h.now = func() time.Time { return now }
	h.tick = func(_ time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
		return func() tea.Msg { return fn(now) }
	}
	for i := range cfg.Projects {
		if cfg.Projects[i].Name == project {
			h.SetProject(&cfg.Projects[i])
		}
	}
	if project == "" {
		h.SetProject(nil)
	}
	h.SetSize(160, 36)
	return h
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

// drain feeds cmd's messages back into the tab until none are left and
// returns the error toasts and info messages.
func (h *harness) drain(cmd tea.Cmd) (errs, infos []string) {
	queue := runCmd(cmd)
	for len(queue) > 0 {
		msg := queue[0]
		queue = queue[1:]
		if t := reflect.TypeOf(msg); t != nil && strings.HasSuffix(t.PkgPath(), "bubbles/v2/cursor") {
			continue
		}
		switch m := msg.(type) {
		case ui.ErrMsg:
			errs = append(errs, m.Err.Error())
			continue
		case ui.InfoMsg:
			infos = append(infos, m.Text)
			continue
		case tabs.PlaneLinkedMsg:
			queue = append(queue, runCmd(h.applyLinks(m))...)
			continue
		}
		_, next := h.Update(msg)
		queue = append(queue, runCmd(next)...)
	}
	return errs, infos
}

// applyLinks does what the root model does with a PlaneLinkedMsg: update
// the shared projects and hand the active one to the tab again.
func (h *harness) applyLinks(m tabs.PlaneLinkedMsg) tea.Cmd {
	h.linkedMsgs = append(h.linkedMsgs, m)
	for _, l := range m.Links {
		for i := range h.cfg.Projects {
			if strings.EqualFold(h.cfg.Projects[i].Repo, l.Repo) {
				h.cfg.Projects[i].PlaneProjectID, h.cfg.Projects[i].PlaneDecided = l.PlaneProjectID, true
			}
		}
	}
	h.SetProject(h.project)
	return h.Refresh()
}

func (h *harness) init() []string {
	errs, _ := h.drain(h.Init())
	return errs
}

func press(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func (h *harness) send(keys ...string) []string {
	var errs []string
	for _, k := range keys {
		_, cmd := h.Update(press(k))
		e, _ := h.drain(cmd)
		errs = append(errs, e...)
	}
	return errs
}

func (h *harness) view() string { return ansi.Strip(h.View()) }

func (h *harness) keys() []string {
	var out []string
	for _, r := range h.table.VisibleRows() {
		out = append(out, r[0])
	}
	return out
}

func mustContain(t *testing.T, v string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(v, w) {
			t.Fatalf("view lacks %q:\n%s", w, v)
		}
	}
}

var openKey = "groups=[triage backlog unstarted started]"

func TestDefaultsToMyOpenItems(t *testing.T) {
	api := samplePlane()
	h := newHarness(api, nil, "app")
	if errs := h.init(); len(errs) > 0 {
		t.Fatal(errs)
	}
	if got := api.listCalls(); len(got) != 1 || got[0] != p1+" assignee=u-me "+openKey+" label= cycle=" {
		t.Fatalf("calls = %v", got)
	}
	if got := h.keys(); !slices.Equal(got, []string{"FORGE-1", "FORGE-2"}) {
		t.Fatalf("rows = %v", got)
	}
	mustContain(t, h.view(),
		"FORGE", "assignee: @daan", "state: open", "f filters",
		"FORGE-1", "Project switcher", "In Progress", "▲", "ui",
		"FORGE-2", "‼", "bug, ui", "daan +1", "2h",
	)
	if got := h.StatusDetail(); got != "@daan · issue 1/2" {
		t.Errorf("status = %q", got)
	}
	if !h.LastUpdated().Equal(now) {
		t.Errorf("updated = %v", h.LastUpdated())
	}
}

func TestCachedRowsShowWhenFetchFails(t *testing.T) {
	api := samplePlane()
	h := newHarness(api, nil, "app")
	h.init()
	// A new tab (restart) with Plane failing renders the cached list.
	api.listErr = errors.New("boom")
	h2 := newHarness(api, nil, "app")
	errs := h2.init()
	if len(errs) != 1 || !strings.Contains(errs[0], "boom") {
		t.Errorf("errs = %v", errs)
	}
	if got := h2.keys(); !slices.Equal(got, []string{"FORGE-1", "FORGE-2"}) {
		t.Errorf("rows = %v", got)
	}
	// One toast per refresh, however many lists fail.
	h3 := newHarness(api, nil, "")
	if errs := h3.init(); len(errs) != 1 {
		t.Errorf("errs = %v", errs)
	}
}

func TestLateCacheDoesNotOverwriteFetch(t *testing.T) {
	api := samplePlane()
	h := newHarness(api, nil, "app")
	h.init()
	h.drain(func() tea.Msg {
		return itemsMsg{key: h.lists[0].key, cached: true, items: api.items[p1][3:]}
	})
	if got := h.keys(); !slices.Equal(got, []string{"FORGE-1", "FORGE-2"}) {
		t.Errorf("rows = %v", got)
	}
}

func TestMineAndClear(t *testing.T) {
	api := samplePlane()
	h := newHarness(api, nil, "app")
	h.init()
	h.send("m")
	if got := api.listCalls(); got[len(got)-1] != p1+" assignee= "+openKey+" label= cycle=" {
		t.Fatalf("calls = %v", got)
	}
	if got := h.keys(); !slices.Equal(got, []string{"FORGE-1", "FORGE-2", "FORGE-4"}) {
		t.Fatalf("rows = %v", got)
	}
	if strings.Contains(h.view(), "assignee:") {
		t.Error("filter box still names an assignee")
	}
	h.send("m")
	if got := h.keys(); len(got) != 2 {
		t.Errorf("mine again: %v", got)
	}
	h.send("x")
	if !h.filt.isZero() || len(h.keys()) != 4 {
		t.Errorf("clear left %+v / %v", h.filt, h.keys())
	}
	if got := api.listCalls(); got[len(got)-1] != p1+" assignee= groups=[] label= cycle=" {
		t.Errorf("calls = %v", got)
	}
}

func TestTextFilter(t *testing.T) {
	api := samplePlane()
	h := newHarness(api, nil, "app")
	h.init()
	h.send("/", "b", "u", "g")
	if !h.CapturingInput() {
		t.Error("filter doesn't capture input")
	}
	if got := h.keys(); !slices.Equal(got, []string{"FORGE-2"}) {
		t.Errorf("rows = %v", got)
	}
	h.send("enter")
	if h.CapturingInput() || len(h.keys()) != 1 {
		t.Errorf("enter: capturing %v rows %v", h.CapturingInput(), h.keys())
	}
	h.send("esc")
	if len(h.keys()) != 2 {
		t.Errorf("esc: rows %v", h.keys())
	}
}

func TestFilterForm(t *testing.T) {
	api := samplePlane()
	h := newHarness(api, nil, "app")
	h.init()
	h.send("f")
	if h.dialog == nil || !h.CapturingInput() {
		t.Fatal("no dialog")
	}
	mustContain(t, h.view(), "Project", "Assignee", "States", "Label", "Cycle", "Sprint 12 (current)")
	// Project: keep; assignee: Anyone (Me, Anyone, bob); states: keep;
	// label: bug (Any, bug, ui); cycle: keep.
	h.send("enter", "down", "enter", "enter", "down", "enter", "enter")
	if h.dialog != nil {
		t.Fatalf("dialog still open:\n%s", h.view())
	}
	want := filters{groups: openGroups, label: lbBug.ID}
	if !h.filt.equal(want) {
		t.Fatalf("filters = %+v, want %+v", h.filt, want)
	}
	if got := api.listCalls(); got[len(got)-1] != p1+" assignee= "+openKey+" label=l-bug cycle=" {
		t.Errorf("calls = %v", got)
	}
	if got := h.keys(); !slices.Equal(got, []string{"FORGE-2"}) {
		t.Errorf("rows = %v", got)
	}
	mustContain(t, h.view(), "label: bug")

	// Picking another project drops the label (it was this project's).
	h.send("f", "down", "down", "enter", "enter", "enter", "enter", "enter")
	if h.dialog != nil {
		t.Fatalf("dialog still open:\n%s", h.view())
	}
	if h.filt.project != p2 || h.filt.label != "" {
		t.Fatalf("filters = %+v", h.filt)
	}
	if got := h.keys(); !slices.Equal(got, []string{"LIB-7"}) {
		t.Errorf("rows = %v", got)
	}
	// Switching project drops the override.
	h.SetProject(&h.cfg.Projects[0])
	h.drain(h.Refresh())
	if h.filt.project != "" || !slices.Equal(h.keys(), []string{"FORGE-1", "FORGE-2", "FORGE-4"}) {
		t.Errorf("after SetProject: %+v %v", h.filt, h.keys())
	}
}

func TestCycleFilter(t *testing.T) {
	api := samplePlane()
	h := newHarness(api, nil, "app")
	h.init()
	h.drain(h.setFilters(filters{assignee: assigneeMe, cycle: cyc12.ID}))
	if got := h.keys(); !slices.Equal(got, []string{"FORGE-1"}) {
		t.Errorf("rows = %v", got)
	}
	mustContain(t, h.view(), "cycle: Sprint 12")
}

func TestAllProjects(t *testing.T) {
	api := samplePlane()
	h := newHarness(api, nil, "")
	h.init()
	if got := h.keys(); !slices.Equal(got, []string{"FORGE-1", "FORGE-2", "LIB-7"}) {
		t.Errorf("rows = %v", got)
	}
	mustContain(t, h.view(), "2 projects")
	// Label and cycle are per project: not sent for several.
	h.drain(h.setFilters(filters{label: lbBug.ID}))
	for _, c := range api.listCalls()[2:] {
		if strings.Contains(c, "label=l-bug") {
			t.Errorf("label sent with several projects: %s", c)
		}
	}
}

func TestProjectWithoutPlane(t *testing.T) {
	h := newHarness(samplePlane(), nil, "notes")
	h.init()
	mustContain(t, h.view(), "notes has no Plane project · ctrl+k → Plane: link project")
}

func TestNotConfigured(t *testing.T) {
	cfg := testConfig()
	d := tabkit.Deps{Theme: ui.NewTheme(true, nil), Keys: ui.DefaultKeyMap(), Config: cfg, PlaneErr: errors.New("plane: base_url is not set")}
	h := &harness{Tab: newTab(d)}
	h.SetSize(120, 30)
	if cmd := h.Init(); cmd != nil {
		t.Error("Init without a client returned a command")
	}
	mustContain(t, h.view(), "Plane isn't set up: plane: base_url is not set")
}

func TestAuthError(t *testing.T) {
	api := samplePlane()
	api.meErr = &plane.APIError{Status: http.StatusUnauthorized, Method: "GET", Path: "users/me"}
	h := newHarness(api, nil, "app")
	errs := h.init()
	if len(errs) != 1 || !strings.Contains(errs[0], "API key rejected") {
		t.Errorf("errs = %v", errs)
	}
	mustContain(t, h.view(), "couldn't load issues: API key rejected")
}

func TestUnknownUserFallsBackToAnyone(t *testing.T) {
	api := samplePlane()
	api.meErr = errors.New("timeout")
	h := newHarness(api, nil, "app")
	errs := h.init()
	if len(errs) != 1 || !strings.Contains(errs[0], "who am I") {
		t.Errorf("errs = %v", errs)
	}
	if h.filt.assignee != "" || len(h.keys()) != 3 {
		t.Errorf("filters %+v rows %v", h.filt, h.keys())
	}
}

func TestDetailPane(t *testing.T) {
	api := samplePlane()
	ss := &fakeSessions{
		links: map[string][]store.Session{"FORGE-1": {{ID: 4, Name: "forge-1", Status: store.StatusWorking, CreatedAt: now.Add(-30 * time.Minute)}}},
		prs:   map[string][]store.Link{"FORGE-1": {{Other: store.PRRef("acme/app", 7), LinkMeta: store.LinkMeta{Title: "Add switcher", State: "open"}}}},
	}
	h := newHarness(api, ss, "app")
	h.init()
	v := h.view()
	mustContain(t, v,
		"FORGE-1 · app", "Project switcher", "● In Progress", "▲ high", "→ daan",
		"cycle Sprint 12", "updated 1h ago",
		"Goal", "Switch projects with", "ctrl+p", "fuzzy",
		"Linked", "forge-1", "working", "#7", "Add switcher", "[open]",
		"Comments (2)", "first word", "second word", "bob",
	)
	if strings.Index(v, "first word") > strings.Index(v, "second word") {
		t.Error("comments not oldest first")
	}
	if e := h.det.entry(api.items[p1][0]); e == nil || e.mdWidth == 0 {
		t.Error("description not rendered with Glamour")
	}
	// Moving down shows the next item's detail.
	h.send("j")
	mustContain(t, h.view(), "FORGE-2 · app", "no description", "no sessions or PRs", "no comments")
	if len(api.detail) != 2 {
		t.Errorf("detail fetches = %v", api.detail)
	}
	// Back up: the first item's detail comes from memory.
	h.send("k")
	if len(api.detail) != 2 {
		t.Errorf("refetched: %v", api.detail)
	}
}

func TestStaleDetailErrorIsQuiet(t *testing.T) {
	api := samplePlane()
	h := newHarness(api, nil, "app")
	h.init()
	errs, _ := h.drain(func() tea.Msg {
		return detailLoadedMsg{id: "w-FORGE-2", key: "w-FORGE-2@old", err: errors.New("boom")}
	})
	if len(errs) != 0 {
		t.Errorf("errs = %v", errs)
	}
	errs, _ = h.drain(func() tea.Msg {
		return detailLoadedMsg{id: "w-FORGE-1", key: h.det.want, err: errors.New("boom")}
	})
	if len(errs) != 1 || !strings.Contains(errs[0], "FORGE-1: boom") {
		t.Errorf("errs = %v", errs)
	}
}

func TestEntitiesAndSelect(t *testing.T) {
	api := samplePlane()
	h := newHarness(api, nil, "app")
	h.init()
	es := h.Entities()
	if len(es) != 2 {
		t.Fatalf("entities = %+v", es)
	}
	e := es[1]
	if e.Kind != EntityIssue || e.ID != "w-FORGE-2" || e.Title != "FORGE-2 Palette ranking" ||
		e.Detail != "Todo · daan +1 · app" || e.Search != "bug ui" {
		t.Errorf("entity = %+v", e)
	}
	h.send("/", "s", "w", "i", "t", "enter")
	if len(h.keys()) != 1 {
		t.Fatalf("rows = %v", h.keys())
	}
	h.drain(h.Select("w-FORGE-2"))
	if h.filter.Visible() || h.selectedID() != "w-FORGE-2" {
		t.Errorf("filter %q selected %q", h.filter.Value(), h.selectedID())
	}
}

func TestIssueStatesSynced(t *testing.T) {
	api := samplePlane()
	ss := &fakeSessions{}
	h := newHarness(api, ss, "app")
	h.init()
	if len(ss.states) != 1 || !reflect.DeepEqual(ss.states[0], map[string]string{"FORGE-1": "In Progress", "FORGE-2": "Todo"}) {
		t.Fatalf("states = %v", ss.states)
	}
	h.drain(h.Refresh())
	if len(ss.states) != 1 {
		t.Errorf("unchanged states synced again: %v", ss.states)
	}
	api.setState(p1, "w-FORGE-2", stDoing)
	h.drain(h.Refresh())
	if len(ss.states) != 2 || !reflect.DeepEqual(ss.states[1], map[string]string{"FORGE-2": "In Progress"}) {
		t.Errorf("states = %v", ss.states)
	}
}

func TestOpenAndCopy(t *testing.T) {
	api := samplePlane()
	h := newHarness(api, nil, "app")
	h.init()
	var opened []string
	h.browser = openerFunc(func(u string) { opened = append(opened, u) })
	h.send("o")
	if want := "https://plane.example/acme/browse/FORGE-1/"; len(opened) != 1 || opened[0] != want {
		t.Errorf("opened = %v", opened)
	}
	_, cmd := h.Update(press("y")) // no copier: OSC 52
	_, infos := h.drain(cmd)
	if len(infos) != 1 || infos[0] != "copied https://plane.example/acme/browse/FORGE-1/" {
		t.Errorf("infos = %v", infos)
	}
}

type openerFunc func(string)

func (f openerFunc) Open(_ context.Context, u string) error { f(u); return nil }

func TestCommandsMatchKeymap(t *testing.T) {
	h := newHarness(samplePlane(), nil, "app")
	ids := map[string]bool{}
	for _, c := range h.Commands() {
		ids[c.ID] = true
	}
	for _, id := range []string{"plane_filters", "plane_mine", "plane_clear_filters", "plane_detail_down", "plane_detail_up", "plane_open", "plane_copy_url", "filter"} {
		if !ids[id] {
			t.Errorf("no palette command %s", id)
		}
	}
}

var _ tabs.Tab = (*harness)(nil)
