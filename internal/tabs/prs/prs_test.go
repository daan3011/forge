package prs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
)

var now = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

// fakeGH answers searches by the section query (before BuildQuery's
// additions) and records every query it was sent.
type fakeGH struct {
	mu      sync.Mutex
	results map[string]github.SearchResult // section query -> result
	err     error
	queries []string

	details   map[string]github.PRDetail // PR id -> detail; missing = pr()'s defaults
	detailErr error
	detailed  []string // PR ids PRDetail was called for

	acts   []string             // quick actions called, e.g. "merge acme/x#1 squash"
	actErr error                // every action fails with it
	block  chan struct{}        // when set, actions wait for it to close
	fresh  map[string]github.PR // PR id -> what PR() returns; missing = an error

	diffs     map[string]string // PR id -> diff; missing = sampleDiff
	diffErr   error
	diffCalls []string // PR ids Diff was called for
}

func (f *fakeGH) PRDetail(_ context.Context, repo string, number int) (github.PRDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := fmt.Sprintf("%s#%d", repo, number)
	f.detailed = append(f.detailed, id)
	if f.detailErr != nil {
		return github.PRDetail{}, f.detailErr
	}
	if d, ok := f.details[id]; ok {
		return d, nil
	}
	// The defaults of pr(): approved and mergeable.
	return github.PRDetail{ReviewDecision: github.ReviewApproved, Mergeable: github.Mergeable}, nil
}

func (f *fakeGH) detailCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.detailed)
}

func (f *fakeGH) SearchPRs(_ context.Context, q string, limit int) (github.SearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
	if limit != github.SectionLimit {
		return github.SearchResult{}, fmt.Errorf("limit %d", limit)
	}
	if f.err != nil {
		return github.SearchResult{}, f.err
	}
	for sq, res := range f.results {
		if strings.Contains(q, sq) {
			if i := strings.Index(q, "repo:"); i >= 0 {
				repo := strings.Fields(q[i+len("repo:"):])[0]
				res.PRs = slices.DeleteFunc(slices.Clone(res.PRs), func(p github.PR) bool { return p.Repo != repo })
			}
			return res, nil
		}
	}
	return github.SearchResult{PRs: []github.PR{}}, nil
}

func (f *fakeGH) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.queries)
}

type memKV struct {
	mu sync.Mutex
	m  map[string]store.CacheEntry
}

func newKV() *memKV { return &memKV{m: map[string]store.CacheEntry{}} }

func (s *memKV) Get(_ context.Context, k string) (store.CacheEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[k]
	if !ok {
		return store.CacheEntry{}, store.ErrNotFound
	}
	return e, nil
}

func (s *memKV) Put(_ context.Context, k, kind, project string, v []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = store.CacheEntry{Key: k, Kind: kind, Project: project, Value: v, FetchedAt: now, TTL: ttl}
	return nil
}

func (s *memKV) keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for k := range s.m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func pr(repo string, n int, title string, mod func(*github.PR)) github.PR {
	p := github.PR{
		Repo: repo, Number: n, Title: title, Author: "daan", URL: github.PullURL(repo, n),
		State: github.StateOpen, ReviewDecision: github.ReviewApproved, CI: github.CISuccess,
		Mergeable: github.Mergeable, Additions: 292, Deletions: 34, HeadRef: "feat-" + fmt.Sprint(n), BaseRef: "main",
		CreatedAt: now.Add(-400 * 24 * time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	if mod != nil {
		mod(&p)
	}
	return p
}

func sampleGH() *fakeGH {
	return &fakeGH{results: map[string]github.SearchResult{
		"author:@me": {Viewer: "daan", Total: 3, PRs: []github.PR{
			pr("acme/bubbletea", 1, "feat(examples): tree", func(p *github.PR) { p.CI = github.CIFailure; p.Additions, p.Deletions = 391, 4 }),
			pr("acme/bubbles", 2, "feat: tree bubble", func(p *github.PR) { p.CI = github.CIFailure; p.Additions, p.Deletions = 1200, 2 }),
			pr("acme/lipgloss", 3, "feat(tree): support width", nil),
		}},
		"review-requested:@me": {Viewer: "daan", Total: 0, PRs: []github.PR{}},
		"involves:@me": {Viewer: "daan", Total: 120, PRs: []github.PR{
			pr("acme/bubbles", 9, "fix: viewport", func(p *github.PR) { p.Author = "bob"; p.Draft = true; p.State = github.StateDraft }),
			pr("acme/lipgloss", 3, "feat(tree): support width", nil),
		}},
	}}
}

func testDeps(gh github.PRClient, kv tabs.KVStore) tabkit.Deps {
	d := tabkit.Deps{Theme: ui.NewTheme(true, nil), Keys: ui.DefaultKeyMap(), Store: kv, Config: config.Default()}
	if gh != nil {
		d.GitHub = gh
	}
	return d
}

func newTestTab(gh github.PRClient, kv tabs.KVStore) *Tab {
	t := newTab(testDeps(gh, kv))
	t.now = func() time.Time { return now }
	t.tick = immediateTick
	t.SetSize(120, 30)
	return t
}

// immediateTick fires at once, so drain runs debounced fetches in order.
func immediateTick(_ time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
	return func() tea.Msg { return fn(now) }
}

// run executes cmd and returns its messages, flattening batches.
func run(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, run(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

// drain feeds cmd's messages back into the tab until none are left and
// returns the error toasts.
func drain(tb *Tab, cmd tea.Cmd) []string {
	var errs []string
	queue := run(cmd)
	for len(queue) > 0 {
		msg := queue[0]
		queue = queue[1:]
		if e, ok := msg.(ui.ErrMsg); ok {
			errs = append(errs, e.Err.Error())
			continue
		}
		_, next := tb.Update(msg)
		queue = append(queue, run(next)...)
	}
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
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func send(tb *Tab, keys ...string) []string {
	var errs []string
	for _, k := range keys {
		_, cmd := tb.Update(press(k))
		errs = append(errs, drain(tb, cmd)...)
	}
	return errs
}

func view(tb *Tab) string { return ansi.Strip(tb.View()) }

func mustContain(t *testing.T, v string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(v, w) {
			t.Fatalf("view lacks %q:\n%s", w, v)
		}
	}
}

func mustLack(t *testing.T, v string, nots ...string) {
	t.Helper()
	for _, n := range nots {
		if strings.Contains(v, n) {
			t.Fatalf("view has %q:\n%s", n, v)
		}
	}
}

func TestSectionsFetchAndCount(t *testing.T) {
	gh, kv := sampleGH(), newKV()
	tb := newTestTab(gh, kv)
	if errs := drain(tb, tb.Init()); len(errs) > 0 {
		t.Fatal(errs)
	}
	if got := len(gh.sent()); got != 3 {
		t.Fatalf("sent %d searches, want one per section", got)
	}
	v := view(tb)
	mustContain(t, v,
		"My PRs (3)", "Review requested (0)", "Involved (2)",
		"is:pr is:open author:@me sort:updated-desc",
		"acme/bubbletea · #1", "feat(examples): tree", "+391 -4", "+1.2k -2", "1h", "1y",
		"feat(tree): support width", "main ← feat-1", "Changes approved",
	)
	if got := tb.StatusDetail(); got != "@daan · PR 1/3 (fetched 3)" {
		t.Errorf("status detail = %q", got)
	}
	if got := len(slices.DeleteFunc(kv.keys(), func(k string) bool { return !strings.HasPrefix(k, "tab:") })); got != 3 {
		t.Errorf("cached %d sections: %v", got, kv.keys())
	}
	if lines := strings.Split(tb.View(), "\n"); len(lines) != 30 {
		t.Errorf("view is %d lines, want 30", len(lines))
	}

	send(tb, "j")
	if got := tb.StatusDetail(); got != "@daan · PR 2/3 (fetched 3)" {
		t.Errorf("after j: %q", got)
	}
	send(tb, "l", "l")
	v = view(tb)
	mustContain(t, v, "fix: viewport", "involves:@me")
	mustLack(t, v, "feat(examples): tree")
	if got := tb.StatusDetail(); got != "@daan · PR 1/2 (fetched 2 of 120)" {
		t.Errorf("involved detail = %q", got)
	}
	send(tb, "l") // wraps to the first section
	mustContain(t, view(tb), "author:@me")
	send(tb, "h")
	mustContain(t, view(tb), "involves:@me")
}

// TestRepoFilter is P2.1's done-when: f narrows every section to a repo,
// with matching counts, and "All repos" restores them.
func TestRepoFilter(t *testing.T) {
	tb := newTestTab(sampleGH(), newKV())
	drain(tb, tb.Init())

	send(tb, "f")
	if !tb.CapturingInput() {
		t.Fatal("f should open the repo picker")
	}
	mustContain(t, view(tb), "Show PRs from which repo?", "All repos", "acme/bubbles", "acme/lipgloss")
	// Options: All repos, acme/bubbles, acme/bubbletea, acme/lipgloss.
	send(tb, "down", "enter")
	if tb.CapturingInput() {
		t.Fatal("enter should close the picker")
	}
	v := view(tb)
	mustContain(t, v, "repo: acme/bubbles", "My PRs (1)", "Review requested (0)", "Involved (1)", "feat: tree bubble")
	mustLack(t, v, "feat(examples): tree", "feat(tree): support width")
	send(tb, "l", "l")
	v = view(tb)
	mustContain(t, v, "fix: viewport")
	mustLack(t, v, "feat(tree): support width")

	// Esc leaves the filter as it was.
	send(tb, "f", "esc")
	mustContain(t, view(tb), "repo: acme/bubbles")

	send(tb, "f", "g", "enter") // g = top: All repos
	v = view(tb)
	mustContain(t, v, "My PRs (3)", "Involved (2)", "feat(tree): support width")
	mustLack(t, v, "repo: acme")
}

func TestRepoFilterEmptySection(t *testing.T) {
	tb := newTestTab(sampleGH(), newKV())
	drain(tb, tb.Init())
	send(tb, "l", "l", "f", "down", "enter") // Involved, acme/bubbles
	send(tb, "h", "h")                       // My PRs has acme/bubbles#2
	mustContain(t, view(tb), "feat: tree bubble")
	send(tb, "l") // Review requested is empty anyway: no repo hint
	mustLack(t, view(tb), "no PRs from")
	send(tb, "f", "g", "j", "j", "j", "enter") // acme/lipgloss
	send(tb, "l")                              // Involved has lipgloss#3; review requested none
	send(tb, "f", "g", "j", "j", "enter")      // acme/bubbletea: nothing in Involved
	mustContain(t, view(tb), "no PRs from acme/bubbletea here · f changes the repo")
}

func TestTextFilter(t *testing.T) {
	tb := newTestTab(sampleGH(), newKV())
	drain(tb, tb.Init())
	send(tb, "/", "w", "i", "d", "t", "h", "enter")
	v := view(tb)
	mustContain(t, v, "/ width", "feat(tree): support width")
	mustLack(t, v, "feat: tree bubble")
	// Hidden search text matches branches and authors.
	send(tb, "esc", "/")
	for _, r := range "feat-2" {
		send(tb, string(r))
	}
	v = view(tb)
	mustContain(t, v, "feat: tree bubble")
	mustLack(t, v, "feat(tree): support width")
	send(tb, "esc")
	if tb.CapturingInput() {
		t.Fatal("esc should close the filter")
	}
}

func TestCacheFirst(t *testing.T) {
	kv := newKV()
	first := newTestTab(sampleGH(), kv)
	drain(first, first.Init())

	// A new forge whose fetches fail still shows the cached rows, with one
	// toast for the whole refresh.
	gh := &fakeGH{err: errors.New("boom")}
	tb := newTestTab(gh, kv)
	errs := drain(tb, tb.Init())
	if len(errs) != 1 || !strings.Contains(errs[0], "boom") {
		t.Fatalf("errors = %q, want one toast", errs)
	}
	mustContain(t, view(tb), "My PRs (3)", "feat(examples): tree")
	if !tb.LastUpdated().Equal(now) {
		t.Errorf("updated = %v, want the cache time", tb.LastUpdated())
	}
}

func TestAuthError(t *testing.T) {
	gh := &fakeGH{err: errors.New("github: authentication token not found for host github.com")}
	tb := newTestTab(gh, newKV())
	errs := drain(tb, tb.Init())
	if len(errs) != 1 || !strings.Contains(errs[0], "gh auth login") {
		t.Fatalf("errors = %q", errs)
	}
	mustContain(t, view(tb), "run `gh auth login`")
}

func TestNoGitHub(t *testing.T) {
	tb := newTestTab(nil, newKV())
	drain(tb, tb.Init())
	mustContain(t, view(tb), "GitHub isn't available")
	if tb.Fetching() {
		t.Error("no client, nothing to fetch")
	}
}

func TestActiveProject(t *testing.T) {
	gh, kv := sampleGH(), newKV()
	tb := newTestTab(gh, kv)
	drain(tb, tb.Init())
	send(tb, "f", "down", "enter") // a repo filter is dropped on project switch
	before := tb.sections[0].key

	tb.SetProject(&config.Project{Name: "lip", Repo: "acme/lipgloss"})
	if tb.sections[0].key == before || !strings.Contains(tb.sections[0].key, ":lip:") {
		t.Fatalf("key %q should change with the project", tb.sections[0].key)
	}
	drain(tb, tb.Refresh())
	sent := gh.sent()
	if q := sent[len(sent)-1]; !strings.Contains(q, "repo:acme/lipgloss") {
		t.Errorf("query %q lacks the project repo", q)
	}
	v := view(tb)
	mustContain(t, v, "My PRs (1)", "repo:acme/lipgloss", "feat(tree): support width")
	mustLack(t, v, "repo: acme/bubbles", "feat: tree bubble")

	// A fetch for the old project landing late is dropped.
	_, cmd := tb.Update(tabkit.FetchedMsg{Tab: Title, Key: before, Value: []byte(`{"prs":[]}`), At: now})
	if cmd != nil || len(tb.table.Rows()) != 1 {
		t.Error("stale fetch should be ignored")
	}
}

func TestSectionKeyFollowsQuery(t *testing.T) {
	a := sectionKey(nil, config.PRSection{Title: "My PRs", Query: "is:open author:@me"})
	b := sectionKey(nil, config.PRSection{Title: "My PRs", Query: "is:closed author:@me"})
	if a == b || !strings.HasPrefix(a, "tab:prs:all:my-prs:") {
		t.Errorf("keys %q / %q", a, b)
	}
}

func TestEntitiesAndSelect(t *testing.T) {
	tb := newTestTab(sampleGH(), newKV())
	drain(tb, tb.Init())
	ents := tb.Entities()
	if len(ents) != 4 {
		t.Fatalf("got %d entities, want 4 distinct PRs", len(ents))
	}
	var viewport tabs.Entity
	for _, e := range ents {
		if e.ID == "acme/bubbles#9" {
			viewport = e
		}
	}
	if viewport.Kind != EntityPR || viewport.Title != "#9 fix: viewport" || viewport.Detail != "acme/bubbles · @bob" || viewport.Search != "feat-9" {
		t.Fatalf("entity = %+v", viewport)
	}
	send(tb, "f", "g", "j", "j", "j", "enter") // filter to acme/lipgloss
	tb.Select("acme/bubbles#9")
	if tb.active != 2 || tb.repo != "" || tb.selectedID() != "acme/bubbles#9" {
		t.Fatalf("select: section %d repo %q id %q", tb.active, tb.repo, tb.selectedID())
	}
	// A PR in the current section stays in it.
	tb.Select("acme/lipgloss#3")
	if tb.active != 2 || tb.selectedID() != "acme/lipgloss#3" {
		t.Fatalf("select in place: section %d id %q", tb.active, tb.selectedID())
	}
}

func TestCommands(t *testing.T) {
	tb := newTestTab(sampleGH(), newKV())
	drain(tb, tb.Init())
	byID := map[string]tabs.Command{}
	for _, c := range tb.Commands() {
		byID[c.ID] = c
	}
	for _, id := range []string{"filter", "pr_next_section", "pr_prev_section", "pr_repo_filter", "pr_section_involved", "grow_pane", "shrink_pane"} {
		if _, ok := byID[id]; !ok {
			t.Errorf("missing command %s", id)
		}
	}
	if _, ok := byID["pr_repo_clear"]; ok {
		t.Error("no repo filter, no clear command")
	}
	drain(tb, byID["pr_section_involved"].Run())
	if tb.active != 2 {
		t.Errorf("section command: active %d", tb.active)
	}
	drain(tb, byID["pr_repo_filter"].Run())
	if tb.dialog == nil {
		t.Fatal("repo filter command should open the picker")
	}
	send(tb, "down", "enter")
	var clearCmd tabs.Command
	for _, c := range tb.Commands() {
		if c.ID == "pr_repo_clear" {
			clearCmd = c
		}
	}
	if clearCmd.Run == nil {
		t.Fatal("a repo filter should offer clearing it")
	}
	drain(tb, clearCmd.Run())
	if tb.repo != "" {
		t.Error("clear should drop the repo filter")
	}
}

func TestCompact(t *testing.T) {
	tests := map[int]string{0: "0", 999: "999", 1000: "1k", 1234: "1.2k", 9999: "10k", 12345: "12k", 1_500_000: "1.5m"}
	for n, want := range tests {
		if got := compact(n); got != want {
			t.Errorf("compact(%d) = %q, want %q", n, got, want)
		}
	}
}

// staleGH has one fresh, one stale and one very stale PR (threshold 5d).
func staleGH() *fakeGH {
	gh := sampleGH()
	gh.results["author:@me"] = github.SearchResult{Viewer: "daan", Total: 3, PRs: []github.PR{
		pr("acme/bubbletea", 1, "fresh one", nil),
		pr("acme/bubbles", 2, "stale one", func(p *github.PR) { p.UpdatedAt = now.Add(-6 * 24 * time.Hour) }),
		pr("acme/lipgloss", 3, "ancient one", func(p *github.PR) { p.UpdatedAt = now.Add(-12 * 24 * time.Hour) }),
	}}
	return gh
}

// TestStaleOnly is P2.5's done-when: old PRs are marked and S filters to
// them across sections.
func TestStaleOnly(t *testing.T) {
	tb := newTestTab(staleGH(), newKV())
	drain(tb, tb.Init())
	v := view(tb)
	mustContain(t, v, "fresh one", "stale one", "ancient one", "6d", "1w")
	if got := strings.Count(v, "◷"); got != 3 { // the column header and two markers
		t.Errorf("view has %d clocks, want header + 2:\n%s", got, v)
	}

	send(tb, "S")
	v = view(tb)
	mustContain(t, v, "stale one", "ancient one", "◷ stale only", "My PRs (2)", "Involved (0)")
	mustLack(t, v, "fresh one")
	if got := tb.StatusDetail(); got != "@daan · PR 1/2 (fetched 3)" {
		t.Errorf("status detail = %q", got)
	}
	send(tb, "l", "l")
	mustContain(t, view(tb), "no stale PRs here · S shows all")

	send(tb, "S")
	v = view(tb)
	mustContain(t, v, "Involved (2)", "fix: viewport")
	mustLack(t, v, "stale only")
}

func TestStaleOnlyCommandAndSelect(t *testing.T) {
	tb := newTestTab(staleGH(), newKV())
	drain(tb, tb.Init())
	cmd := func() tabs.Command {
		for _, c := range tb.Commands() {
			if c.ID == "pr_stale_only" {
				return c
			}
		}
		t.Fatal("no pr_stale_only command")
		return tabs.Command{}
	}
	if c := cmd(); c.Title != "PRs: show stale only" || c.Key.Help().Key != "S" {
		t.Fatalf("command = %q key %q", c.Title, c.Key.Help().Key)
	}
	drain(tb, cmd().Run())
	if !tb.staleOnly || cmd().Title != "PRs: show all (not just stale)" {
		t.Fatalf("command should turn stale-only on (title %q)", cmd().Title)
	}
	tb.Select("acme/bubbles#2") // stale: the filter stays
	if !tb.staleOnly || tb.selectedID() != "acme/bubbles#2" {
		t.Fatalf("select stale: staleOnly %v id %q", tb.staleOnly, tb.selectedID())
	}
	tb.Select("acme/bubbletea#1") // fresh: the filter goes
	if tb.staleOnly || tb.selectedID() != "acme/bubbletea#1" {
		t.Fatalf("select fresh: staleOnly %v id %q", tb.staleOnly, tb.selectedID())
	}
}

func TestStaleOff(t *testing.T) {
	d := testDeps(staleGH(), newKV())
	d.Config.GitHub.StaleAfterDays = 0
	tb := newTab(d)
	tb.now = func() time.Time { return now }
	tb.SetSize(120, 30)
	drain(tb, tb.Init())
	if got := strings.Count(view(tb), "◷"); got != 1 {
		t.Errorf("markers off: %d clocks, want just the header", got)
	}
	errs := send(tb, "S")
	if tb.staleOnly || len(errs) != 1 || !strings.Contains(errs[0], "stale_after_days") {
		t.Fatalf("S with markers off: staleOnly %v errs %v", tb.staleOnly, errs)
	}
}
