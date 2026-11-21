package pipelines

import (
	"cmp"
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
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/refresh"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
)

// Title is the tab's title, which also routes its messages.
const Title = "Pipelines"

// EntityRun is the palette group of workflow runs.
const EntityRun = "Run"

const (
	// listRatio is the list's starting share of the width.
	listRatio = 0.62
	// filterBoxHeight is the bordered filter box above the table.
	filterBoxHeight = 3
	// activeInterval is how often runs refresh while one on screen is
	// queued or in progress.
	activeInterval = 15 * time.Second
)

// filters narrow the runs. Branch, status and mine are sent to GitHub;
// workflow is applied to what came back.
type filters struct {
	branch   string
	workflow string
	status   string // REST status / conclusion; "" = any
	mine     bool
}

func (f filters) any() bool { return f != filters{} }

// repoRuns is one repo's runs for the current filters.
type repoRuns struct {
	repo       string
	key        string // kv_cache key (project, repo and filters)
	runs       []github.Run
	loaded     bool
	fresh      bool // a fetch for key has landed; ignore older cached data
	fetching   bool
	cacheAsked bool
	updated    time.Time
}

// Tab is the Pipelines tab (P2.6): GitHub Actions runs for the active
// project's repo, or every configured repo.
type Tab struct {
	theme   *ui.Theme
	keys    *ui.KeyMap
	store   tabs.KVStore
	gh      github.RunsClient
	browser browser.Opener
	cfg     *config.Config
	now     func() time.Time
	// tick arms timers (tea.Tick; tests replace it).
	tick func(time.Duration, func(time.Time) tea.Msg) tea.Cmd

	split  ui.SplitPane
	table  ui.Table
	multi  bool // the table has a repo column
	filter ui.Filter
	width  int
	height int

	project *config.Project
	// override is a repo shown instead of the project's (a PR's runs from
	// another repo); "" = none.
	override string
	filt     filters
	viewer   string // login, once known (for mine)
	asking   bool   // the viewer is being fetched
	repos    []*repoRuns

	dialog   *huh.Form
	onDone   func() tea.Cmd
	authErr  bool
	fetchErr error // the last fetch failed (shown while nothing is loaded)
	errShown bool  // a fetch error was toasted since the last Refresh

	visible bool
	fastGen int  // generation of the fast refresh timer
	fastOn  bool // a fast refresh timer is armed

	det detailState
}

var (
	_ tabs.Tab             = (*Tab)(nil)
	_ tabs.InputCapturer   = (*Tab)(nil)
	_ tabs.Commander       = (*Tab)(nil)
	_ tabs.EntityProvider  = (*Tab)(nil)
	_ tabs.Updater         = (*Tab)(nil)
	_ tabs.Fetcher         = (*Tab)(nil)
	_ tabs.AutoRefresher   = (*Tab)(nil)
	_ tabs.StatusDetailer  = (*Tab)(nil)
	_ tabs.VisibilityAware = (*Tab)(nil)
	_ tabs.RunsShower      = (*Tab)(nil)
)

// New returns the Pipelines tab.
func New(d tabkit.Deps) tabs.Tab { return newTab(d) }

func newTab(d tabkit.Deps) *Tab {
	cfg := d.Config
	if cfg == nil {
		cfg = config.Default()
	}
	t := &Tab{
		theme: d.Theme, keys: d.Keys, store: d.Store, gh: d.Runs, browser: d.Browser, cfg: cfg,
		now: time.Now, tick: tea.Tick,
		split:  ui.NewSplitPane(d.Theme),
		filter: ui.NewFilter(d.Theme, d.Keys),
		det:    newDetailState(),
	}
	t.split.SetRatio(listRatio)
	t.buildTable(false)
	t.rebuildRepos()
	return t
}

// buildTable makes the table, with a repo column when multi.
func (t *Tab) buildTable(multi bool) {
	t.multi = multi
	t.table = ui.NewTable(t.theme, t.keys, t.columns())
	t.table.SetEmptyView(t.emptyView)
	t.resizeTable()
}

// repoNames are the repos listed: the override, the project's, or every
// configured project's.
func (t *Tab) repoNames() []string {
	if t.override != "" {
		return []string{t.override}
	}
	if t.project != nil {
		if t.project.Repo == "" {
			return nil
		}
		return []string{t.project.Repo}
	}
	var out []string
	for _, p := range t.cfg.Projects {
		if p.Repo != "" && !slices.ContainsFunc(out, func(r string) bool { return strings.EqualFold(r, p.Repo) }) {
			out = append(out, p.Repo)
		}
	}
	return out
}

// apiFilter is what GitHub is asked to filter on.
func (t *Tab) apiFilter() github.RunFilter {
	f := github.RunFilter{Branch: t.filt.branch, Status: t.filt.status}
	if t.filt.mine {
		f.Actor = t.viewer
	}
	return f
}

// repoKey is the kv_cache key of repo's runs under the current project and
// GitHub-side filters.
func (t *Tab) repoKey(repo string) string {
	f := t.apiFilter()
	h := fnv.New32a()
	_, _ = h.Write([]byte(f.Branch + "\x00" + f.Status + "\x00" + f.Actor))
	return fmt.Sprintf("%s:%s:%08x", tabkit.CacheKey(Title, t.project), strings.ToLower(repo), h.Sum32())
}

// rebuildRepos lays out the repos for the current project, override and
// filters, keeping entries whose key is unchanged.
func (t *Tab) rebuildRepos() {
	old := map[string]*repoRuns{}
	for _, r := range t.repos {
		old[r.key] = r
	}
	t.repos = nil
	for _, name := range t.repoNames() {
		k := t.repoKey(name)
		if r := old[k]; r != nil {
			t.repos = append(t.repos, r)
			continue
		}
		t.repos = append(t.repos, &repoRuns{repo: name, key: k})
	}
	if multi := len(t.repos) > 1; multi != t.multi {
		t.buildTable(multi)
	}
}

func (t *Tab) byKey(k string) *repoRuns {
	for _, r := range t.repos {
		if r.key == k {
			return r
		}
	}
	return nil
}

// Init implements tabs.Tab.
func (t *Tab) Init() tea.Cmd { return t.Refresh() }

// Refresh implements tabs.Tab: each repo loads its cached copy once per key
// and fetches unless a fetch is running. With "mine" on and no login yet,
// the login is fetched first.
func (t *Tab) Refresh() tea.Cmd {
	t.errShown = false
	if t.filt.mine && t.viewer == "" {
		return t.askViewer()
	}
	var cmds []tea.Cmd
	for _, r := range t.repos {
		if !r.cacheAsked {
			r.cacheAsked = true
			cmds = append(cmds, tabkit.LoadCached(t.store, Title, r.key))
		}
		if r.fetching || t.gh == nil {
			continue
		}
		r.fetching = true
		gh, repo, f := t.gh, r.repo, t.apiFilter()
		cmds = append(cmds, tabkit.FetchAndCache(t.store, tabkit.Fetch{
			Tab: Title, Key: r.key, Project: t.project, Now: t.now,
			Run: func(ctx context.Context) ([]byte, error) {
				runs, err := gh.ListRuns(ctx, repo, f)
				if err != nil {
					return nil, err
				}
				return json.Marshal(runs)
			},
		}))
	}
	return tea.Batch(cmds...)
}

// viewerMsg carries the login for "mine".
type viewerMsg struct {
	login string
	err   error
}

// TargetTab implements tabs.TargetedMsg.
func (viewerMsg) TargetTab() string { return Title }

func (t *Tab) askViewer() tea.Cmd {
	if t.gh == nil || t.asking {
		return nil
	}
	t.asking = true
	gh := t.gh
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		login, err := gh.Viewer(ctx)
		return viewerMsg{login: login, err: err}
	}
}

// Update implements tabs.Tab. After every message the detail pane catches
// up with the selection.
func (t *Tab) Update(msg tea.Msg) (tabs.Tab, tea.Cmd) {
	cmd := t.update(msg)
	return t, tea.Batch(cmd, t.syncDetail())
}

func (t *Tab) update(msg tea.Msg) tea.Cmd {
	if cmd, ok := t.updateDetail(msg); ok {
		return cmd
	}
	switch msg := msg.(type) {
	case tabkit.CacheLoadedMsg:
		t.cacheLoaded(msg)
		return nil
	case tabkit.FetchedMsg:
		return t.fetched(msg)
	case viewerMsg:
		return t.viewerLoaded(msg)
	case fastTickMsg:
		return t.fastTick(msg)
	}
	if t.dialog != nil {
		return t.updateDialog(msg)
	}
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	if t.filter.Active() {
		t.updateFilter(kp)
		return nil
	}
	l, pk := t.keys.List, t.keys.Pipelines
	switch {
	case key.Matches(kp, l.Filter):
		t.startFilter()
	case key.Matches(kp, t.keys.Input.Cancel) && t.filter.Value() != "":
		t.clearFilter()
	case key.Matches(kp, pk.Filters):
		return t.openFilters()
	case key.Matches(kp, pk.Mine):
		return t.toggleMine()
	case key.Matches(kp, pk.ClearFilters):
		return t.clearFilters()
	case key.Matches(kp, pk.DetailDown):
		t.scrollDetail(1)
	case key.Matches(kp, pk.DetailUp):
		t.scrollDetail(-1)
	case key.Matches(kp, pk.Open):
		return t.openInBrowser()
	case key.Matches(kp, l.GrowPane):
		t.split.Grow()
		t.resizeTable()
	case key.Matches(kp, l.ShrinkPane):
		t.split.Shrink()
		t.resizeTable()
	default:
		return t.table.Update(kp)
	}
	return nil
}

func (t *Tab) cacheLoaded(msg tabkit.CacheLoadedMsg) {
	r := t.byKey(msg.Key)
	if r == nil || r.fresh {
		return
	}
	var runs []github.Run
	if json.Unmarshal(msg.Value, &runs) != nil {
		return // unreadable cache: wait for the fetch
	}
	r.runs, r.loaded, r.updated = runs, true, msg.FetchedAt
	t.setRows()
}

func (t *Tab) fetched(msg tabkit.FetchedMsg) tea.Cmd {
	r := t.byKey(msg.Key)
	if r == nil {
		return nil // fetched for filters or a project we have since left
	}
	r.fetching = false
	if msg.Err != nil {
		t.authErr = github.IsAuthError(msg.Err)
		t.fetchErr = msg.Err
		fast := t.armFast()
		if t.errShown {
			return fast // one toast per refresh, not one per repo
		}
		t.errShown = true
		err := msg.Err
		if t.authErr {
			err = errors.New("GitHub: not logged in; run `gh auth login`")
		}
		return tea.Batch(errCmd(err), fast)
	}
	var runs []github.Run
	if err := json.Unmarshal(msg.Value, &runs); err != nil {
		return errCmd(fmt.Errorf("pipelines: %w", err))
	}
	t.authErr, t.fetchErr = false, nil
	r.runs, r.loaded, r.fresh, r.updated = runs, true, true, msg.At
	t.setRows()
	cmds := []tea.Cmd{t.armFast()}
	if msg.CacheErr != nil {
		cmds = append(cmds, errCmd(msg.CacheErr))
	}
	return tea.Batch(cmds...)
}

func (t *Tab) viewerLoaded(msg viewerMsg) tea.Cmd {
	t.asking = false
	if msg.err != nil {
		t.filt.mine = false
		t.rebuildRepos()
		return errCmd(fmt.Errorf("pipelines: who am I? %s", github.Reason(msg.err)))
	}
	t.viewer = msg.login
	if !t.filt.mine {
		return nil
	}
	t.rebuildRepos()
	t.setRows()
	return t.Refresh()
}

func errCmd(err error) tea.Cmd {
	return func() tea.Msg { return ui.ErrMsg{Err: err} }
}

// --- runs & rows -----------------------------------------------------------------

// runs is every loaded run that passes the workflow filter, newest first.
func (t *Tab) runs() []github.Run {
	var out []github.Run
	for _, r := range t.repos {
		for _, run := range r.runs {
			if t.filt.workflow == "" || strings.EqualFold(run.Workflow, t.filt.workflow) {
				out = append(out, run)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b github.Run) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(b.ID, a.ID)
	})
	return out
}

func (t *Tab) loaded() bool {
	return len(t.repos) > 0 && slices.ContainsFunc(t.repos, func(r *repoRuns) bool { return r.loaded })
}

// setRows fills the table, keeping the selected run selected.
func (t *Tab) setRows() {
	sel := t.selectedKey()
	var rows [][]string
	for _, r := range t.runs() {
		rows = append(rows, t.runRow(r))
	}
	t.table.SetRows(rows)
	if sel != "" {
		t.selectKey(sel)
	}
}

func (t *Tab) selectedKey() string {
	if row := t.table.SelectedRow(); row != nil {
		return row[len(row)-1]
	}
	return ""
}

func (t *Tab) selectKey(k string) bool {
	for i, row := range t.table.VisibleRows() {
		if row[len(row)-1] == k {
			t.table.SetCursor(i)
			return true
		}
	}
	return false
}

// selected is the run under the cursor.
func (t *Tab) selected() (github.Run, bool) {
	k := t.selectedKey()
	if k == "" {
		return github.Run{}, false
	}
	for _, r := range t.repos {
		for _, run := range r.runs {
			if run.Key() == k {
				return run, true
			}
		}
	}
	return github.Run{}, false
}

// anyActive reports whether a run on screen is queued or in progress.
func (t *Tab) anyActive() bool {
	return slices.ContainsFunc(t.runs(), github.Run.Active)
}

// --- fast refresh ------------------------------------------------------------------

// fastTickMsg is the fast refresh timer firing.
type fastTickMsg struct{ gen int }

// TargetTab implements tabs.TargetedMsg.
func (fastTickMsg) TargetTab() string { return Title }

// fastInterval is the refresh interval while runs are active; 0 when the
// normal GitHub refresh is already at least as fast.
func (t *Tab) fastInterval() time.Duration {
	if base := t.cfg.GitHub.RefreshInterval.Duration; base > 0 && base <= activeInterval {
		return 0
	}
	return activeInterval
}

// armFast starts the fast refresh timer while the tab is visible and a run
// on screen is active. Only one timer runs at a time.
func (t *Tab) armFast() tea.Cmd {
	d := t.fastInterval()
	if t.fastOn || !t.visible || d == 0 || t.gh == nil || !t.anyActive() {
		return nil
	}
	t.fastOn = true
	t.fastGen++
	gen := t.fastGen
	return t.tick(d, func(time.Time) tea.Msg { return fastTickMsg{gen: gen} })
}

// fastTick refreshes the runs, and the selected run's jobs while it is
// active. The next timer is armed when the fetch lands.
func (t *Tab) fastTick(msg fastTickMsg) tea.Cmd {
	if msg.gen != t.fastGen {
		return nil
	}
	t.fastOn = false
	if !t.visible || !t.anyActive() {
		return nil
	}
	cmds := []tea.Cmd{t.Refresh()}
	if r, ok := t.selected(); ok && r.Active() {
		cmds = append(cmds, t.fetchJobs(r))
	}
	if !t.Fetching() {
		cmds = append(cmds, t.armFast()) // nothing will land to re-arm it
	}
	return tea.Batch(cmds...)
}

// SetVisible implements tabs.VisibilityAware: the fast refresh only runs on
// screen.
func (t *Tab) SetVisible(v bool) tea.Cmd {
	t.visible = v
	if !v {
		t.fastOn = false
		t.fastGen++ // drop the armed timer
		return nil
	}
	return t.armFast()
}

// --- filters ---------------------------------------------------------------------

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

// statusChoices are the status filter's options: label and REST value.
var statusChoices = []struct{ label, value string }{
	{"Any status", ""},
	{"In progress", "in_progress"},
	{"Queued", "queued"},
	{"Failed", "failure"},
	{"Succeeded", "success"},
	{"Cancelled", "cancelled"},
	{"Completed", "completed"},
}

func statusLabel(v string) string {
	for _, c := range statusChoices {
		if c.value == v {
			return c.label
		}
	}
	return v
}

// seen returns the distinct values of f over the loaded runs, sorted.
func (t *Tab) seen(f func(github.Run) string) []string {
	var out []string
	for _, r := range t.repos {
		for _, run := range r.runs {
			v := f(run)
			if v != "" && !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
	}
	slices.SortFunc(out, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	return out
}

// openFilters opens the branch / workflow / status / mine form.
func (t *Tab) openFilters() tea.Cmd {
	f := t.filt
	wfOpts := []huh.Option[string]{huh.NewOption("Any workflow", "")}
	workflows := t.seen(func(r github.Run) string { return r.Workflow })
	if f.workflow != "" && !slices.Contains(workflows, f.workflow) {
		workflows = append(workflows, f.workflow)
	}
	for _, w := range workflows {
		wfOpts = append(wfOpts, huh.NewOption(w, w))
	}
	stOpts := make([]huh.Option[string], 0, len(statusChoices))
	for _, c := range statusChoices {
		stOpts = append(stOpts, huh.NewOption(c.label, c.value))
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().Key("branch").Title("Branch").
			Description("empty = every branch").Placeholder("any").
			Suggestions(t.seen(func(r github.Run) string { return r.Branch })).
			Value(&f.branch),
		huh.NewSelect[string]().Key("workflow").Title("Workflow").Options(wfOpts...).Value(&f.workflow),
		huh.NewSelect[string]().Key("status").Title("Status").Options(stOpts...).Value(&f.status),
		huh.NewConfirm().Key("mine").Title("Only runs I started?").Affirmative("Mine").Negative("Everyone's").Value(&f.mine),
	))
	return t.openDialog(form, func() tea.Cmd {
		f.branch = strings.TrimSpace(f.branch)
		return t.setFilters(f)
	})
}

// setFilters applies f: new keys for GitHub-side changes (and a refresh),
// a re-filter for the workflow.
func (t *Tab) setFilters(f filters) tea.Cmd {
	if f == t.filt {
		return nil
	}
	t.filt = f
	t.rebuildRepos()
	t.table.SetRows(nil) // a new list starts at the top
	t.setRows()
	return t.Refresh()
}

func (t *Tab) toggleMine() tea.Cmd {
	f := t.filt
	f.mine = !f.mine
	return t.setFilters(f)
}

// clearFilters drops every filter and a PR's repo override.
func (t *Tab) clearFilters() tea.Cmd {
	if !t.filt.any() && t.override == "" && t.filter.Value() == "" {
		return nil
	}
	if t.filter.Visible() {
		t.clearFilter()
	}
	t.override = ""
	t.filt = filters{}
	t.rebuildRepos()
	t.table.SetRows(nil)
	t.setRows()
	return t.Refresh()
}

// ShowRuns implements tabs.RunsShower: the runs of repo's branch, with no
// other filters. A repo outside the active project is shown on its own
// until the filters are cleared or the project changes.
func (t *Tab) ShowRuns(repo, branch string) tea.Cmd {
	t.override = ""
	if repo != "" && !slices.ContainsFunc(t.repoNames(), func(r string) bool { return strings.EqualFold(r, repo) }) {
		t.override = repo
	}
	if t.filter.Visible() {
		t.clearFilter()
	}
	t.filt = filters{branch: branch}
	t.rebuildRepos()
	t.table.SetRows(nil)
	t.setRows()
	return t.Refresh()
}

// openDialog shows form over the tab; done runs once it is completed.
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

func (t *Tab) dialogSize() (w, h int) { return max(t.width-4, 10), max(t.height-2, 3) }

func (t *Tab) sizeDialog() {
	if t.dialog == nil {
		return
	}
	w, h := t.dialogSize()
	_, _ = t.dialog.Update(tea.WindowSizeMsg{Width: w, Height: h})
}

// openInBrowser opens the selected run on GitHub.
func (t *Tab) openInBrowser() tea.Cmd {
	r, ok := t.selected()
	if !ok {
		return nil
	}
	if t.browser == nil {
		return errCmd(errors.New("can't open a browser here"))
	}
	if r.URL == "" {
		return errCmd(fmt.Errorf("run %d has no URL", r.ID))
	}
	b, url := t.browser, r.URL
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := b.Open(ctx, url); err != nil {
			return ui.ErrMsg{Err: fmt.Errorf("open run: %w", err)}
		}
		return nil
	}
}

// --- layout ----------------------------------------------------------------------

// SetSize implements tabs.Tab.
func (t *Tab) SetSize(width, height int) {
	t.width, t.height = width, height
	t.split.SetSize(width, height)
	t.resizeTable()
	t.sizeDialog()
}

func (t *Tab) resizeTable() {
	w, h := t.split.LeftSize()
	h -= filterBoxHeight
	if t.filter.Visible() {
		h--
	}
	t.table.SetSize(w, max(h, 0))
}

// SetProject implements tabs.Tab: new repos and keys, no rows until
// Refresh loads them; filters and the override are dropped.
func (t *Tab) SetProject(p *config.Project) {
	t.project = p
	t.override = ""
	t.filt = filters{}
	if t.filter.Visible() {
		t.clearFilter()
	}
	t.repos = nil
	t.rebuildRepos()
	t.table.SetRows(nil)
}

// --- interfaces ------------------------------------------------------------------

// Title implements tabs.Tab.
func (t *Tab) Title() string { return Title }

// ShortHelp implements tabs.Tab.
func (t *Tab) ShortHelp() []key.Binding {
	switch {
	case t.dialog != nil:
		return t.keys.FormHelp()
	case t.filter.Active():
		return []key.Binding{t.keys.Input.Cancel, t.keys.Input.Accept}
	}
	pk := t.keys.Pipelines
	return []key.Binding{pk.Filters, pk.Mine, pk.ClearFilters, t.keys.List.Filter, pk.Open}
}

// FullHelp implements tabs.Tab.
func (t *Tab) FullHelp() [][]key.Binding {
	l, in, pk := t.keys.List, t.keys.Input, t.keys.Pipelines
	return [][]key.Binding{
		{l.Up, l.Down, l.Top, l.Bottom, l.PageDown, l.PageUp},
		{l.Filter, pk.Filters, pk.Mine, pk.ClearFilters, l.ShrinkPane, l.GrowPane},
		{pk.DetailDown, pk.DetailUp, pk.Open},
		{in.Accept, in.Cancel, in.DeleteBack, in.ClearInput},
	}
}

// Commands implements tabs.Commander.
func (t *Tab) Commands() []tabs.Command {
	l, pk := t.keys.List, t.keys.Pipelines
	do := func(f func()) func() tea.Cmd { return func() tea.Cmd { f(); return t.syncDetail() } }
	mine := "Pipelines: show only mine"
	if t.filt.mine {
		mine = "Pipelines: show everyone's"
	}
	cmds := []tabs.Command{
		{ID: "filter", Title: "Filter runs", Key: l.Filter, Run: do(t.startFilter)},
		{ID: "pipeline_filters", Title: "Pipelines: filters…", Key: pk.Filters, Run: t.openFilters},
		{ID: "pipeline_mine", Title: mine, Key: pk.Mine, Run: t.toggleMine},
		{ID: "pipeline_clear_filters", Title: "Pipelines: clear filters", Key: pk.ClearFilters, Run: t.clearFilters},
	}
	return append(cmds,
		tabs.Command{ID: "pipeline_detail_down", Title: "Pipelines: scroll detail down", Key: pk.DetailDown, Run: do(func() { t.scrollDetail(1) })},
		tabs.Command{ID: "pipeline_detail_up", Title: "Pipelines: scroll detail up", Key: pk.DetailUp, Run: do(func() { t.scrollDetail(-1) })},
		tabs.Command{ID: "pipeline_open", Title: "Run: open in browser", Key: pk.Open, Run: t.openInBrowser},
		tabs.Command{ID: "grow_pane", Title: "Widen list", Key: l.GrowPane, Run: do(func() { t.split.Grow(); t.resizeTable() })},
		tabs.Command{ID: "shrink_pane", Title: "Narrow list", Key: l.ShrinkPane, Run: do(func() { t.split.Shrink(); t.resizeTable() })},
	)
}

// Entities implements tabs.EntityProvider: every loaded run.
func (t *Tab) Entities() []tabs.Entity {
	var out []tabs.Entity
	for _, r := range t.repos {
		for _, run := range r.runs {
			out = append(out, tabs.Entity{
				Kind: EntityRun, ID: run.Key(),
				Title:  fmt.Sprintf("%s #%d %s", run.Workflow, run.RunNumber, run.Title),
				Detail: fmt.Sprintf("%s · %s · %s", run.Repo, run.Branch, stateWord(run.State)),
				Search: run.Branch + " " + run.Actor,
			})
		}
	}
	return out
}

// Select implements tabs.EntityProvider, dropping the filters that hide
// the run.
func (t *Tab) Select(id string) tea.Cmd {
	if t.selectKey(id) {
		return t.syncDetail()
	}
	if t.filter.Visible() {
		t.clearFilter()
	}
	if t.filt.workflow != "" {
		t.filt.workflow = ""
		t.setRows()
	}
	t.selectKey(id)
	return t.syncDetail()
}

// CapturingInput implements tabs.InputCapturer.
func (t *Tab) CapturingInput() bool { return t.dialog != nil || t.filter.Active() }

// LastUpdated implements tabs.Updater: the oldest repo fetch, zero until
// every repo has data.
func (t *Tab) LastUpdated() time.Time {
	var oldest time.Time
	for _, r := range t.repos {
		if r.updated.IsZero() {
			return time.Time{}
		}
		if oldest.IsZero() || r.updated.Before(oldest) {
			oldest = r.updated
		}
	}
	return oldest
}

// Fetching implements tabs.Fetcher.
func (t *Tab) Fetching() bool {
	return t.asking || slices.ContainsFunc(t.repos, func(r *repoRuns) bool { return r.fetching })
}

// Sources implements tabs.AutoRefresher.
func (t *Tab) Sources() []refresh.Source { return []refresh.Source{refresh.GitHub} }

// RefreshWhenHidden implements tabs.AutoRefresher: no, GitHub calls are
// rate limited.
func (t *Tab) RefreshWhenHidden() bool { return false }

// StatusDetail implements tabs.StatusDetailer: "@login · run 3/40 · 2
// running".
func (t *Tab) StatusDetail() string {
	if !t.loaded() {
		return ""
	}
	var parts []string
	if t.viewer != "" {
		parts = append(parts, "@"+t.viewer)
	}
	n := len(t.table.VisibleRows())
	pos := 0
	if n > 0 {
		pos = t.table.Cursor() + 1
	}
	parts = append(parts, fmt.Sprintf("run %d/%d", pos, n))
	active := 0
	for _, r := range t.runs() {
		if r.Active() {
			active++
		}
	}
	if active > 0 {
		parts = append(parts, fmt.Sprintf("%d running", active))
	}
	return strings.Join(parts, " · ")
}

// --- view ------------------------------------------------------------------------

// View implements tabs.Tab.
func (t *Tab) View() string {
	if t.dialog != nil {
		w, h := t.dialogSize()
		return t.theme.PaneFocused.Render(ui.Box(t.dialog.View(), w, h))
	}
	lw, _ := t.split.LeftSize()
	list := t.filterBox(lw)
	if t.filter.Visible() {
		list += "\n" + t.filter.View(lw)
	}
	list += "\n" + t.table.View()
	return t.split.View(list, t.detail(), true)
}

// filterBox is the bordered box naming the repos and active filters.
func (t *Tab) filterBox(width int) string {
	th := t.theme
	sep := th.SectionSep.Render(" · ")
	label := func(k, v string) string { return th.SearchIcon.Render(k+": ") + th.Body.Render(v) }
	var parts []string
	switch {
	case t.override != "":
		parts = append(parts, label("repo", t.override))
	case len(t.repos) == 1:
		parts = append(parts, th.SearchQuery.Render(t.repos[0].repo))
	case len(t.repos) > 1:
		parts = append(parts, th.SearchQuery.Render(fmt.Sprintf("%d repos", len(t.repos))))
	}
	if t.filt.branch != "" {
		parts = append(parts, label("branch", t.filt.branch))
	}
	if t.filt.workflow != "" {
		parts = append(parts, label("workflow", t.filt.workflow))
	}
	if t.filt.status != "" {
		parts = append(parts, label("status", strings.ToLower(statusLabel(t.filt.status))))
	}
	if t.filt.mine {
		who := "mine"
		if t.viewer != "" {
			who = "@" + t.viewer
		}
		parts = append(parts, label("actor", who))
	}
	if !t.filt.any() {
		parts = append(parts, th.Hint.Render(t.keys.Pipelines.Filters.Help().Key+" filters"))
	}
	line := th.SearchIcon.Render("⌕ ") + strings.Join(parts, sep)
	return th.SearchBox.Render(ui.Box(line, max(width-4, 1), 1))
}

func (t *Tab) emptyView(w, h int) string {
	th := t.theme
	msg := ""
	switch {
	case len(t.repos) == 0 && t.project != nil:
		msg = "project " + t.project.Name + " has no GitHub repo (projects[].repo)"
	case len(t.repos) == 0:
		msg = "no projects with a GitHub repo configured"
	case t.gh == nil:
		msg = "GitHub isn't available"
	case t.authErr:
		msg = "not logged in to GitHub: run `gh auth login`"
	case !t.loaded() && t.fetchErr != nil:
		msg = "couldn't load runs: " + github.Reason(t.fetchErr)
	case !t.loaded() || t.asking:
		msg = "heating up…"
	case t.filt.any() || t.override != "":
		msg = "no runs match · " + t.keys.Pipelines.ClearFilters.Help().Key + " clears filters"
	}
	if msg == "" {
		return anim.EmptyForge(w, h, th)
	}
	return ui.Box(th.TableEmpty.Render(msg), w, h)
}
