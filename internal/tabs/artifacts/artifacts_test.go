package artifacts

import (
	"context"
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

	artfs "github.com/daan3011/forge/internal/artifacts"
	"github.com/daan3011/forge/internal/browser"
	"github.com/daan3011/forge/internal/clipboard"
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
)

var now = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

func writeFile(t *testing.T, path, body string, mod time.Time) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !mod.IsZero() {
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// fakeSessions lists fixed sessions.
type fakeSessions struct {
	live, archived []store.Session
	err            error
}

func (f *fakeSessions) List(_ context.Context, project string, archived bool) ([]sessions.Row, error) {
	src := f.live
	if archived {
		src = f.archived
	}
	var out []sessions.Row
	for _, x := range src {
		if project == "" || x.Project == project {
			out = append(out, sessions.Row{Session: x})
		}
	}
	return out, f.err
}

// fakeWatcher signals when the test says so.
type fakeWatcher struct {
	ch   chan struct{}
	mu   sync.Mutex
	sets [][]string
}

func newFakeWatcher() *fakeWatcher { return &fakeWatcher{ch: make(chan struct{}, 1)} }

func (f *fakeWatcher) SetTargets(ts []artfs.Target) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets = append(f.sets, artfs.Roots(ts))
	return nil
}
func (f *fakeWatcher) Events() <-chan struct{} { return f.ch }
func (f *fakeWatcher) Close() error            { return nil }

// fixture is two projects and a session worktree in temp dirs, with a real
// store.
type fixture struct {
	cfg   *config.Config
	alpha string // alpha's checkout
	bravo string
	wt    string // session s1's worktree (alpha)
	sess  *fakeSessions
	st    *store.Store
	files *browser.Fake
	clip  *clipboard.Fake
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		cfg:   config.Default(),
		alpha: filepath.Join(base, "alpha"),
		bravo: filepath.Join(base, "bravo"),
		wt:    filepath.Join(base, "worktrees", "s1"),
		files: &browser.Fake{},
		clip:  &clipboard.Fake{},
	}
	f.cfg.Projects = []config.Project{
		{Name: "alpha", Repo: "o/alpha", Path: f.alpha},
		{Name: "bravo", Repo: "o/bravo", Path: f.bravo},
	}
	f.st, err = store.Open(context.Background(), filepath.Join(base, "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.st.Close() })
	s1, err := f.st.Sessions().Create(context.Background(), store.Session{
		Name: "s1", ClaudeSessionID: "u1", Project: "alpha", Worktree: f.wt,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.sess = &fakeSessions{live: []store.Session{s1}}

	writeFile(t, filepath.Join(f.alpha, "docs", "flow.svg"), "svg", now.Add(-3*time.Hour))
	writeFile(t, filepath.Join(f.alpha, "main.go"), "package main", now.Add(-time.Minute))
	writeFile(t, filepath.Join(f.alpha, "node_modules", "x", "index.html"), "x", now)
	writeFile(t, filepath.Join(f.wt, "design", "mock.html"), "<h1>mock</h1>", now.Add(-time.Hour))
	writeFile(t, filepath.Join(f.bravo, "shot.png"), "png", now.Add(-2*time.Hour))
	return f
}

func (f *fixture) tab(project int, w artfs.Watcher) *Tab {
	d := tabkit.Deps{
		Theme: ui.NewTheme(true, nil), Keys: ui.DefaultKeyMap(), Config: f.cfg,
		Sessions: nil, Artifacts: f.st.Artifacts(), Files: f.files, Clipboard: f.clip,
	}
	if w != nil {
		d.ArtifactWatcher = w
	}
	tab := newTab(d)
	tab.sessions = f.sess
	tab.now = func() time.Time { return now }
	if project >= 0 {
		tab.SetProject(&f.cfg.Projects[project])
	} else {
		tab.SetProject(nil)
	}
	tab.SetSize(120, 30)
	return tab
}

// refresh runs a scan synchronously.
func refresh(t *testing.T, tab *Tab) {
	t.Helper()
	tab.Update(tab.Refresh()())
}

func press(tab *Tab, k string) tea.Cmd {
	var msg tea.KeyPressMsg
	switch k {
	case "down":
		msg = tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		msg = tea.KeyPressMsg{Code: tea.KeyUp}
	default:
		msg = tea.KeyPressMsg{Code: rune(k[0]), Text: k}
	}
	_, cmd := tab.Update(msg)
	return cmd
}

func view(tab *Tab) string { return ansi.Strip(tab.View()) }

func names(tab *Tab) []string {
	var out []string
	for _, r := range tab.rows {
		if r.header {
			out = append(out, "# "+r.project)
			continue
		}
		out = append(out, filepath.Base(tab.items[r.item].Path))
	}
	return out
}

func TestGroupsNewestFirst(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(-1, nil)
	refresh(t, tab)
	want := []string{"# alpha", "mock.html", "flow.svg", "# bravo", "shot.png"}
	if got := names(tab); !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	if a, ok := tab.selected(); !ok || filepath.Base(a.Path) != "mock.html" {
		t.Fatalf("selected = %+v (cursor skips the header)", a)
	}
	v := view(tab)
	for _, s := range []string{"alpha", "2 artifacts", "◫ mock.html", "s1", "html", "1h", "◇ flow.svg", "repo", "▣ shot.png"} {
		if !strings.Contains(v, s) {
			t.Errorf("view lacks %q:\n%s", s, v)
		}
	}
	if strings.Contains(v, "index.html") || strings.Contains(v, "main.go") {
		t.Errorf("view shows ignored files:\n%s", v)
	}
	// Detail pane.
	for _, s := range []string{"Project  alpha", "Source   s1", "Size     13 B", "worktrees/s1/design", "(1h ago)"} {
		if !strings.Contains(v, s) {
			t.Errorf("detail lacks %q:\n%s", s, v)
		}
	}
	if got := tab.StatusDetail(); got != "3 artifacts" {
		t.Errorf("StatusDetail = %q", got)
	}

	// Moving skips headers both ways.
	press(tab, "down")
	press(tab, "down")
	if a, _ := tab.selected(); filepath.Base(a.Path) != "shot.png" {
		t.Errorf("after 2 down = %s", a.Path)
	}
	press(tab, "up")
	if a, _ := tab.selected(); filepath.Base(a.Path) != "flow.svg" {
		t.Errorf("after up = %s", a.Path)
	}
	press(tab, "g")
	if a, _ := tab.selected(); filepath.Base(a.Path) != "mock.html" {
		t.Errorf("after top = %s", a.Path)
	}
}

func TestProjectFilterAndStaleScan(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(-1, nil)
	stale := tab.Refresh()
	tab.SetProject(&f.cfg.Projects[1])
	tab.Update(stale()) // from before the switch: dropped
	if len(tab.rows) != 0 || tab.scanned {
		t.Fatalf("stale scan applied: %v", names(tab))
	}
	refresh(t, tab)
	if got := names(tab); !slices.Equal(got, []string{"shot.png"}) {
		t.Errorf("bravo rows = %v (no headers for one project)", got)
	}
}

func TestCachedThenScanned(t *testing.T) {
	f := newFixture(t)
	refresh(t, f.tab(-1, nil)) // a previous run indexed everything

	// A new run shows the index before its scan lands; a file removed
	// meanwhile drops out with the scan.
	if err := os.Remove(filepath.Join(f.bravo, "shot.png")); err != nil {
		t.Fatal(err)
	}
	tab := f.tab(-1, nil)
	tab.Update(tab.loadCached()())
	if got := len(tab.items); got != 3 {
		t.Fatalf("cached items = %d", got)
	}
	refresh(t, tab)
	if got := names(tab); !slices.Equal(got, []string{"# alpha", "mock.html", "flow.svg"}) {
		t.Errorf("after scan = %v", got)
	}
	// A cached list arriving after the scan is ignored.
	tab.Update(cachedMsg{gen: tab.gen, items: nil})
	if len(tab.items) != 2 {
		t.Errorf("late cache replaced the scan")
	}
}

func TestRemovedWorktreeIsPruned(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(-1, nil)
	refresh(t, tab)
	if err := os.RemoveAll(f.wt); err != nil {
		t.Fatal(err)
	}
	refresh(t, tab) // the worktree is no longer a target, but its rows go
	if got := names(tab); !slices.Equal(got, []string{"# bravo", "shot.png", "# alpha", "flow.svg"}) {
		t.Errorf("rows = %v (groups ordered by their newest artifact)", got)
	}
	got, err := f.st.Artifacts().List(context.Background(), "")
	if err != nil || len(got) != 2 {
		t.Errorf("index = %d rows, %v", len(got), err)
	}
}

func TestOpenAndCopyPath(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(-1, nil)
	refresh(t, tab)
	mock := filepath.Join(f.wt, "design", "mock.html")

	msg := press(tab, "o")()
	if im, ok := msg.(ui.InfoMsg); !ok || im.Text != "opened mock.html" {
		t.Errorf("open = %#v", msg)
	}
	if got := f.files.Files(); !slices.Equal(got, []string{mock}) {
		t.Errorf("opened = %v", got)
	}

	msg = press(tab, "y")()
	if im, ok := msg.(ui.InfoMsg); !ok || im.Text != "copied "+mock {
		t.Errorf("copy = %#v", msg)
	}
	if got := f.clip.Copied(); !slices.Equal(got, []string{mock}) {
		t.Errorf("copied = %v", got)
	}

	// Without a copy tool, the terminal copies (OSC 52).
	f.clip.Err = clipboard.ErrNoTool
	msg = press(tab, "y")()
	fb, ok := msg.(clipboardFallbackMsg)
	if !ok || fb.text != mock {
		t.Fatalf("fallback = %#v", msg)
	}
	if _, cmd := tab.Update(fb); cmd == nil {
		t.Error("fallback did nothing")
	}

	// A failing opener is an error toast.
	f.files.Err = errors.New("boom")
	if _, ok := press(tab, "o")().(ui.ErrMsg); !ok {
		t.Error("open error not reported")
	}
}

func TestKeep(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(-1, nil)
	refresh(t, tab)

	msg := press(tab, "c")()
	km, ok := msg.(keptMsg)
	want := filepath.Join(f.alpha, ".forge", "artifacts", "s1", "mock.html")
	if !ok || km.err != nil || !km.wrote || km.dest != want {
		t.Fatalf("keep = %#v", msg)
	}
	_, cmd := tab.Update(km)
	if cmd == nil {
		t.Fatal("no toast / rescan")
	}
	refresh(t, tab)
	if got := names(tab); !slices.Contains(got, "mock.html") || len(got) != 6 {
		t.Fatalf("rows after keep = %v", got)
	}
	if a, _ := tab.selected(); a.Path != filepath.Join(f.wt, "design", "mock.html") {
		t.Errorf("cursor moved to %s", a.Path)
	}
	if !strings.Contains(view(tab), "kept") {
		t.Errorf("kept copy not labelled:\n%s", view(tab))
	}

	// Again: the same copy is reused.
	km = press(tab, "c")().(keptMsg)
	if km.wrote || km.dest != want {
		t.Errorf("second keep = %#v", km)
	}
	// The kept copy itself can't be kept.
	tab.Select(want)
	if a, _ := tab.selected(); a.Path != want {
		t.Fatalf("Select didn't select the copy: %s", a.Path)
	}
	if m, ok := press(tab, "c")().(ui.InfoMsg); !ok || !strings.Contains(m.Text, "already kept") {
		t.Errorf("keep of a copy = %#v", m)
	}
}

func TestEmptyState(t *testing.T) {
	f := newFixture(t)
	f.cfg.Artifacts.Watch = []string{"**/*.nothing"}
	tab := f.tab(-1, nil)
	tab.spec = artfs.NewSpec(f.cfg.Artifacts.Watch)
	if v := view(tab); !strings.Contains(v, "Scanning") {
		t.Errorf("before scan:\n%s", v)
	}
	refresh(t, tab)
	if v := view(tab); !strings.Contains(v, "No artifacts yet") || !strings.Contains(v, "**/*.nothing") {
		t.Errorf("empty:\n%s", v)
	}
	if press(tab, "o") != nil || press(tab, "y") != nil || press(tab, "c") != nil {
		t.Error("actions ran with nothing selected")
	}
}

func TestCommandsAndEntities(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(-1, nil)
	refresh(t, tab)
	ids := map[string]bool{}
	for _, c := range tab.Commands() {
		ids[c.ID] = true
	}
	for _, id := range []string{"artifacts_open", "artifacts_copy_path", "artifacts_keep"} {
		if !ids[id] {
			t.Errorf("no palette command %s", id)
		}
	}
	for _, c := range tab.Commands() {
		if c.ID == "artifacts_copy_path" {
			c.Run()()
		}
	}
	if len(f.clip.Copied()) != 1 {
		t.Error("palette copy didn't copy")
	}
	ents := tab.Entities()
	if len(ents) != 3 || ents[0].Kind != EntityKind || ents[0].Title != "mock.html" || ents[0].Detail != "alpha · s1" {
		t.Errorf("entities = %+v", ents)
	}
	shot := filepath.Join(f.bravo, "shot.png")
	tab.Select(shot)
	if a, _ := tab.selected(); a.Path != shot {
		t.Errorf("Select = %s", a.Path)
	}
}

func TestWatcherRefreshes(t *testing.T) {
	f := newFixture(t)
	w := newFakeWatcher()
	tab := f.tab(-1, w)
	refresh(t, tab)
	w.mu.Lock()
	roots := w.sets[len(w.sets)-1]
	w.mu.Unlock()
	if !slices.Equal(roots, []string{f.alpha, f.bravo, f.wt}) {
		t.Errorf("watched = %v", roots)
	}
	writeFile(t, filepath.Join(f.bravo, "new.svg"), "x", now)
	w.ch <- struct{}{}
	_, cmd := tab.Update(tab.waitWatch()())
	if cmd == nil {
		t.Fatal("watch event didn't rescan")
	}
	// The batch holds the re-armed wait and the scan; run the scan.
	refresh(t, tab)
	if !slices.Contains(names(tab), "new.svg") {
		t.Errorf("rows = %v", names(tab))
	}
	close(w.ch)
	if _, cmd := tab.Update(tab.waitWatch()()); cmd != nil {
		t.Error("closed watcher re-armed")
	}
}

func TestTruncateLeft(t *testing.T) {
	tests := []struct {
		s    string
		w    int
		want string
	}{
		{"/a/b", 10, "/a/b"},
		{"/aaa/bbb/ccc", 8, "…bbb/ccc"},
		{"/aaa", 1, "…"},
	}
	for _, tt := range tests {
		if got := truncateLeft(tt.s, tt.w); got != tt.want {
			t.Errorf("truncateLeft(%q, %d) = %q, want %q", tt.s, tt.w, got, tt.want)
		}
	}
}

func TestAgo(t *testing.T) {
	if got := ago(10 * time.Second); got != "just now" {
		t.Errorf("ago(10s) = %q", got)
	}
	if got := ago(5 * time.Minute); got != "5m ago" {
		t.Errorf("ago(5m) = %q", got)
	}
}

func TestHumanSize(t *testing.T) {
	tests := map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KB", 1536: "1.5 KB", 5 << 20: "5.0 MB"}
	for n, want := range tests {
		if got := humanSize(n); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", n, got, want)
		}
	}
}

// --- teatest flow ----------------------------------------------------------------

// host runs the tab as a program, standing in for the root model.
type host struct {
	tab *Tab

	mu    sync.Mutex
	frame string
}

func (h *host) Init() tea.Cmd { return h.tab.Init() }

func (h *host) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
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

func (h *host) wait(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.mu.Lock()
		frame := h.frame
		h.mu.Unlock()
		if strings.Contains(frame, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q\nframe:\n%s", want, frame)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestAgentMockupAppears is the P4.5 "done when": an HTML mockup an agent
// writes into its worktree shows up in the gallery through the real
// fsnotify watcher, without a refresh.
func TestAgentMockupAppears(t *testing.T) {
	f := newFixture(t)
	w, err := artfs.NewWatcher(20 * time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	tab := f.tab(-1, w)
	h := &host{tab: tab}
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(120, 30))
	h.wait(t, "mock.html")

	start := time.Now()
	writeFile(t, filepath.Join(f.wt, "design", "checkout-v2.html"), "<h1>v2</h1>", time.Time{})
	h.wait(t, "checkout-v2.html")
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %s", d)
	}
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}
