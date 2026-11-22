package context

import (
	stdctx "context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/cachectl"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
)

var cacheNow = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

type fakeCaches struct {
	mu      sync.Mutex
	report  cachectl.Report
	stats   int
	ops     []cachectl.Op
	applied []cachectl.Plan
	// items is what a plan finds for any op but Vacuum.
	items   []cachectl.Item
	skipped []cachectl.Skip
}

func (f *fakeCaches) Stats(stdctx.Context) (cachectl.Report, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stats++
	return f.report, nil
}

func (f *fakeCaches) Plan(_ stdctx.Context, op cachectl.Op) (cachectl.Plan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops = append(f.ops, op)
	p := cachectl.Plan{Op: op, Items: f.items, Skipped: f.skipped}
	if op.Kind == cachectl.Vacuum {
		p.Items, p.DB = nil, f.report.DB
	}
	return p, nil
}

func (f *fakeCaches) Apply(_ stdctx.Context, p cachectl.Plan) (cachectl.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, p)
	res := cachectl.Result{Op: p.Op, Removed: len(p.Items), Bytes: p.Bytes(), Skipped: p.Skipped}
	if p.Op.Kind == cachectl.Vacuum {
		res.DBBefore, res.DBAfter = 4<<20, 1<<20
	}
	return res, nil
}

func (f *fakeCaches) lastOp() cachectl.Op {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.ops) == 0 {
		return cachectl.Op{Kind: -1}
	}
	return f.ops[len(f.ops)-1]
}

func (f *fakeCaches) appliedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.applied)
}

func newFakeCaches() *fakeCaches {
	day := 24 * time.Hour
	return &fakeCaches{
		report: cachectl.Report{
			At: cacheNow,
			DB: store.DBStats{Path: "/data/forge.db", Bytes: 4 << 20, Free: 3 << 20, WAL: 512},
			Types: []cachectl.TypeStat{
				{Type: cachectl.GitHub, Entries: 3, Bytes: 3 << 10, Oldest: cacheNow.Add(-9 * day), Projects: []cachectl.ProjectStat{
					{Project: "proj", Entries: 2, Bytes: 2 << 10, Oldest: cacheNow.Add(-9 * day)},
					{Project: cachectl.AllProjects, Entries: 1, Bytes: 1 << 10, Oldest: cacheNow.Add(-day)},
				}},
				{Type: cachectl.Plane},
				{Type: cachectl.Transcripts},
				{Type: cachectl.Docs, Entries: 4, Bytes: 900, InMemory: true, Projects: []cachectl.ProjectStat{{Project: "proj", Entries: 4, Bytes: 900}}},
				{Type: cachectl.Artifacts},
				{Type: cachectl.Worktrees, Entries: 2, Bytes: 5 << 20, Orphans: 1, Projects: []cachectl.ProjectStat{{Project: "proj", Entries: 2, Bytes: 5 << 20, Orphans: 1}}},
				{Type: cachectl.Archived},
			},
		},
		items: []cachectl.Item{
			{ID: "a", Label: "a", Bytes: 1 << 10}, {ID: "b", Label: "b", Bytes: 1 << 10}, {ID: "c", Label: "c", Bytes: 1 << 10},
		},
	}
}

func cacheTab(t *testing.T) (*Tab, *fakeCaches) {
	t.Helper()
	tab := newFixture(t).tab(t)
	fc := newFakeCaches()
	tab.caches = fc
	tab.now = func() time.Time { return cacheNow }
	return tab, fc
}

func showCache(t *testing.T, tab *Tab) {
	t.Helper()
	pumpAll(tab, tab.Refresh())
	_, cmd := tab.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	pumpAll(tab, cmd)
	if tab.view != viewCache || !tab.cs.loaded {
		t.Fatal("cache view not showing")
	}
}

func hit(tab *Tab, k tea.KeyPressMsg) []string {
	_, cmd := tab.Update(k)
	toasts, _ := pumpAll(tab, cmd)
	return toasts
}

func press(tab *Tab, s string) []string {
	return hit(tab, tea.KeyPressMsg{Code: []rune(s)[0], Text: s})
}

var (
	enterKey = tea.KeyPressMsg{Code: tea.KeyEnter}
	leftKey  = tea.KeyPressMsg{Code: tea.KeyLeft}
)

func TestCacheViewListAndDrill(t *testing.T) {
	tab, _ := cacheTab(t)
	showCache(t, tab)
	v := view(tab)
	for _, want := range []string{
		"Memory", "Packs", "Cache", "Type", "Entries", "Size", "Oldest",
		"GitHub API", "3.0 KB", "1w", "Docs index", "mem", "Worktrees · 1 orphan", "5.0 MB",
		"forge.db 4.0 MB", "WAL 512 B", "3.0 MB free",
		"3 entries · 3.0 KB · oldest 1w", "forge.db (kv_cache)", "break down by project",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if got := tab.StatusDetail(); !strings.Contains(got, "forge stores") {
		t.Errorf("status = %q", got)
	}

	hit(tab, enterKey)
	v = view(tab)
	for _, want := range []string{"‹ GitHub API", "proj", cachectl.AllProjects, "GitHub API · proj", "clear this type for proj"} {
		if !strings.Contains(v, want) {
			t.Errorf("drilled view lacks %q:\n%s", want, v)
		}
	}
	press(tab, "j")
	if p, _ := tab.selectedProject(); p.Project != cachectl.AllProjects {
		t.Errorf("selected project = %q", p.Project)
	}
	press(tab, "h")
	if tab.cs.drill != "" {
		t.Error("h should go back to the types")
	}

	// A type without entries has no breakdown.
	press(tab, "j")
	hit(tab, enterKey)
	if tab.cs.drill != "" {
		t.Error("drilled into an empty type")
	}

	press(tab, "c")
	if tab.view != viewMemory {
		t.Error("c should go back to memory files")
	}
}

func TestCacheClearAsksFirst(t *testing.T) {
	tab, fc := cacheTab(t)
	showCache(t, tab)
	fc.skipped = []cachectl.Skip{{Label: "x", Reason: "pinned"}}

	press(tab, "x")
	if !tab.CapturingInput() {
		t.Fatal("no confirmation")
	}
	if op := fc.lastOp(); op.Kind != cachectl.Clear || op.Type != cachectl.GitHub || op.Project != "" {
		t.Errorf("op = %+v", op)
	}
	v := view(tab)
	for _, want := range []string{"Clear GitHub API: 3 entries?", "3.0 KB will be freed", "Kept: 1 pinned."} {
		if !strings.Contains(v, want) {
			t.Errorf("confirm lacks %q:\n%s", want, v)
		}
	}
	// The default answer keeps everything.
	hit(tab, enterKey)
	if tab.CapturingInput() || fc.appliedCount() != 0 {
		t.Fatalf("applied without a yes (capturing=%v)", tab.CapturingInput())
	}

	stats := fc.stats
	press(tab, "x")
	hit(tab, leftKey) // Keep -> Clear
	toasts := hit(tab, enterKey)
	if fc.appliedCount() != 1 {
		t.Fatal("not applied after yes")
	}
	if !slices.Contains(toasts, "ok: removed 3 entries, freed 3.0 KB; kept 1 pinned") {
		t.Errorf("toasts = %q", toasts)
	}
	if fc.stats <= stats {
		t.Error("stats not reloaded after clearing")
	}
}

func TestCacheClearForProject(t *testing.T) {
	tab, fc := cacheTab(t)
	showCache(t, tab)
	hit(tab, enterKey)
	press(tab, "x")
	if op := fc.lastOp(); op.Type != cachectl.GitHub || op.Project != "proj" {
		t.Errorf("op = %+v", op)
	}
	if v := view(tab); !strings.Contains(v, "Clear GitHub API for proj: 3 entries?") {
		t.Errorf("confirm:\n%s", v)
	}
}

func TestCachePrune(t *testing.T) {
	tab, fc := cacheTab(t)
	showCache(t, tab)
	press(tab, "P")
	if v := view(tab); !strings.Contains(v, "Prune GitHub API") {
		t.Fatalf("no days form:\n%s", v)
	}
	hit(tab, enterKey) // keep the default 30
	op := fc.lastOp()
	if op.Kind != cachectl.Prune || !op.OlderThan.Equal(cacheNow.Add(-30*24*time.Hour)) {
		t.Fatalf("op = %+v", op)
	}
	if v := view(tab); !strings.Contains(v, "Remove 3 entries from GitHub API older than 30 days?") {
		t.Errorf("confirm:\n%s", v)
	}

	// The docs index has no age.
	tab2, fc2 := cacheTab(t)
	showCache(t, tab2)
	tab2.setCacheCursor(3)
	toasts := press(tab2, "P")
	if len(fc2.ops) != 0 || len(toasts) != 1 || !strings.Contains(toasts[0], "no age to prune by") {
		t.Errorf("docs prune: ops %v, toasts %q", fc2.ops, toasts)
	}
}

func TestCacheNothingToDo(t *testing.T) {
	tab, fc := cacheTab(t)
	showCache(t, tab)
	fc.items, fc.skipped = nil, []cachectl.Skip{{Reason: "dirty"}}
	toasts := press(tab, "o")
	if tab.CapturingInput() {
		t.Fatal("a dialog opened for an empty plan")
	}
	if op := fc.lastOp(); op.Kind != cachectl.Orphans {
		t.Errorf("op = %+v", op)
	}
	if !slices.Contains(toasts, "info: nothing to remove (kept: 1 dirty)") {
		t.Errorf("toasts = %q", toasts)
	}
}

func TestCacheDocsRebuilds(t *testing.T) {
	tab, fc := cacheTab(t)
	showCache(t, tab)
	tab.setCacheCursor(3)
	_, cmd := tab.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	var reindex bool
	for _, m := range drain(cmd) {
		if _, ok := m.(tabs.ReindexDocsMsg); ok {
			reindex = true
		}
	}
	if !reindex || len(fc.ops) != 0 || tab.CapturingInput() {
		t.Errorf("reindex=%v ops=%v capturing=%v", reindex, fc.ops, tab.CapturingInput())
	}
}

func TestCacheVacuum(t *testing.T) {
	tab, fc := cacheTab(t)
	showCache(t, tab)
	press(tab, "V")
	if v := view(tab); !strings.Contains(v, "Vacuum forge.db?") || !strings.Contains(v, "about 3.0 MB is free pages") {
		t.Fatalf("confirm:\n%s", v)
	}
	hit(tab, leftKey)
	toasts := hit(tab, enterKey)
	if fc.appliedCount() != 1 || !slices.Contains(toasts, "ok: vacuumed: 4.0 MB → 1.0 MB") {
		t.Errorf("applied %d, toasts %q", fc.appliedCount(), toasts)
	}
}

func TestCachePaletteSwitchesView(t *testing.T) {
	tab, fc := cacheTab(t)
	pumpAll(tab, tab.Refresh())
	pumpAll(tab, tab.toggleCache()) // measure once
	press(tab, "c")
	for _, c := range tab.Commands() {
		if c.ID == "context_cache_vacuum" {
			pumpAll(tab, c.Run())
		}
	}
	if tab.view != viewCache || fc.lastOp().Kind != cachectl.Vacuum || !tab.CapturingInput() {
		t.Errorf("view=%v op=%+v capturing=%v", tab.view, fc.lastOp(), tab.CapturingInput())
	}
}

func TestCacheWithoutDatabase(t *testing.T) {
	tab := newFixture(t).tab(t)
	pumpAll(tab, tab.Refresh())
	press(tab, "c")
	if v := view(tab); !strings.Contains(v, "Cache controls need forge's database") {
		t.Errorf("view:\n%s", v)
	}
	if toasts := press(tab, "x"); !slices.Contains(toasts, "info: cache controls need forge's database") {
		t.Errorf("toasts = %q", toasts)
	}
}

// TestCacheClearFlow: open the cache view, clear a type, confirm, and see
// the new numbers.
func TestCacheClearFlow(t *testing.T) {
	tab, fc := cacheTab(t)
	h := &host{tab: tab}
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(120, 30))
	waitFor(t, h, "Total ≈")
	tm.Send(tea.KeyPressMsg{Code: 'c', Text: "c"})
	waitFor(t, h, "GitHub API", "forge.db 4.0 MB")

	fc.mu.Lock()
	fc.report.Types[0] = cachectl.TypeStat{Type: cachectl.GitHub} // what Stats says after clearing
	fc.mu.Unlock()
	tm.Send(tea.KeyPressMsg{Code: 'x', Text: "x"})
	waitFor(t, h, "Clear GitHub API: 3 entries?")
	tm.Send(leftKey)
	time.Sleep(50 * time.Millisecond)
	tm.Send(enterKey)
	waitFor(t, h, "0 entries · 0 B")
	if fc.appliedCount() != 1 {
		t.Errorf("applied %d times", fc.appliedCount())
	}

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}
