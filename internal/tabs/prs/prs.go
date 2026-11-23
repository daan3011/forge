package prs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/daan3011/forge/internal/browser"
	"github.com/daan3011/forge/internal/checkout"
	"github.com/daan3011/forge/internal/clipboard"
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/delta"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/refresh"
	"github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
)

// Title is the tab's title, which also routes its fetch results.
const Title = "PRs"

// EntityPR is the palette group of PRs.
const EntityPR = "PR"

// listRatio is the list's starting share of the width: the PR columns and
// the query need room; the rest goes to the detail pane (< / > move it).
const listRatio = 0.65

// searchBoxHeight is the bordered query box above the table.
const searchBoxHeight = 3

// section is one configured PR search and what it last returned.
type section struct {
	cfg        config.PRSection
	key        string // kv_cache key for the current project
	res        github.SearchResult
	loaded     bool // res holds data (cached or fetched)
	cacheAsked string
	fetching   bool
	fresh      bool // a fetch for key has landed; ignore older cached data
	updated    time.Time
	err        error // the last fetch failed; nil once one succeeds
}

// Tab is the PRs tab: one GitHub search per configured section, shown as
// sub-tabs with counts.
type Tab struct {
	theme *ui.Theme
	keys  *ui.KeyMap
	store tabs.KVStore
	gh    github.PRClient
	anim  *anim.Engine
	// detailLoading is set by the last View when the detail pane showed
	// the loading hammer, so Animating keeps it moving.
	detailLoading bool
	cfg           *config.Config
	now           func() time.Time
	// sessions finds the forge sessions linked to a PR; nil shows none.
	sessions sessions.API
	// tick arms the detail debounce (tea.Tick; tests replace it).
	tick func(time.Duration, func(time.Time) tea.Msg) tea.Cmd
	// Quick actions (P2.3); each may be nil.
	browser  browser.Opener
	clip     clipboard.Copier
	checkout checkout.Checker

	split   ui.SplitPane
	divider anim.Spring
	sliding bool
	table   ui.Table
	filter  ui.Filter
	width   int
	height  int

	project  *config.Project
	sections []*section
	active   int
	repo     string // repo filter ("owner/name"); "" = all repos
	// staleOnly hides PRs updated within github.stale_after_days (P2.5).
	staleOnly bool
	dialog    *huh.Form
	onDone    func() tea.Cmd // runs when the dialog is completed
	authErr   bool           // the last fetch failed for want of a gh login
	errShown  bool           // a fetch error was toasted since the last Refresh

	det detailState // the detail pane (P2.2)

	busy       map[string]string    // PR id -> running action, e.g. "merging #12"
	patched    map[string]github.PR // PR id -> copy re-fetched after an action
	lastMethod string               // merge method last used

	// The diff viewer (P2.4); nil when closed.
	diff        *diffView
	diffID      int // bumps per opened viewer
	diffs       diffCache
	delta       delta.Renderer
	deltaWarned bool // a delta failure was toasted once

	// runs downloads job logs for "fix this" (P2.8); nil leaves them out.
	runs github.RunsClient

	// plane finds work item keys and posts back-link comments (P3.7); nil
	// links nothing.
	plane plane.API
	bl    backlinkState

	// wanted is an action another tab asked for (tabs.PRActionMsg, P6.1)
	// on a PR no section has loaded yet; nil when none.
	wanted *pendingAction
}

var (
	_ tabs.Tab            = (*Tab)(nil)
	_ tabs.InputCapturer  = (*Tab)(nil)
	_ tabs.Commander      = (*Tab)(nil)
	_ tabs.EntityProvider = (*Tab)(nil)
	_ tabs.Updater        = (*Tab)(nil)
	_ tabs.Fetcher        = (*Tab)(nil)
	_ tabs.AutoRefresher  = (*Tab)(nil)
	_ tabs.Animator       = (*Tab)(nil)
	_ tabs.StatusDetailer = (*Tab)(nil)
	_ tabs.Busier         = (*Tab)(nil)
	_ tabs.PRActioner     = (*Tab)(nil)
)

// New returns the PRs tab. Init loads every section cache-first.
func New(d tabkit.Deps) tabs.Tab { return newTab(d) }

func newTab(d tabkit.Deps) *Tab {
	cfg := d.Config
	if cfg == nil {
		cfg = config.Default()
	}
	t := &Tab{
		theme: d.Theme, keys: d.Keys, store: d.Store, gh: d.GitHub, anim: d.Engine(), cfg: cfg,
		now:      time.Now,
		sessions: d.Sessions,
		tick:     tea.Tick,
		browser:  d.Browser, clip: d.Clipboard, checkout: d.Checkout, delta: d.Delta,
		runs: d.Runs, plane: d.Plane,
		busy: map[string]string{}, patched: map[string]github.PR{},
		split:  ui.NewSplitPane(d.Theme),
		filter: ui.NewFilter(d.Theme, d.Keys),
		det:    newDetailState(),
	}
	t.split.SetRatio(listRatio)
	t.table = ui.NewTable(d.Theme, d.Keys, t.columns())
	t.table.SetRowHeight(rowHeight)
	t.table.SetEmptyView(t.emptyView)
	for _, sc := range cfg.GitHub.PRSections {
		t.sections = append(t.sections, &section{cfg: sc})
	}
	t.setKeys()
	return t
}

// sectionKey is the cache key of a section for a project. The query's hash
// is part of it, so editing a section's query never shows stale results.
func sectionKey(p *config.Project, sc config.PRSection) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(sc.Query))
	return fmt.Sprintf("%s:%s:%08x", tabkit.CacheKey(Title, p), slug(sc.Title), h.Sum32())
}

func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

func (t *Tab) setKeys() {
	for _, s := range t.sections {
		s.key = sectionKey(t.project, s.cfg)
	}
}

// query is the search a section runs: its query, limited to the active
// project's repo.
func (t *Tab) query(s *section) string {
	repo := ""
	if t.project != nil {
		repo = t.project.Repo
	}
	return github.BuildQuery(s.cfg.Query, repo)
}

func (t *Tab) cur() *section {
	if len(t.sections) == 0 {
		return nil
	}
	return t.sections[t.active]
}

// Init implements tabs.Tab.
func (t *Tab) Init() tea.Cmd { return t.Refresh() }

// Refresh implements tabs.Tab: every section loads its cached copy once per
// key, and every section not already fetching fetches, so all counts fill.
func (t *Tab) Refresh() tea.Cmd {
	t.errShown = false
	var cmds []tea.Cmd
	for _, s := range t.sections {
		if s.cacheAsked != s.key {
			s.cacheAsked = s.key
			cmds = append(cmds, tabkit.LoadCached(t.store, Title, s.key))
		}
		if s.fetching || t.gh == nil {
			continue
		}
		s.fetching = true
		gh, q := t.gh, t.query(s)
		cmds = append(cmds, tabkit.FetchAndCache(t.store, tabkit.Fetch{
			Tab: Title, Key: s.key, Project: t.project, Now: t.now,
			Run: func(ctx context.Context) ([]byte, error) {
				res, err := gh.SearchPRs(ctx, q, github.SectionLimit)
				if err != nil {
					return nil, err
				}
				return json.Marshal(res)
			},
		}))
	}
	return tea.Batch(cmds...)
}

func (t *Tab) byKey(k string) *section {
	for _, s := range t.sections {
		if s.key == k {
			return s
		}
	}
	return nil
}

// Update implements tabs.Tab. After every message the detail pane catches
// up with the selection: a new PR arms the debounced fetch, a new width
// re-renders Markdown.
func (t *Tab) Update(msg tea.Msg) (tabs.Tab, tea.Cmd) {
	cmd := t.update(msg)
	return t, tea.Batch(cmd, t.syncDetail(), t.ensureRendered(), t.ensureDiffRendered())
}

func (t *Tab) update(msg tea.Msg) tea.Cmd {
	if cmd, ok := t.updateDetail(msg); ok {
		return cmd
	}
	if cmd, ok := t.updateActions(msg); ok {
		return cmd
	}
	if cmd, ok := t.updateDiffMsg(msg); ok {
		return cmd
	}
	switch msg := msg.(type) {
	case tabkit.CacheLoadedMsg:
		t.cacheLoaded(msg)
		return t.runPending()
	case tabkit.FetchedMsg:
		cmd := t.fetched(msg)
		return tea.Batch(cmd, t.runPending())
	case backlinkedMsg:
		return t.backlinked(msg)
	}
	if t.dialog != nil {
		return t.updateDialog(msg)
	}
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	if t.diff != nil {
		return t.updateDiff(kp)
	}
	if t.filter.Active() {
		t.updateFilter(kp)
		return nil
	}
	l, pk := t.keys.List, t.keys.PRs
	switch {
	case key.Matches(kp, l.Filter):
		t.startFilter()
	case key.Matches(kp, t.keys.Input.Cancel) && t.filter.Value() != "":
		t.clearFilter()
	case key.Matches(kp, pk.NextSection):
		t.switchSection(t.active + 1)
	case key.Matches(kp, pk.PrevSection):
		t.switchSection(t.active - 1)
	case key.Matches(kp, pk.RepoFilter):
		return t.openRepoFilter()
	case key.Matches(kp, pk.StaleOnly):
		return t.toggleStale()
	case key.Matches(kp, pk.DetailNext):
		t.setDetailTab(t.det.tab + 1)
	case key.Matches(kp, pk.DetailPrev):
		t.setDetailTab(t.det.tab - 1)
	case key.Matches(kp, pk.DetailDown):
		t.scrollDetail(1)
	case key.Matches(kp, pk.DetailUp):
		t.scrollDetail(-1)
	case key.Matches(kp, l.GrowPane):
		t.resize(t.split.Grow)
	case key.Matches(kp, l.ShrinkPane):
		t.resize(t.split.Shrink)
	case key.Matches(kp, pk.Open):
		return t.openInBrowser()
	case key.Matches(kp, pk.CopyURL):
		return t.copyURL()
	case key.Matches(kp, pk.Checkout):
		return t.checkoutPR()
	case key.Matches(kp, pk.UpdateBranch):
		return t.updateBranch()
	case key.Matches(kp, pk.Merge):
		return t.openMerge()
	case key.Matches(kp, pk.AutoMerge):
		return t.openAutoMerge()
	case key.Matches(kp, pk.ToggleDraft):
		return t.toggleDraft()
	case key.Matches(kp, pk.Session):
		return t.startSession()
	case key.Matches(kp, pk.Diff):
		return t.openDiff()
	case key.Matches(kp, pk.Runs):
		return t.showRuns()
	case key.Matches(kp, pk.Fix):
		return t.fix(false)
	case key.Matches(kp, pk.FixEdit):
		return t.fix(true)
	default:
		return t.table.Update(kp)
	}
	return nil
}

func (t *Tab) cacheLoaded(msg tabkit.CacheLoadedMsg) {
	s := t.byKey(msg.Key)
	if s == nil || s.fresh {
		return
	}
	var res github.SearchResult
	if json.Unmarshal(msg.Value, &res) != nil {
		return // unreadable cache: wait for the fetch
	}
	t.applyPatches(&res)
	s.res, s.loaded, s.updated = res, true, msg.FetchedAt
	t.rowsChanged(s)
}

func (t *Tab) fetched(msg tabkit.FetchedMsg) tea.Cmd {
	s := t.byKey(msg.Key)
	if s == nil {
		return nil // fetched for a project we have since left
	}
	s.fetching = false
	s.err = msg.Err
	if msg.Err != nil {
		t.authErr = github.IsAuthError(msg.Err)
		if t.errShown {
			return nil // one toast per refresh, not one per section
		}
		t.errShown = true
		err := msg.Err
		if t.authErr {
			err = errors.New("GitHub: not logged in; run `gh auth login`")
		}
		return errCmd(ui.WithRetry(err, t.keys))
	}
	var res github.SearchResult
	if err := json.Unmarshal(msg.Value, &res); err != nil {
		return errCmd(fmt.Errorf("prs: %w", err))
	}
	t.authErr = false
	t.applyPatches(&res)
	s.res, s.loaded, s.fresh, s.updated = res, true, true, msg.At
	t.rowsChanged(s)
	bl := t.backlink(res.PRs)
	if msg.CacheErr != nil {
		return tea.Batch(bl, errCmd(msg.CacheErr))
	}
	return bl
}

func errCmd(err error) tea.Cmd {
	return func() tea.Msg { return ui.ErrMsg{Err: err} }
}

// shownPRs is a section's PRs after the repo and stale-only filters.
func (t *Tab) shownPRs(s *section) []github.PR {
	if t.repo == "" && !t.staleOnly {
		return s.res.PRs
	}
	var out []github.PR
	for _, p := range s.res.PRs {
		if t.shows(p) {
			out = append(out, p)
		}
	}
	return out
}

// shows reports whether p passes the repo and stale-only filters.
func (t *Tab) shows(p github.PR) bool { return t.inRepo(p) && (!t.staleOnly || t.isStale(p)) }

// inRepo reports whether p passes the repo filter.
func (t *Tab) inRepo(p github.PR) bool { return t.repo == "" || strings.EqualFold(p.Repo, t.repo) }

// staleDays is github.stale_after_days; 0 means no stale markers.
func (t *Tab) staleDays() int { return t.cfg.GitHub.StaleAfterDays }

func (t *Tab) isStale(p github.PR) bool {
	return staleLevel(p.UpdatedAt, t.now(), t.staleDays()) != fresh
}

// toggleStale switches the stale-only filter, which applies to every
// section. With the markers off there is nothing to filter on.
func (t *Tab) toggleStale() tea.Cmd {
	if !t.staleOnly && t.staleDays() <= 0 {
		return errCmd(errors.New("stale markers are off (github.stale_after_days is 0)"))
	}
	t.staleOnly = !t.staleOnly
	t.table.SetRows(nil)
	t.setRows()
	return nil
}

// rowsChanged re-lays the table if s is the section on screen.
func (t *Tab) rowsChanged(s *section) {
	if s == t.cur() {
		t.setRows()
	}
}

// setRows fills the table from the current section, keeping the selected
// PR selected when it is still there.
func (t *Tab) setRows() {
	sel := t.selectedID()
	var rows [][]string
	if s := t.cur(); s != nil {
		now, days := t.now(), t.staleDays()
		for _, p := range t.shownPRs(s) {
			rows = append(rows, prRow(p, now, days))
		}
	}
	t.table.SetRows(rows)
	if sel != "" {
		t.selectID(sel)
	}
}

func (t *Tab) selectedID() string {
	if row := t.table.SelectedRow(); row != nil {
		return row[colID]
	}
	return ""
}

func (t *Tab) selectID(id string) {
	for i, row := range t.table.VisibleRows() {
		if row[colID] == id {
			t.table.SetCursor(i)
			return
		}
	}
}

// selected is the PR under the cursor.
func (t *Tab) selected() (github.PR, bool) {
	id, s := t.selectedID(), t.cur()
	if id == "" || s == nil {
		return github.PR{}, false
	}
	for _, p := range s.res.PRs {
		if p.ID() == id {
			return p, true
		}
	}
	return github.PR{}, false
}

func (t *Tab) switchSection(i int) {
	n := len(t.sections)
	if n == 0 {
		return
	}
	t.active = (i%n + n) % n
	t.table.SetRows(nil) // a new list starts at the top
	t.setRows()
}

// --- filter, repo filter & layout --------------------------------------------

func (t *Tab) startFilter() {
	t.filter.Start()
	t.resizeTable()
}

func (t *Tab) updateFilter(msg tea.KeyPressMsg) {
	in := t.keys.Input
	switch {
	case key.Matches(msg, in.Accept):
		t.filter.Stop()
		if t.filter.Value() == "" {
			t.resizeTable()
		}
	case key.Matches(msg, in.Cancel):
		t.clearFilter()
	default:
		if t.filter.Update(msg) {
			t.table.SetFilter(t.filter.Value())
		}
	}
}

func (t *Tab) clearFilter() {
	t.filter.Clear()
	t.table.SetFilter("")
	t.resizeTable()
}

// repoChoices are the repos the repo filter offers: those in any loaded
// section plus the configured projects', sorted.
func (t *Tab) repoChoices() []string {
	seen := map[string]bool{}
	var out []string
	add := func(r string) {
		if r != "" && !seen[strings.ToLower(r)] {
			seen[strings.ToLower(r)] = true
			out = append(out, r)
		}
	}
	for _, s := range t.sections {
		for _, p := range s.res.PRs {
			add(p.Repo)
		}
	}
	if t.project != nil {
		add(t.project.Repo)
	} else {
		for _, p := range t.cfg.Projects {
			add(p.Repo)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	return out
}

// allRepos is the repo filter's "no filter" choice.
const allRepos = ""

func (t *Tab) openRepoFilter() tea.Cmd {
	picked := t.repo
	opts := []huh.Option[string]{huh.NewOption("All repos", allRepos)}
	for _, r := range t.repoChoices() {
		opts = append(opts, huh.NewOption(r, r))
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Key("repo").
			Title("Show PRs from which repo?").
			Description("applies to every section · / searches").
			Options(opts...).Value(&picked),
	))
	return t.openDialog(form, func() tea.Cmd { t.setRepo(picked); return nil })
}

// openDialog shows form over the tab; done runs once it is completed (not
// when it is aborted).
func (t *Tab) openDialog(form *huh.Form, done func() tea.Cmd) tea.Cmd {
	t.dialog = form.WithTheme(t.theme.Huh()).WithKeyMap(t.keys.HuhKeyMap()).WithShowHelp(false)
	t.onDone = done
	init := t.dialog.Init()
	t.sizeDialog()
	return init
}

func (t *Tab) updateDialog(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		return nil // the app owns the window size; SetSize resizes the form
	}
	_, cmd := t.dialog.Update(msg)
	switch t.dialog.State {
	case huh.StateCompleted:
		done := t.onDone
		t.dialog, t.onDone = nil, nil
		if done == nil {
			return nil
		}
		return done()
	case huh.StateAborted:
		t.dialog, t.onDone = nil, nil
		return nil
	}
	return cmd
}

// setRepo narrows every section to repo ("" = all repos).
func (t *Tab) setRepo(repo string) {
	t.repo = repo
	t.table.SetRows(nil)
	t.setRows()
}

func (t *Tab) dialogSize() (w, h int) {
	return max(t.width-4, 10), max(t.height-3, 3)
}

func (t *Tab) sizeDialog() {
	if t.dialog == nil {
		return
	}
	w, h := t.dialogSize()
	_, _ = t.dialog.Update(tea.WindowSizeMsg{Width: w, Height: h})
}

func (t *Tab) resize(change func()) {
	from := t.split.ShownRatio()
	change()
	t.divider = anim.NewSpring(from, t.split.Ratio(), t.anim.Frame(), t.anim.Settings())
	t.sliding = true
	t.slide()
}

// slide lays the panes out at the divider's position for the current frame.
func (t *Tab) slide() {
	if !t.sliding {
		return
	}
	pos, settled := t.divider.At(t.anim.Frame())
	if settled {
		pos, t.sliding = t.split.Ratio(), false
	}
	if pos != t.split.ShownRatio() {
		t.split.ShowRatio(pos)
		t.resizeTable()
	}
}

// Animating implements tabs.Animator: true while the divider springs.
func (t *Tab) Animating() bool {
	if t.diff != nil && !t.diff.loaded && t.dialog == nil && t.anim.Settings().Motion() {
		return true // the hammer while fetching the diff
	}
	if (t.loading() || t.detailLoading) && anim.Ticks(t.anim.Settings()) {
		return true // the hammer while the section or a PR's details load
	}
	return t.sliding && !t.divider.Settled(t.anim.Frame())
}

// loading reports whether the current section waits for its first data.
func (t *Tab) loading() bool {
	s := t.cur()
	return s != nil && !s.loaded && (s.err == nil || s.fetching) && t.gh != nil && !t.authErr
}

// SetSize implements tabs.Tab. The section bar takes the first row.
func (t *Tab) SetSize(width, height int) {
	t.width, t.height = width, height
	t.split.SetSize(width, max(height-1, 0))
	t.resizeTable()
	t.sizeDialog()
	t.layoutDiff()
}

func (t *Tab) resizeTable() {
	w, h := t.split.LeftSize()
	h -= searchBoxHeight
	if t.filter.Visible() {
		h--
	}
	t.table.SetSize(w, max(h, 0))
}

// SetProject implements tabs.Tab: new cache keys and queries (with repo:),
// no rows until Refresh loads them, and no repo filter.
func (t *Tab) SetProject(p *config.Project) {
	t.project = p
	changed := false
	for _, s := range t.sections {
		if k := sectionKey(p, s.cfg); k != s.key {
			changed = true
			*s = section{cfg: s.cfg, key: k}
		}
	}
	if changed {
		t.repo, t.staleOnly = "", false
		t.table.SetRows(nil)
	}
}

// --- view ----------------------------------------------------------------------

// View implements tabs.Tab.
func (t *Tab) View() string {
	t.detailLoading = false // set again if the detail draws the hammer
	t.slide()
	bar := ui.SectionBar(t.theme, t.sectionTitles(), t.counts(), t.active, t.width)
	if t.dialog != nil {
		w, h := t.dialogSize()
		return bar + "\n" + t.theme.PaneFocused.Render(ui.Box(t.dialog.View(), w, h))
	}
	if t.diff != nil {
		return bar + "\n" + t.diffViewString()
	}
	lw, _ := t.split.LeftSize()
	list := t.searchBox(lw)
	if t.filter.Visible() {
		list += "\n" + t.filter.View(lw)
	}
	list += "\n" + t.table.View()
	return bar + "\n" + t.split.View(list, t.detail(), true)
}

func (t *Tab) sectionTitles() []string {
	out := make([]string, len(t.sections))
	for i, s := range t.sections {
		out[i] = s.cfg.Title
	}
	return out
}

// counts are the sections' PR counts after the repo filter; -1 while a
// section has nothing loaded.
func (t *Tab) counts() []int {
	out := make([]int, len(t.sections))
	for i, s := range t.sections {
		out[i] = -1
		if s.loaded {
			out[i] = len(t.shownPRs(s))
		}
	}
	return out
}

// searchBox is the bordered box with the section's query, as sent.
func (t *Tab) searchBox(width int) string {
	inner := max(width-4, 1) // border and padding
	text := ""
	if s := t.cur(); s != nil {
		text = t.query(s)
	}
	line := t.theme.SearchIcon.Render("⌕ ") + t.theme.SearchQuery.Render(text)
	if t.repo != "" {
		line += t.theme.SectionSep.Render(" · ") + t.theme.SearchIcon.Render("repo: ") + t.theme.Body.Render(t.repo)
	}
	if t.staleOnly {
		line += t.theme.SectionSep.Render(" · ") + t.theme.PRStale.Render("◷ stale only")
	}
	return t.theme.SearchBox.Render(ui.Box(line, inner, 1))
}

func (t *Tab) emptyView(w, h int) string {
	th := t.theme
	msg := ""
	switch {
	case len(t.sections) == 0:
		msg = "no PR sections configured (github.pr_sections)"
	case t.gh == nil:
		msg = "GitHub isn't available"
	case t.authErr:
		msg = "not logged in to GitHub: run `gh auth login`"
	case t.cur() != nil && !t.cur().loaded && t.cur().err != nil && !t.cur().fetching:
		return anim.Failed("couldn't load PRs: "+github.Reason(t.cur().err), ui.RetryKey(t.keys), th, w, h)
	case t.loading():
		return anim.Loading("heating up…", t.anim.Frame(), t.anim.Settings(), th, w, h)
	case t.staleOnly && t.cur() != nil && slices.ContainsFunc(t.cur().res.PRs, t.inRepo):
		msg = "no stale PRs here · " + t.keys.PRs.StaleOnly.Help().Key + " shows all"
	case t.repo != "" && t.cur() != nil && len(t.cur().res.PRs) > 0:
		msg = "no PRs from " + t.repo + " here · " + t.keys.PRs.RepoFilter.Help().Key + " changes the repo"
	}
	if msg == "" {
		return anim.EmptyForge(w, h, th)
	}
	return ui.Box(th.TableEmpty.Render(msg), w, h)
}

// --- interfaces ----------------------------------------------------------------

// Title implements tabs.Tab.
func (t *Tab) Title() string { return Title }

// ShortHelp implements tabs.Tab.
func (t *Tab) ShortHelp() []key.Binding {
	switch {
	case t.dialog != nil:
		return t.keys.FormHelp()
	case t.diff != nil:
		return t.diffHelp()
	case t.filter.Active():
		return []key.Binding{t.keys.Input.Cancel, t.keys.Input.Accept}
	}
	pk := t.keys.PRs
	return []key.Binding{pk.PrevSection, pk.NextSection, pk.DetailPrev, pk.DetailNext, pk.RepoFilter, pk.StaleOnly, t.keys.List.Filter, pk.Diff, pk.Open, pk.Checkout, pk.Merge}
}

// FullHelp implements tabs.Tab.
func (t *Tab) FullHelp() [][]key.Binding {
	l, in, pk, df := t.keys.List, t.keys.Input, t.keys.PRs, t.keys.Diff
	return [][]key.Binding{
		{l.Up, l.Down, l.Top, l.Bottom, l.PageDown, l.PageUp},
		{pk.NextSection, pk.PrevSection, l.Filter, pk.RepoFilter, pk.StaleOnly, l.ShrinkPane, l.GrowPane},
		{pk.DetailNext, pk.DetailPrev, pk.DetailDown, pk.DetailUp},
		{pk.Open, pk.CopyURL, pk.Checkout, pk.Session, pk.Diff, pk.Runs, pk.Fix, pk.FixEdit},
		{df.Down, df.Up, df.PageDown, df.PageUp, df.Top, df.Bottom, df.NextFile, df.PrevFile},
		{df.Approve, df.RequestChanges, df.Comment, df.Open, df.Close},
		{pk.UpdateBranch, pk.Merge, pk.AutoMerge, pk.ToggleDraft},
		{in.Accept, in.Cancel, in.DeleteBack, in.ClearInput},
	}
}

// Commands implements tabs.Commander.
func (t *Tab) Commands() []tabs.Command {
	l, pk := t.keys.List, t.keys.PRs
	// Commands run outside Update, so they catch the detail pane up here.
	do := func(f func()) func() tea.Cmd { return func() tea.Cmd { f(); return t.syncDetail() } }
	var cmds []tabs.Command
	if t.diff != nil {
		cmds = t.diffCommands()
	}
	reviewCmd := func(id, title, event string, k key.Binding) tabs.Command {
		c := tabs.Command{ID: id, Title: title, Run: func() tea.Cmd { return t.openReview(event) }}
		if t.diff != nil {
			c.Key = k // the review keys only work in the viewer
		}
		return c
	}
	df := t.keys.Diff
	cmds = append(cmds,
		tabs.Command{ID: "pr_diff", Title: "PR: review diff", Key: pk.Diff, Run: t.openDiff},
		reviewCmd("diff_approve", "PR: approve…", github.ReviewEventApprove, df.Approve),
		reviewCmd("diff_request_changes", "PR: request changes…", github.ReviewEventRequestChanges, df.RequestChanges),
		reviewCmd("diff_comment", "PR: comment…", github.ReviewEventComment, df.Comment),
	)
	cmds = append(cmds, []tabs.Command{
		{ID: "filter", Title: "Filter PRs", Key: l.Filter, Run: do(t.startFilter)},
		{ID: "pr_next_section", Title: "PRs: next section", Key: pk.NextSection, Run: do(func() { t.switchSection(t.active + 1) })},
		{ID: "pr_prev_section", Title: "PRs: previous section", Key: pk.PrevSection, Run: do(func() { t.switchSection(t.active - 1) })},
		{ID: "pr_repo_filter", Title: "PRs: filter by repo…", Key: pk.RepoFilter, Run: t.openRepoFilter},
	}...)
	staleTitle := "PRs: show stale only"
	if t.staleOnly {
		staleTitle = "PRs: show all (not just stale)"
	}
	cmds = append(cmds, tabs.Command{ID: "pr_stale_only", Title: staleTitle, Key: pk.StaleOnly, Run: func() tea.Cmd {
		cmd := t.toggleStale()
		return tea.Batch(cmd, t.syncDetail())
	}})
	if t.repo != "" {
		cmds = append(cmds, tabs.Command{ID: "pr_repo_clear", Title: "PRs: show all repos", Run: do(func() { t.setRepo(allRepos) })})
	}
	for i, s := range t.sections {
		cmds = append(cmds, tabs.Command{ID: "pr_section_" + slug(s.cfg.Title), Title: "PRs: " + s.cfg.Title, Run: do(func() { t.switchSection(i) })})
	}
	return append(cmds,
		tabs.Command{ID: "grow_pane", Title: "Widen list", Key: l.GrowPane, Run: do(func() { t.resize(t.split.Grow) })},
		tabs.Command{ID: "shrink_pane", Title: "Narrow list", Key: l.ShrinkPane, Run: do(func() { t.resize(t.split.Shrink) })},
		tabs.Command{ID: "pr_detail_next", Title: "PRs: next detail tab", Key: pk.DetailNext, Run: do(func() { t.setDetailTab(t.det.tab + 1) })},
		tabs.Command{ID: "pr_detail_prev", Title: "PRs: previous detail tab", Key: pk.DetailPrev, Run: do(func() { t.setDetailTab(t.det.tab - 1) })},
		tabs.Command{ID: "pr_detail_overview", Title: "PRs: show overview", Run: do(func() { t.setDetailTab(tabOverview) })},
		tabs.Command{ID: "pr_detail_checks", Title: "PRs: show checks", Run: do(func() { t.setDetailTab(tabChecks) })},
		tabs.Command{ID: "pr_detail_activity", Title: "PRs: show activity", Run: do(func() { t.setDetailTab(tabActivity) })},
		tabs.Command{ID: "pr_detail_down", Title: "PRs: scroll detail down", Key: pk.DetailDown, Run: do(func() { t.scrollDetail(1) })},
		tabs.Command{ID: "pr_detail_up", Title: "PRs: scroll detail up", Key: pk.DetailUp, Run: do(func() { t.scrollDetail(-1) })},
		tabs.Command{ID: "pr_open", Title: "PR: open in browser", Key: pk.Open, Run: t.openInBrowser},
		tabs.Command{ID: "pr_copy_url", Title: "PR: copy URL", Key: pk.CopyURL, Run: t.copyURL},
		tabs.Command{ID: "pr_checkout", Title: "PR: check out locally", Key: pk.Checkout, Run: t.checkoutPR},
		tabs.Command{ID: "pr_update_branch", Title: "PR: update branch", Key: pk.UpdateBranch, Run: t.updateBranch},
		tabs.Command{ID: "pr_merge", Title: "PR: merge…", Key: pk.Merge, Run: t.openMerge},
		tabs.Command{ID: "pr_auto_merge", Title: "PR: enable auto-merge…", Key: pk.AutoMerge, Run: t.openAutoMerge},
		tabs.Command{ID: "pr_toggle_draft", Title: "PR: toggle draft / ready", Key: pk.ToggleDraft, Run: t.toggleDraft},
		tabs.Command{ID: "pr_session", Title: "PR: start a forge session", Key: pk.Session, Run: t.startSession},
		tabs.Command{ID: "pr_runs", Title: "PR: show pipeline runs", Key: pk.Runs, Run: t.showRuns},
		tabs.Command{ID: "pr_fix", Title: "CI: fix this (failing checks)", Key: pk.Fix, Run: func() tea.Cmd { return t.fix(false) }},
		tabs.Command{ID: "pr_fix_edit", Title: "CI: fix this (edit prompt)…", Key: pk.FixEdit, Run: func() tea.Cmd { return t.fix(true) }},
	)
}

// Entities implements tabs.EntityProvider: every loaded PR once, keyed by
// "owner/repo#n".
func (t *Tab) Entities() []tabs.Entity {
	seen := map[string]bool{}
	var out []tabs.Entity
	for _, s := range t.sections {
		for _, p := range s.res.PRs {
			if seen[p.ID()] {
				continue
			}
			seen[p.ID()] = true
			out = append(out, tabs.Entity{
				Kind: EntityPR, ID: p.ID(),
				Title:  fmt.Sprintf("#%d %s", p.Number, p.Title),
				Detail: strings.TrimSuffix(p.Repo+" · @"+p.Author, " · @"),
				Search: p.HeadRef,
			})
		}
	}
	return out
}

// Select implements tabs.EntityProvider: it opens the first section with
// the PR (the current one if it has it), dropping filters that hide it.
func (t *Tab) Select(id string) tea.Cmd {
	order := make([]int, 0, len(t.sections))
	order = append(order, t.active)
	for i := range t.sections {
		if i != t.active {
			order = append(order, i)
		}
	}
	for _, i := range order {
		at := slices.IndexFunc(t.sections[i].res.PRs, func(p github.PR) bool { return p.ID() == id })
		if at < 0 {
			continue
		}
		if t.staleOnly && !t.isStale(t.sections[i].res.PRs[at]) {
			t.staleOnly = false
		}
		if t.filter.Visible() {
			t.clearFilter()
		}
		if repo, _, _ := strings.Cut(id, "#"); t.repo != "" && !strings.EqualFold(repo, t.repo) {
			t.repo = allRepos
		}
		if i != t.active {
			t.active = i
		}
		t.setRows()
		t.selectID(id)
		return t.syncDetail()
	}
	return nil
}

// CapturingInput implements tabs.InputCapturer.
func (t *Tab) CapturingInput() bool { return t.dialog != nil || t.filter.Active() || t.diff != nil }

// LastUpdated implements tabs.Updater: when the shown section was fetched.
func (t *Tab) LastUpdated() time.Time {
	if s := t.cur(); s != nil {
		return s.updated
	}
	return time.Time{}
}

// Fetching implements tabs.Fetcher: any section is fetching.
func (t *Tab) Fetching() bool {
	return slices.ContainsFunc(t.sections, func(s *section) bool { return s.fetching })
}

// Sources implements tabs.AutoRefresher.
func (t *Tab) Sources() []refresh.Source { return []refresh.Source{refresh.GitHub} }

// RefreshWhenHidden implements tabs.AutoRefresher: no, GitHub calls are
// rate limited; the tab catches up when shown.
func (t *Tab) RefreshWhenHidden() bool { return false }

// StatusDetail implements tabs.StatusDetailer: "@login · PR 3/12 (fetched
// 12)", with "of N" when GitHub matched more than were fetched.
func (t *Tab) StatusDetail() string {
	s := t.cur()
	if s == nil || !s.loaded {
		return ""
	}
	var parts []string
	if s.res.Viewer != "" {
		parts = append(parts, "@"+s.res.Viewer)
	}
	n := len(t.table.VisibleRows())
	pos := 0
	if n > 0 {
		pos = t.table.Cursor() + 1
	}
	fetched := fmt.Sprintf("fetched %d", len(s.res.PRs))
	if s.res.Total > len(s.res.PRs) {
		fetched += fmt.Sprintf(" of %d", s.res.Total)
	}
	parts = append(parts, fmt.Sprintf("PR %d/%d (%s)", pos, n, fetched))
	return strings.Join(parts, " · ")
}
