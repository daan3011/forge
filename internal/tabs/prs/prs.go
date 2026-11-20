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
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/refresh"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
)

// Title is the tab's title, which also routes its fetch results.
const Title = "PRs"

// EntityPR is the palette group of PRs.
const EntityPR = "PR"

// listRatio is the list's starting share of the width: the PR columns need
// room, and the detail pane is small until P2.2.
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
}

// Tab is the PRs tab: one GitHub search per configured section, shown as
// sub-tabs with counts.
type Tab struct {
	theme *ui.Theme
	keys  *ui.KeyMap
	store tabs.KVStore
	gh    github.PRSearcher
	anim  *anim.Engine
	cfg   *config.Config
	now   func() time.Time

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
	dialog   *huh.Form
	picked   *string // the repo dialog's value
	authErr  bool    // the last fetch failed for want of a gh login
	errShown bool    // a fetch error was toasted since the last Refresh
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
		now:    time.Now,
		split:  ui.NewSplitPane(d.Theme),
		filter: ui.NewFilter(d.Theme, d.Keys),
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

// Update implements tabs.Tab.
func (t *Tab) Update(msg tea.Msg) (tabs.Tab, tea.Cmd) {
	switch msg := msg.(type) {
	case tabkit.CacheLoadedMsg:
		t.cacheLoaded(msg)
		return t, nil
	case tabkit.FetchedMsg:
		return t, t.fetched(msg)
	}
	if t.dialog != nil {
		return t, t.updateDialog(msg)
	}
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return t, nil
	}
	if t.filter.Active() {
		t.updateFilter(kp)
		return t, nil
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
		return t, t.openRepoFilter()
	case key.Matches(kp, l.GrowPane):
		t.resize(t.split.Grow)
	case key.Matches(kp, l.ShrinkPane):
		t.resize(t.split.Shrink)
	default:
		return t, t.table.Update(kp)
	}
	return t, nil
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
	s.res, s.loaded, s.updated = res, true, msg.FetchedAt
	t.rowsChanged(s)
}

func (t *Tab) fetched(msg tabkit.FetchedMsg) tea.Cmd {
	s := t.byKey(msg.Key)
	if s == nil {
		return nil // fetched for a project we have since left
	}
	s.fetching = false
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
		return errCmd(err)
	}
	var res github.SearchResult
	if err := json.Unmarshal(msg.Value, &res); err != nil {
		return errCmd(fmt.Errorf("prs: %w", err))
	}
	t.authErr = false
	s.res, s.loaded, s.fresh, s.updated = res, true, true, msg.At
	t.rowsChanged(s)
	if msg.CacheErr != nil {
		return errCmd(msg.CacheErr)
	}
	return nil
}

func errCmd(err error) tea.Cmd {
	return func() tea.Msg { return ui.ErrMsg{Err: err} }
}

// shownPRs is a section's PRs after the repo filter.
func (t *Tab) shownPRs(s *section) []github.PR {
	if t.repo == "" {
		return s.res.PRs
	}
	var out []github.PR
	for _, p := range s.res.PRs {
		if strings.EqualFold(p.Repo, t.repo) {
			out = append(out, p)
		}
	}
	return out
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
		now := t.now()
		for _, p := range t.shownPRs(s) {
			rows = append(rows, prRow(p, now))
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
	t.picked = &picked
	t.dialog = huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Key("repo").
			Title("Show PRs from which repo?").
			Description("applies to every section · / searches").
			Options(opts...).Value(t.picked),
	)).WithTheme(t.theme.Huh()).WithKeyMap(t.keys.HuhKeyMap()).WithShowHelp(false)
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
		t.dialog = nil
		t.setRepo(*t.picked)
		return nil
	case huh.StateAborted:
		t.dialog = nil
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
	return t.sliding && !t.divider.Settled(t.anim.Frame())
}

// SetSize implements tabs.Tab. The section bar takes the first row.
func (t *Tab) SetSize(width, height int) {
	t.width, t.height = width, height
	t.split.SetSize(width, max(height-1, 0))
	t.resizeTable()
	t.sizeDialog()
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
		t.repo = ""
		t.table.SetRows(nil)
	}
}

// --- view ----------------------------------------------------------------------

// View implements tabs.Tab.
func (t *Tab) View() string {
	t.slide()
	bar := ui.SectionBar(t.theme, t.sectionTitles(), t.counts(), t.active, t.width)
	if t.dialog != nil {
		w, h := t.dialogSize()
		return bar + "\n" + t.theme.PaneFocused.Render(ui.Box(t.dialog.View(), w, h))
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
	case t.cur() != nil && !t.cur().loaded:
		msg = "heating up…"
	case t.repo != "" && t.cur() != nil && len(t.cur().res.PRs) > 0:
		msg = "no PRs from " + t.repo + " here · " + t.keys.PRs.RepoFilter.Help().Key + " changes the repo"
	}
	if msg == "" {
		return anim.EmptyForge(w, h, th)
	}
	return ui.Box(th.TableEmpty.Render(msg), w, h)
}

// detail is the right pane: a short summary until P2.2's detail view.
func (t *Tab) detail() string {
	th := t.theme
	w, _ := t.split.RightSize()
	p, ok := t.selected()
	if !ok {
		return th.Hint.Render("no PR selected")
	}
	icon, ist := th.PRIcon(p.State)
	rev, rst := th.ReviewIcon(p.ReviewDecision)
	ci, cst := th.CIIcon(p.CI)
	now := t.now()
	author := "ghost"
	if p.Author != "" {
		author = "@" + p.Author
	}
	lines := []string{
		th.PRRepo.Render(fmt.Sprintf("%s · #%d", p.Repo, p.Number)),
		th.DetailTitle.Render(ansi.Wrap(p.Title, max(w, 1), "")),
		"",
		ist.Render(icon+" "+p.State) + th.Hint.Render("  "+p.BaseRef+" ← "+p.HeadRef),
		th.Hint.Render("by ") + th.Body.Render(author) + th.Hint.Render(" · opened "+age(now, p.CreatedAt)+" ago · updated "+age(now, p.UpdatedAt)+" ago"),
		th.DiffAdd.Render("+"+fmt.Sprint(p.Additions)) + " " + th.DiffDel.Render("-"+fmt.Sprint(p.Deletions)),
		"",
		rst.Render(rev) + " " + th.Body.Render(reviewText(p.ReviewDecision)),
		cst.Render(ci) + " " + th.Body.Render(ciText(p.CI)),
		mergeLine(th, p.Mergeable),
		"",
		th.LinkText.Render(p.URL),
	}
	return strings.Join(lines, "\n")
}

func reviewText(d string) string {
	switch d {
	case github.ReviewApproved:
		return "changes approved"
	case github.ReviewChangesRequested:
		return "changes requested"
	case github.ReviewRequired:
		return "review required"
	}
	return "no review required"
}

func ciText(s string) string {
	switch s {
	case github.CISuccess:
		return "checks passed"
	case github.CIFailure, github.CIError:
		return "checks failing"
	case github.CIPending, github.CIExpected:
		return "checks running"
	}
	return "no checks"
}

func mergeLine(th *ui.Theme, m string) string {
	switch m {
	case github.Mergeable:
		g, st := th.CIIcon(github.CISuccess)
		return st.Render(g) + " " + th.Body.Render("no conflicts with base branch")
	case github.Conflicting:
		g, st := th.CIIcon(github.CIFailure)
		return st.Render(g) + " " + th.Body.Render("conflicts with base branch")
	}
	g, st := th.CIIcon("")
	return st.Render(g) + " " + th.Hint.Render("mergeability unknown")
}

// --- interfaces ----------------------------------------------------------------

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
	pk := t.keys.PRs
	return []key.Binding{pk.PrevSection, pk.NextSection, pk.RepoFilter, t.keys.List.Filter}
}

// FullHelp implements tabs.Tab.
func (t *Tab) FullHelp() [][]key.Binding {
	l, in, pk := t.keys.List, t.keys.Input, t.keys.PRs
	return [][]key.Binding{
		{l.Up, l.Down, l.Top, l.Bottom, l.PageDown, l.PageUp},
		{pk.NextSection, pk.PrevSection, l.Filter, pk.RepoFilter, l.ShrinkPane, l.GrowPane},
		{in.Accept, in.Cancel, in.DeleteBack, in.ClearInput},
	}
}

// Commands implements tabs.Commander.
func (t *Tab) Commands() []tabs.Command {
	l, pk := t.keys.List, t.keys.PRs
	do := func(f func()) func() tea.Cmd { return func() tea.Cmd { f(); return nil } }
	cmds := []tabs.Command{
		{ID: "filter", Title: "Filter PRs", Key: l.Filter, Run: do(t.startFilter)},
		{ID: "pr_next_section", Title: "PRs: next section", Key: pk.NextSection, Run: do(func() { t.switchSection(t.active + 1) })},
		{ID: "pr_prev_section", Title: "PRs: previous section", Key: pk.PrevSection, Run: do(func() { t.switchSection(t.active - 1) })},
		{ID: "pr_repo_filter", Title: "PRs: filter by repo…", Key: pk.RepoFilter, Run: t.openRepoFilter},
	}
	if t.repo != "" {
		cmds = append(cmds, tabs.Command{ID: "pr_repo_clear", Title: "PRs: show all repos", Run: do(func() { t.setRepo(allRepos) })})
	}
	for i, s := range t.sections {
		cmds = append(cmds, tabs.Command{ID: "pr_section_" + slug(s.cfg.Title), Title: "PRs: " + s.cfg.Title, Run: do(func() { t.switchSection(i) })})
	}
	return append(cmds,
		tabs.Command{ID: "grow_pane", Title: "Widen list", Key: l.GrowPane, Run: do(func() { t.resize(t.split.Grow) })},
		tabs.Command{ID: "shrink_pane", Title: "Narrow list", Key: l.ShrinkPane, Run: do(func() { t.resize(t.split.Shrink) })},
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
		if !slices.ContainsFunc(t.sections[i].res.PRs, func(p github.PR) bool { return p.ID() == id }) {
			continue
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
		return nil
	}
	return nil
}

// CapturingInput implements tabs.InputCapturer.
func (t *Tab) CapturingInput() bool { return t.dialog != nil || t.filter.Active() }

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
