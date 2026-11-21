package plane

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/daan3011/forge/internal/browser"
	"github.com/daan3011/forge/internal/clipboard"
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/refresh"
	"github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
)

// Title is the tab's title, which also routes its messages.
const Title = "Plane"

// EntityIssue is the palette group of work items.
const EntityIssue = "Issue"

const (
	// listRatio is the list's starting share of the width.
	listRatio = 0.6
	// filterBoxHeight is the bordered filter box above the table.
	filterBoxHeight = 3
	// listLimit caps the items fetched per project and filter.
	listLimit = 1000
	// metaTTL is how long a project's states, labels, cycles and members
	// are kept before they are fetched again.
	metaTTL = 10 * time.Minute

	fetchTimeout = 60 * time.Second
	shortTimeout = 30 * time.Second
)

// listExpand are the objects expanded in list responses, so a cached list
// renders on its own.
var listExpand = []string{"state", "labels", "assignees"}

// projectItems is one Plane project's items for one filter.
type projectItems struct {
	id         string // Plane project id
	name       string // forge project name, or "" when not configured
	key        string // project + filters the list is for
	filter     plane.WorkItemFilter
	items      []plane.WorkItem
	loaded     bool
	fresh      bool // a fetch for key has landed; ignore older cached data
	fetching   bool
	cacheAsked bool
	updated    time.Time
}

// projectMeta is a project's metadata, for the filter form, the filter
// box and names the list doesn't expand (comment authors).
type projectMeta struct {
	project plane.Project
	states  []plane.State
	labels  []plane.Label
	cycles  []plane.Cycle
	members []plane.Member
	at      time.Time
}

// Tab is the Plane tab (P3.2): work items of the active project's Plane
// project, or of every configured one.
type Tab struct {
	theme    *ui.Theme
	keys     *ui.KeyMap
	api      plane.API
	apiErr   error
	linker   tabs.ProjectLinker
	sessions sessions.API
	browser  browser.Opener
	clip     clipboard.Copier
	cfg      *config.Config
	now      func() time.Time
	// tick arms timers (tea.Tick; tests replace it).
	tick func(time.Duration, func(time.Time) tea.Msg) tea.Cmd

	split  ui.SplitPane
	table  ui.Table
	filter ui.Filter
	width  int
	height int

	project *config.Project
	filt    filters
	me      plane.User
	asking  bool // Me is being fetched
	mode    viewMode
	// lists are the current view's lists: one per project for the list,
	// two (open / recently closed) for the board.
	lists []*projectItems
	// pool keeps both views' lists, so `b` shows data at once.
	pool map[string]*projectItems

	board   boardState
	overlay bool                    // the board's card detail is open
	moves   map[string]*pendingMove // state changes on their way, by item id

	meta     map[string]*projectMeta // by Plane project id
	metaBusy map[string]bool

	// synced is the state name last written to the links table per key.
	synced map[string]string

	dialog   *huh.Form
	onDone   func() tea.Cmd
	fetchErr error // the last fetch failed (shown while nothing is loaded)
	errShown bool  // a fetch error was toasted since the last Refresh

	det  detailState
	link linkState

	queued *pendingForm // a create / edit form waiting for its data
}

var (
	_ tabs.Tab            = (*Tab)(nil)
	_ tabs.InputCapturer  = (*Tab)(nil)
	_ tabs.Commander      = (*Tab)(nil)
	_ tabs.EntityProvider = (*Tab)(nil)
	_ tabs.Updater        = (*Tab)(nil)
	_ tabs.Fetcher        = (*Tab)(nil)
	_ tabs.AutoRefresher  = (*Tab)(nil)
	_ tabs.StatusDetailer = (*Tab)(nil)
)

// New returns the Plane tab.
func New(d tabkit.Deps) tabs.Tab { return newTab(d) }

func newTab(d tabkit.Deps) *Tab {
	cfg := d.Config
	if cfg == nil {
		cfg = config.Default()
	}
	t := &Tab{
		theme: d.Theme, keys: d.Keys, api: d.Plane, apiErr: d.PlaneErr, linker: d.ProjectLinks,
		sessions: d.Sessions, browser: d.Browser, clip: d.Clipboard, cfg: cfg,
		now: time.Now, tick: tea.Tick,
		split:    ui.NewSplitPane(d.Theme),
		filter:   ui.NewFilter(d.Theme, d.Keys),
		filt:     defaultFilters(),
		meta:     map[string]*projectMeta{},
		pool:     map[string]*projectItems{},
		moves:    map[string]*pendingMove{},
		board:    newBoardState(),
		metaBusy: map[string]bool{},
		synced:   map[string]string{},
		det:      newDetailState(),
		link:     newLinkState(),
	}
	t.split.SetRatio(listRatio)
	t.table = ui.NewTable(t.theme, t.keys, t.columns())
	t.table.SetEmptyView(t.emptyView)
	t.rebuildLists()
	return t
}

// --- projects & lists ------------------------------------------------------------

// planeProject is a Plane project the tab lists.
type planeProject struct{ id, name string }

// projects are the Plane projects listed: the override, the active
// project's, or every configured project's.
func (t *Tab) projects() []planeProject {
	if t.filt.project != "" {
		return []planeProject{{id: t.filt.project, name: t.forgeName(t.filt.project)}}
	}
	if t.project != nil {
		if t.project.PlaneProjectID == "" {
			return nil
		}
		return []planeProject{{id: t.project.PlaneProjectID, name: t.project.Name}}
	}
	var out []planeProject
	for _, p := range t.cfg.Projects {
		if p.PlaneProjectID != "" && !slices.ContainsFunc(out, func(q planeProject) bool { return q.id == p.PlaneProjectID }) {
			out = append(out, planeProject{id: p.PlaneProjectID, name: p.Name})
		}
	}
	return out
}

// forgeName is the forge project using Plane project id, or "".
func (t *Tab) forgeName(id string) string {
	for _, p := range t.cfg.Projects {
		if p.PlaneProjectID == id {
			return p.Name
		}
	}
	return ""
}

// projectIDs are the listed Plane projects, once each.
func (t *Tab) projectIDs() []string {
	var out []string
	for _, l := range t.lists {
		if !slices.Contains(out, l.id) {
			out = append(out, l.id)
		}
	}
	return out
}

// single reports whether exactly one project is listed (label and cycle
// filters only make sense then).
func (t *Tab) single() bool { return len(t.projectIDs()) == 1 }

// apiFilter is what Plane is asked for in project id.
func (t *Tab) apiFilter(single bool) plane.WorkItemFilter {
	f := plane.WorkItemFilter{
		StateGroups: t.filt.groups,
		Expand:      listExpand,
		OrderBy:     "-updated_at",
		Limit:       listLimit,
	}
	switch t.filt.assignee {
	case "":
	case assigneeMe:
		if t.me.ID != "" {
			f.AssigneeIDs = []string{t.me.ID}
		}
	default:
		f.AssigneeIDs = []string{t.filt.assignee}
	}
	if single {
		if t.filt.label != "" {
			f.LabelIDs = []string{t.filt.label}
		}
		f.CycleID = t.filt.cycle
	}
	return f
}

// closedGroups are the state groups the board limits by age.
var closedGroups = []plane.StateGroup{plane.GroupCompleted, plane.GroupCancelled}

// viewFilters are what Plane is asked for per project in mode: the list's
// filter, or for the board every state (closed ones updated since the
// cutoff day) with the other filters kept.
func (t *Tab) viewFilters(mode viewMode, single bool) []plane.WorkItemFilter {
	f := t.apiFilter(single)
	if mode == modeList {
		return []plane.WorkItemFilter{f}
	}
	open, closed := f, f
	open.StateGroups = slices.Clone(openGroups)
	closed.StateGroups = slices.Clone(closedGroups)
	closed.UpdatedAfter = t.doneCutoff()
	return []plane.WorkItemFilter{open, closed}
}

// doneCutoff is the start of the day board_done_days ago, so the closed
// lists' cache key stays the same all day.
func (t *Tab) doneCutoff() time.Time {
	n := t.now()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, n.Location()).AddDate(0, 0, -t.cfg.Plane.DoneDays())
}

// listKey identifies a project's list under a filter.
func listKey(id string, f plane.WorkItemFilter) string {
	after := ""
	if !f.UpdatedAfter.IsZero() {
		after = f.UpdatedAfter.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("%s|%v|%v|%v|%s|%s", id, f.StateGroups, f.AssigneeIDs, f.LabelIDs, f.CycleID, after)
}

// rebuildLists lays out the lists for the current project, filters and
// view. The pool keeps both views' lists for the current project and
// filters and drops the rest.
func (t *Tab) rebuildLists() {
	ps := t.projects()
	single := len(ps) == 1
	keep := map[string]bool{}
	t.lists = nil
	for _, mode := range []viewMode{modeList, modeBoard} {
		for _, p := range ps {
			for _, f := range t.viewFilters(mode, single) {
				k := listKey(p.id, f)
				keep[k] = true
				l := t.pool[k]
				if l == nil {
					l = &projectItems{id: p.id, name: p.name, key: k, filter: f}
					t.pool[k] = l
				}
				if mode == t.mode {
					t.lists = append(t.lists, l)
				}
			}
		}
	}
	for k := range t.pool {
		if !keep[k] {
			delete(t.pool, k)
		}
	}
}

func (t *Tab) byKey(k string) *projectItems {
	for _, l := range t.lists {
		if l.key == k {
			return l
		}
	}
	return nil
}

// --- loading ---------------------------------------------------------------------

// Init implements tabs.Tab.
func (t *Tab) Init() tea.Cmd { return t.Refresh() }

// Refresh implements tabs.Tab: each list loads its cached copy once per key
// and fetches unless a fetch is running; metadata is fetched when missing
// or old. With "assigned to me" and no user yet, the user comes first.
func (t *Tab) Refresh() tea.Cmd {
	t.errShown = false
	if t.api == nil {
		return nil
	}
	cmds := []tea.Cmd{t.ensureLinks()}
	if t.filt.assignee == assigneeMe && t.me.ID == "" {
		return tea.Batch(append(cmds, t.askMe())...)
	}
	for _, l := range t.lists {
		cmds = append(cmds, t.loadMeta(l.id))
		f := l.filter
		if !l.cacheAsked {
			l.cacheAsked = true
			cmds = append(cmds, loadCached(t.api, l.key, l.id, f))
		}
		if l.fetching {
			continue
		}
		l.fetching = true
		cmds = append(cmds, fetchItems(t.api, l.key, l.id, f, t.now))
	}
	return tea.Batch(cmds...)
}

// itemsMsg carries a project's items, from the cache or a fetch.
type itemsMsg struct {
	key    string
	items  []plane.WorkItem
	at     time.Time
	cached bool
	err    error
}

// meMsg carries the API key's user.
type meMsg struct {
	user plane.User
	err  error
}

// metaMsg carries a project's metadata.
type metaMsg struct {
	id   string
	meta projectMeta
	err  error
}

// TargetTab implements tabs.TargetedMsg.
func (itemsMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (meMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (metaMsg) TargetTab() string { return Title }

func loadCached(api plane.API, key, id string, f plane.WorkItemFilter) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		c, ok := api.CachedWorkItems(ctx, id, f)
		if !ok {
			return nil
		}
		return itemsMsg{key: key, items: c.Items, at: c.FetchedAt, cached: true}
	}
}

func fetchItems(api plane.API, key, id string, f plane.WorkItemFilter, now func() time.Time) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		items, err := api.WorkItems(ctx, id, f)
		return itemsMsg{key: key, items: items, at: now(), err: err}
	}
}

func (t *Tab) askMe() tea.Cmd {
	if t.api == nil || t.asking {
		return nil
	}
	t.asking = true
	api := t.api
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), shortTimeout)
		defer cancel()
		u, err := api.Me(ctx)
		return meMsg{user: u, err: err}
	}
}

// loadMeta fetches a project's metadata unless it is fresh or on its way.
func (t *Tab) loadMeta(id string) tea.Cmd {
	if m := t.meta[id]; t.api == nil || t.metaBusy[id] || m != nil && t.now().Sub(m.at) < metaTTL {
		return nil
	}
	t.metaBusy[id] = true
	api, now := t.api, t.now
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		var (
			m    projectMeta
			err  error
			errs []error
		)
		if m.project, err = api.Project(ctx, id); err != nil {
			errs = append(errs, err)
		}
		if m.states, err = api.States(ctx, id); err != nil {
			errs = append(errs, err)
		}
		if m.labels, err = api.Labels(ctx, id); err != nil {
			errs = append(errs, err)
		}
		if m.cycles, err = api.Cycles(ctx, id); err != nil {
			errs = append(errs, err)
		}
		if m.members, err = api.ProjectMembers(ctx, id); err != nil {
			errs = append(errs, err)
		}
		m.at = now()
		return metaMsg{id: id, meta: m, err: errors.Join(errs...)}
	}
}

func (t *Tab) itemsLoaded(msg itemsMsg) tea.Cmd {
	l := t.byKey(msg.key)
	if l == nil {
		return nil // for filters or a project we have since left
	}
	if msg.cached {
		if l.fresh {
			return nil
		}
		l.items, l.loaded, l.updated = msg.items, true, msg.at
		t.reapplyMoves(l)
		t.setRows()
		return nil
	}
	l.fetching = false
	if msg.err != nil {
		t.fetchErr = msg.err
		return t.toastOnce(msg.err)
	}
	t.fetchErr = nil
	l.items, l.loaded, l.fresh, l.updated = msg.items, true, true, msg.at
	t.reapplyMoves(l)
	t.setRows()
	return t.syncStates(l.items)
}

func (t *Tab) meLoaded(msg meMsg) tea.Cmd {
	t.asking = false
	if msg.err != nil {
		t.fetchErr = msg.err
		if plane.IsAuthError(msg.err) || errors.Is(msg.err, plane.ErrNoV2) {
			return t.toastOnce(msg.err)
		}
		// Not knowing who I am shouldn't hide everything: show anyone's.
		t.filt.assignee = ""
		t.rebuildLists()
		return tea.Batch(errCmd(fmt.Errorf("plane: who am I? %s", reason(msg.err))), t.Refresh())
	}
	t.me = msg.user
	t.rebuildLists()
	t.setRows()
	return t.Refresh()
}

func (t *Tab) metaLoaded(msg metaMsg) tea.Cmd {
	delete(t.metaBusy, msg.id)
	m := msg.meta
	t.meta[msg.id] = &m
	t.setRows() // names the list didn't expand
	open := t.openPending()
	if msg.err != nil {
		return tea.Batch(open, t.toastOnce(fmt.Errorf("project details: %w", msg.err)))
	}
	return open
}

// toastOnce reports err unless this refresh already reported one.
func (t *Tab) toastOnce(err error) tea.Cmd {
	if t.errShown {
		return nil
	}
	t.errShown = true
	return errCmd(fmt.Errorf("plane: %s", reason(err)))
}

// syncStates writes the items' current states to the links table (P1.9),
// for the keys whose state changed since the last sync.
func (t *Tab) syncStates(items []plane.WorkItem) tea.Cmd {
	if t.sessions == nil {
		return nil
	}
	changed := map[string]string{}
	for _, w := range items {
		name, _ := t.stateOf(w)
		if name == "" || w.Identifier == "" {
			continue
		}
		if prev, ok := t.synced[w.Identifier]; !ok || prev != name {
			changed[w.Identifier] = name
			t.synced[w.Identifier] = name
		}
	}
	if len(changed) == 0 {
		return nil
	}
	api := t.sessions
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := api.SetIssueStates(ctx, changed); err != nil {
			return ui.ErrMsg{Err: fmt.Errorf("plane: issue states: %w", err)}
		}
		return nil
	}
}

// reason is err for people.
func reason(err error) string {
	switch {
	case plane.IsAuthError(err):
		return "API key rejected (plane.api_key or $PLANE_API_KEY)"
	case errors.Is(err, plane.ErrNoV2):
		return "this Plane instance has no /api/v2"
	}
	return err.Error()
}

func errCmd(err error) tea.Cmd {
	return func() tea.Msg { return ui.ErrMsg{Err: err} }
}

// --- update ----------------------------------------------------------------------

// Update implements tabs.Tab. After every message the detail pane catches
// up with the selection.
func (t *Tab) Update(msg tea.Msg) (tabs.Tab, tea.Cmd) {
	cmd := t.update(msg)
	return t, tea.Batch(cmd, t.syncDetail(), t.ensureRendered())
}

func (t *Tab) update(msg tea.Msg) tea.Cmd {
	if cmd, ok := t.updateDetail(msg); ok {
		return cmd
	}
	if cmd, ok := t.updateEdit(msg); ok {
		return cmd
	}
	switch msg := msg.(type) {
	case itemsMsg:
		return t.itemsLoaded(msg)
	case meMsg:
		return t.meLoaded(msg)
	case metaMsg:
		return t.metaLoaded(msg)
	case planeProjectsMsg:
		return t.planeProjectsLoaded(msg)
	case linkFailedMsg:
		return t.linkFailed(msg)
	case moveDoneMsg:
		return t.moveDone(msg)
	case clipboardFallbackMsg:
		return tea.Batch(tea.SetClipboard(msg.text), func() tea.Msg { return ui.InfoMsg{Text: "copied " + msg.text} })
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
	if t.mode == modeBoard {
		return t.updateBoard(kp)
	}
	if cmd, ok := t.itemAction(kp); ok {
		return cmd
	}
	l, pk := t.keys.List, t.keys.Plane
	switch {
	case key.Matches(kp, l.Filter):
		t.startFilter()
	case key.Matches(kp, t.keys.Input.Cancel) && t.filter.Value() != "":
		t.clearFilter()
	case key.Matches(kp, pk.Board):
		return t.toggleBoard()
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
	case key.Matches(kp, pk.CopyURL):
		return t.copyURL()
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

// --- items & rows ----------------------------------------------------------------

// items is every loaded item of the current view, once each (the newest
// copy), most recently updated first.
func (t *Tab) items() []plane.WorkItem {
	var out []plane.WorkItem
	at := map[string]int{}
	for _, l := range t.lists {
		for _, w := range l.items {
			if i, ok := at[w.ID]; ok {
				if w.UpdatedAt.After(out[i].UpdatedAt.Time) {
					out[i] = w
				}
				continue
			}
			at[w.ID] = len(out)
			out = append(out, w)
		}
	}
	slices.SortStableFunc(out, func(a, b plane.WorkItem) int {
		if c := b.UpdatedAt.Compare(a.UpdatedAt.Time); c != 0 {
			return c
		}
		return cmp.Compare(b.SequenceID, a.SequenceID)
	})
	return out
}

func (t *Tab) loaded() bool {
	return slices.ContainsFunc(t.lists, func(l *projectItems) bool { return l.loaded })
}

// setRows fills the table, keeping the selected item selected.
func (t *Tab) setRows() {
	sel := t.selectedID()
	var rows [][]string
	for _, w := range t.items() {
		rows = append(rows, t.itemRow(w))
	}
	t.table.SetRows(rows)
	if sel != "" {
		t.selectID(sel)
	}
	t.rebuildBoard()
}

func (t *Tab) selectedID() string {
	if t.mode == modeBoard {
		return t.board.selID
	}
	if row := t.table.SelectedRow(); row != nil {
		return row[len(row)-1]
	}
	return ""
}

func (t *Tab) selectID(id string) bool {
	for i, row := range t.table.VisibleRows() {
		if row[len(row)-1] == id {
			t.table.SetCursor(i)
			return true
		}
	}
	return false
}

// selected is the item under the cursor.
func (t *Tab) selected() (plane.WorkItem, bool) {
	id := t.selectedID()
	if id == "" {
		return plane.WorkItem{}, false
	}
	return t.itemByID(id)
}

// itemByID finds an item in the current view, else in the other one.
func (t *Tab) itemByID(id string) (plane.WorkItem, bool) {
	for _, l := range t.lists {
		for _, w := range l.items {
			if w.ID == id {
				return w, true
			}
		}
	}
	for _, l := range t.pool {
		for _, w := range l.items {
			if w.ID == id {
				return w, true
			}
		}
	}
	return plane.WorkItem{}, false
}

// --- actions ---------------------------------------------------------------------

// clipboardFallbackMsg asks for the URL to be copied with OSC 52 because no
// copy tool was found.
type clipboardFallbackMsg struct{ text string }

// TargetTab implements tabs.TargetedMsg.
func (clipboardFallbackMsg) TargetTab() string { return Title }

func (t *Tab) itemURL() (string, string, error) {
	w, ok := t.selected()
	if !ok {
		return "", "", errors.New("no issue selected")
	}
	u := t.cfg.Plane.IssueURL(w.Key())
	if u == "" {
		return "", w.Key(), fmt.Errorf("%s: no URL (plane.base_url / workspace_slug)", w.Key())
	}
	return u, w.Key(), nil
}

// openInBrowser opens the selected item in Plane.
func (t *Tab) openInBrowser() tea.Cmd {
	u, k, err := t.itemURL()
	if err != nil {
		return errCmd(err)
	}
	if t.browser == nil {
		return errCmd(errors.New("can't open a browser here"))
	}
	b := t.browser
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := b.Open(ctx, u); err != nil {
			return ui.ErrMsg{Err: fmt.Errorf("open %s: %w", k, err)}
		}
		return nil
	}
}

// copyURL copies the selected item's URL.
func (t *Tab) copyURL() tea.Cmd {
	u, k, err := t.itemURL()
	if err != nil {
		return errCmd(err)
	}
	if t.clip == nil {
		return func() tea.Msg { return clipboardFallbackMsg{text: u} }
	}
	c := t.clip
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := c.Copy(ctx, u)
		switch {
		case errors.Is(err, clipboard.ErrNoTool):
			return clipboardFallbackMsg{text: u}
		case err != nil:
			return ui.ErrMsg{Err: fmt.Errorf("copy URL of %s: %w", k, err)}
		}
		return ui.InfoMsg{Text: "copied " + u}
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

// SetProject implements tabs.Tab: the list follows the project. The
// project override and the per-project label and cycle filters are
// dropped; assignee and states stay.
func (t *Tab) SetProject(p *config.Project) {
	t.project = p
	t.queued = nil
	t.filt.project, t.filt.label, t.filt.cycle = "", "", ""
	if t.filter.Visible() {
		t.clearFilter()
	}
	t.lists = nil
	t.pool = map[string]*projectItems{}
	t.overlay = false
	t.rebuildLists()
	t.table.SetRows(nil)
	t.rebuildBoard()
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
	pk := t.keys.Plane
	switch {
	case t.mode == modeBoard && t.overlay:
		return []key.Binding{t.keys.Input.Cancel, pk.MovePrev, pk.MoveNext, pk.Edit, pk.Comment, pk.Session, pk.Open}
	case t.mode == modeBoard:
		return []key.Binding{pk.ColLeft, pk.ColRight, pk.MovePrev, pk.MoveNext, pk.OpenCard, pk.New, pk.Edit, pk.Board}
	}
	return []key.Binding{pk.New, pk.Edit, pk.Comment, pk.Filters, pk.Mine, t.keys.List.Filter, pk.Board}
}

// FullHelp implements tabs.Tab.
func (t *Tab) FullHelp() [][]key.Binding {
	l, in, pk := t.keys.List, t.keys.Input, t.keys.Plane
	if t.mode == modeBoard {
		return [][]key.Binding{
			{pk.ColLeft, pk.ColRight, l.Up, l.Down, l.Top, l.Bottom},
			{pk.MovePrev, pk.MoveNext, pk.CollapseColumn, pk.CollapseEmpty, pk.OpenCard, in.Cancel},
			{l.Filter, pk.Filters, pk.Mine, pk.ClearFilters, pk.Board},
			{pk.DetailDown, pk.DetailUp, pk.Open, pk.CopyURL},
			{pk.New, pk.Edit, pk.Comment, pk.Session},
		}
	}
	return [][]key.Binding{
		{l.Up, l.Down, l.Top, l.Bottom, l.PageDown, l.PageUp},
		{l.Filter, pk.Filters, pk.Mine, pk.ClearFilters, l.ShrinkPane, l.GrowPane},
		{pk.DetailDown, pk.DetailUp, pk.Open, pk.CopyURL, pk.Board},
		{pk.New, pk.Edit, pk.Comment, pk.Session},
		{in.Accept, in.Cancel, in.DeleteBack, in.ClearInput},
	}
}

// Commands implements tabs.Commander.
func (t *Tab) Commands() []tabs.Command {
	l, pk := t.keys.List, t.keys.Plane
	do := func(f func()) func() tea.Cmd { return func() tea.Cmd { f(); return t.syncDetail() } }
	mine := "Plane: show only mine"
	if t.filt.assignee == assigneeMe {
		mine = "Plane: show everyone's"
	}
	board := "Plane: show board"
	if t.mode == modeBoard {
		board = "Plane: show list"
	}
	boardOnly := func(f func() tea.Cmd) func() tea.Cmd {
		return func() tea.Cmd {
			if t.mode != modeBoard {
				return tea.Batch(t.toggleBoard(), f())
			}
			return f()
		}
	}
	return []tabs.Command{
		{ID: "filter", Title: "Filter issues", Key: l.Filter, Run: do(t.startFilter)},
		{ID: "plane_filters", Title: "Plane: filters…", Key: pk.Filters, Run: t.openFilters},
		{ID: "plane_mine", Title: mine, Key: pk.Mine, Run: t.toggleMine},
		{ID: "plane_clear_filters", Title: "Plane: clear filters", Key: pk.ClearFilters, Run: t.clearFilters},
		{ID: "plane_detail_down", Title: "Plane: scroll detail down", Key: pk.DetailDown, Run: do(func() { t.scrollDetail(1) })},
		{ID: "plane_detail_up", Title: "Plane: scroll detail up", Key: pk.DetailUp, Run: do(func() { t.scrollDetail(-1) })},
		{ID: "plane_link_project", Title: "Plane: link project…", Run: t.linkProject},
		{ID: "plane_open", Title: "Issue: open in browser", Key: pk.Open, Run: t.openInBrowser},
		{ID: "plane_copy_url", Title: "Issue: copy URL", Key: pk.CopyURL, Run: t.copyURL},
		{ID: "plane_new", Title: "Issue: new…", Key: pk.New, Run: t.newItem},
		{ID: "plane_edit", Title: "Issue: edit…", Key: pk.Edit, Run: t.editSelected},
		{ID: "plane_comment", Title: "Issue: comment…", Key: pk.Comment, Run: t.commentSelected},
		{ID: "plane_session", Title: "Issue: start a forge session", Key: pk.Session, Run: t.startSession},
		{ID: "plane_board", Title: board, Key: pk.Board, Run: t.toggleBoard},
		{ID: "plane_col_left", Title: "Board: previous column", Key: pk.ColLeft, Run: boardOnly(func() tea.Cmd { return t.moveColumn(-1) })},
		{ID: "plane_col_right", Title: "Board: next column", Key: pk.ColRight, Run: boardOnly(func() tea.Cmd { return t.moveColumn(1) })},
		{ID: "plane_move_prev", Title: "Issue: move to previous state", Key: pk.MovePrev, Run: func() tea.Cmd { return t.moveCard(-1) }},
		{ID: "plane_move_next", Title: "Issue: move to next state", Key: pk.MoveNext, Run: func() tea.Cmd { return t.moveCard(1) }},
		{ID: "plane_collapse_column", Title: "Board: collapse / expand column", Key: pk.CollapseColumn, Run: boardOnly(t.toggleColumn)},
		{ID: "plane_collapse_empty", Title: "Board: collapse / expand empty columns", Key: pk.CollapseEmpty, Run: boardOnly(t.toggleCollapseEmpty)},
		{ID: "plane_open_card", Title: "Board: open card", Key: pk.OpenCard, Run: boardOnly(t.openCard)},
		{ID: "grow_pane", Title: "Widen list", Key: l.GrowPane, Run: do(func() { t.split.Grow(); t.resizeTable() })},
		{ID: "shrink_pane", Title: "Narrow list", Key: l.ShrinkPane, Run: do(func() { t.split.Shrink(); t.resizeTable() })},
	}
}

// Entities implements tabs.EntityProvider: every loaded item, found by key
// or title.
func (t *Tab) Entities() []tabs.Entity {
	var out []tabs.Entity
	for _, w := range t.items() {
		state, _ := t.stateOf(w)
		detail := []string{}
		for _, s := range []string{state, assigneeCell(t.assigneeNames(w)), t.projectLabel(w.ProjectID)} {
			if s != "" {
				detail = append(detail, s)
			}
		}
		out = append(out, tabs.Entity{
			Kind: EntityIssue, ID: w.ID,
			Title:  w.Key() + " " + w.Name,
			Detail: strings.Join(detail, " · "),
			Search: strings.Join(t.labelNames(w), " "),
		})
	}
	return out
}

// projectLabel names a Plane project: the forge project, else its
// identifier.
func (t *Tab) projectLabel(id string) string {
	if n := t.forgeName(id); n != "" {
		return n
	}
	if m := t.meta[id]; m != nil {
		return m.project.Identifier
	}
	return ""
}

// Select implements tabs.EntityProvider, dropping the text filter if it
// hides the item.
func (t *Tab) Select(id string) tea.Cmd {
	if t.mode == modeBoard {
		t.board.pick(id)
		t.rebuildBoard()
		if t.board.selID != id && t.filter.Visible() {
			t.clearFilter() // the / filter hides it
		}
		return t.syncDetail()
	}
	if t.selectID(id) {
		return t.syncDetail()
	}
	if t.filter.Visible() {
		t.clearFilter()
	}
	t.selectID(id)
	return t.syncDetail()
}

// CapturingInput implements tabs.InputCapturer.
func (t *Tab) CapturingInput() bool { return t.dialog != nil || t.filter.Active() }

// LastUpdated implements tabs.Updater: the oldest list's fetch, zero until
// every list has data.
func (t *Tab) LastUpdated() time.Time {
	var oldest time.Time
	for _, l := range t.lists {
		if l.updated.IsZero() {
			return time.Time{}
		}
		if oldest.IsZero() || l.updated.Before(oldest) {
			oldest = l.updated
		}
	}
	return oldest
}

// Fetching implements tabs.Fetcher.
func (t *Tab) Fetching() bool {
	return t.asking || slices.ContainsFunc(t.lists, func(l *projectItems) bool { return l.fetching })
}

// Sources implements tabs.AutoRefresher.
func (t *Tab) Sources() []refresh.Source { return []refresh.Source{refresh.Plane} }

// RefreshWhenHidden implements tabs.AutoRefresher: no, Plane is rate
// limited.
func (t *Tab) RefreshWhenHidden() bool { return false }

// StatusDetail implements tabs.StatusDetailer: "@daan · issue 3/40".
func (t *Tab) StatusDetail() string {
	if !t.loaded() {
		return ""
	}
	var parts []string
	if t.me.ID != "" {
		parts = append(parts, "@"+userName(t.me))
	}
	var s string
	if t.mode == modeBoard {
		s = t.boardStatus()
	} else {
		n := len(t.table.VisibleRows())
		pos := 0
		if n > 0 {
			pos = t.table.Cursor() + 1
		}
		s = fmt.Sprintf("issue %d/%d", pos, n)
	}
	if slices.ContainsFunc(t.lists, func(l *projectItems) bool { return len(l.items) >= listLimit }) {
		s += fmt.Sprintf(" (first %d per project)", listLimit)
	}
	return strings.Join(append(parts, s), " · ")
}

// --- view ------------------------------------------------------------------------

// View implements tabs.Tab.
func (t *Tab) View() string {
	if t.dialog != nil {
		w, h := t.dialogSize()
		return t.theme.PaneFocused.Render(ui.Box(t.dialog.View(), w, h))
	}
	if t.mode == modeBoard {
		return t.boardScreen()
	}
	lw, _ := t.split.LeftSize()
	list := t.filterBox(lw)
	if t.filter.Visible() {
		list += "\n" + t.filter.View(lw)
	}
	list += "\n" + t.table.View()
	rw, rh := t.split.RightSize()
	return t.split.View(list, t.detailView(rw, rh), true)
}

func (t *Tab) emptyView(w, h int) string {
	th := t.theme
	msg := ""
	switch {
	case t.api == nil:
		msg = "Plane isn't set up"
		if t.apiErr != nil {
			msg += ": " + t.apiErr.Error()
		}
	case len(t.lists) == 0:
		msg = t.linkHint()
	case !t.loaded() && t.fetchErr != nil:
		msg = "couldn't load issues: " + reason(t.fetchErr)
	case !t.loaded() || t.asking:
		msg = "heating up…"
	case !t.filt.isZero():
		msg = "no issues match · " + t.keys.Plane.ClearFilters.Help().Key + " clears filters"
	}
	if msg == "" {
		return anim.EmptyForge(w, h, th)
	}
	return ui.Box(th.TableEmpty.Render(msg), w, h)
}
