package docs

import (
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
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
)

var now = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

func writeDoc(t *testing.T, root, rel, body string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeWatcher signals when the test says so.
type fakeWatcher struct {
	ch   chan struct{}
	mu   sync.Mutex
	sets [][]string
}

func newFakeWatcher() *fakeWatcher { return &fakeWatcher{ch: make(chan struct{}, 1)} }

func (f *fakeWatcher) Set(roots []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets = append(f.sets, slices.Clone(roots))
	return nil
}
func (f *fakeWatcher) Events() <-chan struct{} { return f.ch }
func (f *fakeWatcher) Close() error            { return nil }

// fixture is two projects with docs in temp dirs.
type fixture struct {
	cfg    *config.Config
	alpha  string // alpha's docs folder
	bravo  string
	edited []string
	mu     sync.Mutex
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()
	f := &fixture{cfg: config.Default()}
	f.cfg.Projects = []config.Project{
		{Name: "alpha", Repo: "o/alpha", Path: filepath.Join(base, "alpha")},
		{Name: "bravo", Repo: "o/bravo", Path: filepath.Join(base, "bravo"), DocsDir: "notes"},
	}
	f.alpha = f.cfg.Projects[0].DocsPath()
	f.bravo = f.cfg.Projects[1].DocsPath()
	var long strings.Builder
	long.WriteString("# Architecture\n\n")
	for i := range 80 {
		long.WriteString("Paragraph line ")
		long.WriteString(strings.Repeat("x", i%7))
		long.WriteString("\n\n")
	}
	long.WriteString("THE END\n")
	writeDoc(t, f.alpha, "architecture.md", long.String())
	writeDoc(t, f.alpha, "runbooks/deploy.md", "# Deploy\n\nRun **make deploy**.\n")
	writeDoc(t, f.alpha, "notes.txt", "not markdown")
	writeDoc(t, f.bravo, "readme.md", "# Bravo readme\n")
	return f
}

func (f *fixture) editedPaths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.edited)
}

func (f *fixture) tab(project int, w *fakeWatcher) *Tab {
	d := tabkit.Deps{Theme: ui.NewTheme(true, nil), Keys: ui.DefaultKeyMap(), Config: f.cfg}
	if w != nil {
		d.DocsWatcher = w
	}
	tab := newTab(d)
	tab.now = func() time.Time { return now }
	tab.edit = func(path string) tea.Cmd {
		f.mu.Lock()
		f.edited = append(f.edited, path)
		f.mu.Unlock()
		return func() tea.Msg { return editedMsg{path: path} }
	}
	if project >= 0 {
		tab.SetProject(&f.cfg.Projects[project])
	} else {
		tab.SetProject(nil)
	}
	tab.SetSize(120, 30)
	return tab
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
		case ui.SuccessMsg:
			toasts = append(toasts, "ok: "+m.Text)
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

func rowNames(tab *Tab) []string {
	out := make([]string, len(tab.rows))
	for i, r := range tab.rows {
		out[i] = strings.Repeat(" ", r.depth) + r.node.Name
		if r.top {
			out[i] = "[" + tab.roots[r.root].name + "]"
		}
	}
	return out
}

func TestBrowseProject(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(0, nil)
	pump(tab, tab.Refresh())

	if got, want := rowNames(tab), []string{"runbooks", "architecture.md"}; !slices.Equal(got, want) {
		t.Fatalf("rows = %q, want %q (folders start closed, .txt hidden)", got, want)
	}
	send(tab, "enter") // expand runbooks
	if got, want := rowNames(tab), []string{"runbooks", " deploy.md", "architecture.md"}; !slices.Equal(got, want) {
		t.Fatalf("expanded rows = %q", got)
	}
	send(tab, "j")
	if v := view(tab); !strings.Contains(v, "Run make deploy") || !strings.Contains(v, "runbooks/deploy.md") {
		t.Fatalf("detail doesn't show the rendered doc:\n%s", v)
	}
	send(tab, "h") // doc -> parent folder
	if r, _ := tab.selected(); r.node.Name != "runbooks" {
		t.Fatalf("h on a doc selected %q", r.node.Name)
	}
	send(tab, "h") // collapse
	if got := rowNames(tab); len(got) != 2 {
		t.Fatalf("collapse: %q", got)
	}
}

func TestDetailFocusScrolls(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(0, nil)
	pump(tab, tab.Refresh())
	send(tab, "j") // architecture.md
	if v := view(tab); strings.Contains(v, "THE END") || !strings.Contains(v, "Architecture") {
		t.Fatalf("long doc not at the top:\n%s", v)
	}
	send(tab, "enter")
	if !tab.focusDetail {
		t.Fatal("enter on a doc didn't focus the detail")
	}
	send(tab, "j")
	send(tab, "j")
	if tab.det.offset != 2 || tab.cursor != 1 {
		t.Fatalf("j in detail: offset %d cursor %d", tab.det.offset, tab.cursor)
	}
	send(tab, "G")
	if v := view(tab); !strings.Contains(v, "THE END") {
		t.Fatalf("G didn't reach the end:\n%s", v)
	}
	send(tab, "esc")
	if tab.focusDetail {
		t.Fatal("esc didn't return to the tree")
	}
	send(tab, "k")
	if tab.cursor != 0 {
		t.Fatalf("k in tree: cursor %d", tab.cursor)
	}
	send(tab, "j")
	if tab.det.offset != 0 {
		t.Fatalf("scroll not reset for the doc: %d", tab.det.offset)
	}
	send(tab, "J")
	if tab.det.offset != 1 || tab.focusDetail {
		t.Fatalf("J from the tree: offset %d", tab.det.offset)
	}
}

func TestAllProjects(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(-1, nil)
	pump(tab, tab.Refresh())
	want := []string{"[alpha]", " runbooks", " architecture.md", "[bravo]", " readme.md"}
	if got := rowNames(tab); !slices.Equal(got, want) {
		t.Fatalf("rows = %q, want %q", got, want)
	}
	if v := view(tab); !strings.Contains(v, "2 docs") {
		t.Fatalf("project row has no count:\n%s", v)
	}
	send(tab, "enter") // collapse alpha
	if got := rowNames(tab); !slices.Equal(got, []string{"[alpha]", "[bravo]", " readme.md"}) {
		t.Fatalf("collapsed alpha: %q", got)
	}
}

func TestEditRerenders(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(0, nil)
	pump(tab, tab.Refresh())
	send(tab, "enter")
	send(tab, "j") // deploy.md
	p := filepath.Join(f.alpha, "runbooks", "deploy.md")
	// The fake editor "saves" the file before reporting back.
	edit := tab.edit
	tab.edit = func(path string) tea.Cmd {
		writeDoc(t, f.alpha, "runbooks/deploy.md", "# Deploy\n\nNow with **rollback**.\n")
		_ = os.Chtimes(path, now, now.Add(time.Minute))
		return edit(path)
	}
	send(tab, "e")
	if got := f.editedPaths(); !slices.Equal(got, []string{p}) {
		t.Fatalf("edited %q", got)
	}
	if v := view(tab); !strings.Contains(v, "Now with rollback") {
		t.Fatalf("not re-rendered after editing:\n%s", v)
	}
}

func TestEditOnFolderDoesNothing(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(0, nil)
	pump(tab, tab.Refresh())
	send(tab, "e")
	if len(f.editedPaths()) != 0 {
		t.Fatal("e on a folder opened the editor")
	}
}

func TestEmptyProject(t *testing.T) {
	f := newFixture(t)
	f.cfg.Projects[0].DocsDir = "missing"
	tab := f.tab(0, nil)
	pump(tab, tab.Refresh())
	if v := view(tab); !strings.Contains(v, "No docs in") || !strings.Contains(v, "n  create one") {
		t.Fatalf("empty state:\n%s", v)
	}
}

func TestProjectSwitchDropsStaleScan(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(0, nil)
	stale := drain(tab.Refresh())
	tab.SetProject(&f.cfg.Projects[1])
	for _, m := range stale {
		tab.Update(m)
	}
	if len(tab.rows) != 0 {
		t.Fatalf("stale scan shown: %q", rowNames(tab))
	}
	pump(tab, tab.Refresh())
	if got := rowNames(tab); !slices.Equal(got, []string{"readme.md"}) {
		t.Fatalf("rows = %q", got)
	}
}

func TestCommandsRegistered(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(0, nil)
	var ids []string
	for _, c := range tab.Commands() {
		ids = append(ids, c.ID)
	}
	for _, id := range []string{"docs_new", "docs_edit", "docs_rename", "docs_delete", "docs_open", "docs_back", "docs_detail_down", "docs_detail_up", "docs_mark", "docs_send", "docs_decision", "docs_decisions", "docs_supersede", "filter"} {
		if !slices.Contains(ids, id) {
			t.Errorf("no palette command %s", id)
		}
	}
}

// --- teatest flows ---------------------------------------------------------------

// host runs the tab as a program, standing in for the root model.
type host struct {
	tab *Tab

	mu     sync.Mutex
	frame  string
	toasts []string
}

func (h *host) Init() tea.Cmd { return h.tab.Init() }

func (h *host) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	toast := ""
	switch msg := msg.(type) {
	case ui.SuccessMsg:
		toast = "ok: " + msg.Text
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

type screen struct {
	tm *teatest.TestModel
	h  *host
}

func newScreen(t *testing.T, tab *Tab) *screen {
	h := &host{tab: tab}
	return &screen{tm: teatest.NewTestModel(t, h, teatest.WithInitialTermSize(120, 30)), h: h}
}

func (s *screen) until(t *testing.T, what string, cond func(frame string, toasts []string) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.h.mu.Lock()
		frame, toasts := s.h.frame, slices.Clone(s.h.toasts)
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

func (s *screen) toast(t *testing.T, want string) {
	t.Helper()
	s.until(t, "toast "+want, func(_ string, toasts []string) bool {
		return slices.ContainsFunc(toasts, func(x string) bool { return strings.HasPrefix(x, want) })
	})
}

func (s *screen) quit(t *testing.T) {
	s.tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	s.tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

func press(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "ctrl+u":
		return tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

// TestNewDocFlow: n, a name in the selected folder, the runbook template;
// the doc is created, selected and opened in $EDITOR.
func TestNewDocFlow(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(0, nil)
	sc := newScreen(t, tab)
	sc.wait(t, "runbooks/", "architecture.md")

	sc.tm.Send(press("enter")) // expand runbooks (selected)
	sc.wait(t, "deploy.md")
	sc.tm.Send(press("n"))
	sc.wait(t, "New doc", "Name", "Template")
	sc.tm.Type("rollback")
	sc.tm.Send(press("enter"))
	time.Sleep(200 * time.Millisecond) // huh moves to the next field in a Cmd
	sc.tm.Send(press("down"))
	sc.tm.Send(press("down"))
	sc.tm.Send(press("down")) // blank, spec, notes, runbook
	sc.tm.Send(press("enter"))
	sc.toast(t, "ok: created runbooks/rollback.md")
	sc.wait(t, "rollback.md", "Runbook", "Rollback")

	p := filepath.Join(f.alpha, "runbooks", "rollback.md")
	b, err := os.ReadFile(p)
	if err != nil || !strings.HasPrefix(string(b), "# Rollback\n") || !strings.Contains(string(b), "## Steps") {
		t.Fatalf("created doc: %q, %v", b, err)
	}
	sc.until(t, "editor opened", func(string, []string) bool { return slices.Equal(f.editedPaths(), []string{p}) })
	sc.quit(t)
}

func TestRenameAndDeleteFlow(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(0, nil)
	sc := newScreen(t, tab)
	sc.wait(t, "architecture.md")

	sc.tm.Send(press("j"))
	sc.tm.Send(press("m"))
	sc.wait(t, "Rename architecture.md")
	sc.tm.Send(press("ctrl+u"))
	sc.tm.Type("design/overview")
	sc.tm.Send(press("enter"))
	sc.toast(t, "ok: renamed to design/overview.md")
	sc.wait(t, "design/", "overview.md")
	if _, err := os.Stat(filepath.Join(f.alpha, "design", "overview.md")); err != nil {
		t.Fatal(err)
	}

	sc.tm.Send(press("D"))
	sc.wait(t, "Move design/overview.md to .trash?")
	sc.tm.Send(press("left")) // Cancel -> Delete
	sc.tm.Send(press("enter"))
	sc.toast(t, "ok: moved design/overview.md to .trash")
	sc.until(t, "doc gone", func(frame string, _ []string) bool { return !strings.Contains(frame, "overview.md") })
	if _, err := os.Stat(filepath.Join(f.alpha, ".trash", "design", "overview.md")); err != nil {
		t.Fatalf("not in trash: %v", err)
	}
	sc.quit(t)
}

// TestWatcherShowsExternalEdits: a file an agent writes shows up without a
// refresh once the watcher signals.
func TestWatcherShowsExternalEdits(t *testing.T) {
	f := newFixture(t)
	w := newFakeWatcher()
	tab := f.tab(0, w)
	sc := newScreen(t, tab)
	sc.wait(t, "architecture.md")

	writeDoc(t, f.alpha, "agent-notes.md", "# From the agent\n")
	w.ch <- struct{}{}
	sc.wait(t, "agent-notes.md")
	w.mu.Lock()
	sets := slices.Clone(w.sets)
	w.mu.Unlock()
	if len(sets) == 0 || !slices.Equal(sets[0], []string{f.alpha}) {
		t.Fatalf("watcher roots = %q", sets)
	}
	sc.quit(t)
}

// --- search (P4.2) ---------------------------------------------------------------

func typeText(tab *Tab, s string) {
	for _, r := range s {
		send(tab, string(r))
	}
}

func hitRels(tab *Tab) []string {
	out := make([]string, len(tab.hits))
	for i, h := range tab.hits {
		out[i] = h.Doc.Rel
	}
	return out
}

// longDoc has a Rollback section far below the first screen.
func longDoc() string {
	var b strings.Builder
	b.WriteString("# Release\n\n")
	for range 60 {
		b.WriteString("filler text\n\n")
	}
	b.WriteString("## Rollback\n\nRun the zanzibar script to undo.\n\n")
	for range 60 {
		b.WriteString("more filler\n\n")
	}
	return b.String()
}

// TestSearchJumpsToHeading is the P4.2 done-when: typing a word finds the
// doc and enter jumps to the matching heading.
func TestSearchJumpsToHeading(t *testing.T) {
	f := newFixture(t)
	writeDoc(t, f.alpha, "runbooks/release.md", longDoc())
	tab := f.tab(0, nil)
	pump(tab, tab.Refresh())

	send(tab, "/")
	if !tab.CapturingInput() {
		t.Fatal("search doesn't capture input")
	}
	typeText(tab, "zanzibar")
	if got := hitRels(tab); !slices.Equal(got, []string{"runbooks/release.md"}) {
		t.Fatalf("hits = %q", got)
	}
	v := view(tab)
	if !strings.Contains(v, "Run the zanzibar script") || !strings.Contains(v, "/ zanzibar") {
		t.Fatalf("results don't show the snippet and query:\n%s", v)
	}
	// The preview is already scrolled to the heading.
	if !strings.Contains(v, "Rollback") {
		t.Fatalf("preview not at the heading:\n%s", v)
	}
	send(tab, "enter") // stop typing
	if tab.CapturingInput() || !tab.searching() {
		t.Fatal("enter should keep the results and stop typing")
	}
	send(tab, "enter") // jump
	if tab.searching() || !tab.focusDetail {
		t.Fatalf("jump: searching %v focus %v", tab.searching(), tab.focusDetail)
	}
	r, _ := tab.selected()
	if r.node.Rel != "runbooks/release.md" {
		t.Fatalf("selected %q", r.node.Rel)
	}
	got, ok := tab.current()
	h := headingLine(got.lines, "Rollback")
	if !ok || h <= 0 || tab.det.offset != h {
		t.Fatalf("detail offset %d, heading at line %d", tab.det.offset, h)
	}
	if v := view(tab); !strings.Contains(v, "zanzibar") || strings.Contains(v, "# Release") {
		t.Fatalf("detail not scrolled:\n%s", v)
	}
}

func TestSearchRanking(t *testing.T) {
	f := newFixture(t)
	writeDoc(t, f.alpha, "a-body.md", "# Notes\n\nwe deploy on fridays\n")
	writeDoc(t, f.alpha, "b-heading.md", "# Ops\n\n## How to deploy\n\ntext\n")
	tab := f.tab(0, nil)
	pump(tab, tab.Refresh())
	send(tab, "/")
	typeText(tab, "deploy")
	want := []string{"runbooks/deploy.md", "b-heading.md", "a-body.md"}
	if got := hitRels(tab); !slices.Equal(got, want) {
		t.Fatalf("hits = %q, want %q", got, want)
	}
	if tab.hits[1].Heading != "How to deploy" || tab.hits[2].Heading != "Notes" {
		t.Fatalf("headings = %q / %q", tab.hits[1].Heading, tab.hits[2].Heading)
	}
	send(tab, "down")
	if r, _ := tab.selected(); r.node.Rel != "b-heading.md" {
		t.Fatalf("down didn't preview the next hit: %q", r.node.Rel)
	}
}

func TestSearchScope(t *testing.T) {
	f := newFixture(t)
	writeDoc(t, f.bravo, "readme2.md", "# Other\n\nalpha mention\n")
	for _, tc := range []struct {
		project int
		want    []string
	}{
		{-1, []string{"readme.md", "readme2.md"}},
		{1, []string{"readme.md", "readme2.md"}},
		{0, nil},
	} {
		tab := f.tab(tc.project, nil)
		pump(tab, tab.Refresh())
		send(tab, "/")
		typeText(tab, "readme")
		got := hitRels(tab)
		if tc.project == 0 {
			if len(got) != 0 {
				t.Fatalf("alpha only: hits %q", got)
			}
			continue
		}
		if len(got) != 1 || got[0] != "readme.md" {
			t.Fatalf("project %d: hits %q", tc.project, got)
		}
	}
	// All projects shows the project next to the path.
	tab := f.tab(-1, nil)
	pump(tab, tab.Refresh())
	send(tab, "/")
	typeText(tab, "mention")
	if v := view(tab); !strings.Contains(v, "bravo · readme2.md") {
		t.Fatalf("no project in result:\n%s", v)
	}
}

func TestSearchEscRestoresTree(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(0, nil)
	pump(tab, tab.Refresh())
	send(tab, "/")
	typeText(tab, "deploy")
	send(tab, "esc")
	if tab.searching() || tab.CapturingInput() || tab.search.Value() != "" {
		t.Fatal("esc didn't clear the search")
	}
	if v := view(tab); !strings.Contains(v, "architecture.md") {
		t.Fatalf("tree not back:\n%s", v)
	}
	// Esc from the results (not typing) also clears.
	send(tab, "/")
	typeText(tab, "x")
	send(tab, "enter")
	send(tab, "esc")
	if tab.searching() {
		t.Fatal("esc in results didn't clear")
	}
	// No matches.
	send(tab, "/")
	typeText(tab, "qqqq")
	if v := view(tab); !strings.Contains(v, "No matches") {
		t.Fatalf("no empty state:\n%s", v)
	}
}

func TestSearchReindexesOnWatch(t *testing.T) {
	f := newFixture(t)
	w := newFakeWatcher()
	tab := f.tab(0, w)
	sc := newScreen(t, tab)
	sc.wait(t, "architecture.md")
	sc.tm.Send(press("/"))
	for _, r := range "kumquat" {
		sc.tm.Send(press(string(r)))
	}
	sc.wait(t, "No matches")
	writeDoc(t, f.alpha, "fruit.md", "# Fruit\n\n## Citrus\n\nA kumquat is small.\n")
	w.ch <- struct{}{}
	sc.wait(t, "A kumquat is small", "fruit.md")
	sc.quit(t)
}

func TestDocsEntities(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(-1, nil)
	pump(tab, tab.Refresh())
	var titles []string
	var deploy tabs.Entity
	for _, e := range tab.Entities() {
		if e.Kind != EntityKind {
			t.Fatalf("kind %q", e.Kind)
		}
		titles = append(titles, e.Title)
		if e.Title == "Deploy" {
			deploy = e
		}
	}
	if !slices.Equal(titles, []string{"Deploy", "Architecture", "Bravo readme"}) {
		t.Fatalf("titles = %q", titles)
	}
	if deploy.Detail != "alpha · runbooks/deploy.md" || !strings.Contains(deploy.Search, "runbooks/deploy.md") {
		t.Fatalf("deploy entity = %+v", deploy)
	}
	send(tab, "/")
	typeText(tab, "x")
	pump(tab, tab.Select(deploy.ID))
	_, cmd := tab.Update(nil)
	pump(tab, cmd)
	if tab.searching() || !tab.focusDetail {
		t.Fatal("select should drop the search and focus the doc")
	}
	if r, _ := tab.selected(); r.node.Path != deploy.ID {
		t.Fatalf("selected %q", r.node.Path)
	}
}

func TestHeadingLine(t *testing.T) {
	lines := []string{"  \x1b[1m# Release\x1b[0m", "", "use rollback carefully", "  ## Rollback  ", "  Use **bold** code"}
	tests := []struct {
		heading string
		want    int
	}{
		{"Release", 0},
		{"Rollback", 3},
		{"Use **bold** code", 4},
		{"rollback carefully", 2},
		{"missing", -1},
		{"", -1},
	}
	for _, tt := range tests {
		if got := headingLine(lines, tt.heading); got != tt.want {
			t.Errorf("headingLine(%q) = %d, want %d", tt.heading, got, tt.want)
		}
	}
}

func TestSnippet(t *testing.T) {
	tests := []struct {
		s    string
		at   int
		w    int
		want string
	}{
		{"short", 0, 10, "short"},
		{"0123456789abcdefghij", 15, 10, "…cdefghij"},
		{"0123456789abcdefghijklmnop", 15, 10, "…cdefghij…"},
		{"0123456789abcdefghij", 1, 10, "012345678…"},
	}
	for _, tt := range tests {
		if got := snippet(tt.s, tt.at, tt.w); got != tt.want {
			t.Errorf("snippet(%q, %d, %d) = %q, want %q", tt.s, tt.at, tt.w, got, tt.want)
		}
	}
}
