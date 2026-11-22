package home

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/browser"
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/refresh"
	"github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
)

// Title is the tab's title, which also routes its messages.
const Title = "Home"

const (
	// listRatio is the "needs me" list's share of the width.
	listRatio = 0.66
	// coldCaption is the empty state's caption; partCaption replaces it
	// while a source fails, since that source may hide work.
	coldCaption = "nothing needs you · the forge is cold"
	partCaption = "nothing else needs you · a source failed (below)"
)

// Table columns.
const (
	colID = iota
	colIcon
	colItem
	colStatus
	colNext
)

// prSearch is one of Home's two PR searches.
type prSearch struct {
	which      string // cache key suffix: "reviews" / "mine"
	query      string
	res        github.SearchResult
	cacheAsked string
	fresh      bool // a fetch for the current key has landed
	loadState
}

// Tab is the Home tab (P6.1): one prioritised "needs me" list across agents,
// PRs and Plane, with running agents, pinned artifacts and today's note on
// the side.
type Tab struct {
	theme    *ui.Theme
	keys     *ui.KeyMap
	anim     *anim.Engine
	cfg      *config.Config
	store    tabs.KVStore
	svc      sessions.API
	gh       github.PRClient
	plane    plane.API
	planeErr error
	arts     tabs.ArtifactIndex
	browser  browser.Opener
	notesDir string
	now      func() time.Time

	split ui.SplitPane
	table ui.Table

	project *config.Project
	gen     int // bumps on SetProject; older loads are dropped

	agents  loadState
	rows    []sessions.Row
	reviews prSearch
	mine    prSearch
	issues  loadState
	me      plane.User
	items   []plane.WorkItem
	pins    []store.Artifact
	pinsErr error
	note    []string
	noteOK  bool // the note was read (it may not exist)

	list []item // the ranked rows, as shown
}

var (
	_ tabs.Tab             = (*Tab)(nil)
	_ tabs.Commander       = (*Tab)(nil)
	_ tabs.Updater         = (*Tab)(nil)
	_ tabs.Fetcher         = (*Tab)(nil)
	_ tabs.AutoRefresher   = (*Tab)(nil)
	_ tabs.SourceRefresher = (*Tab)(nil)
	_ tabs.SessionWatcher  = (*Tab)(nil)
	_ tabs.Animator        = (*Tab)(nil)
	_ tabs.StatusDetailer  = (*Tab)(nil)
)

// New returns the Home tab.
func New(d tabkit.Deps) tabs.Tab { return newTab(d) }

func newTab(d tabkit.Deps) *Tab {
	cfg := d.Config
	if cfg == nil {
		cfg = config.Default()
	}
	t := &Tab{
		theme: d.Theme, keys: d.Keys, anim: d.Engine(), cfg: cfg, store: d.Store,
		svc: d.Sessions, gh: d.GitHub, plane: d.Plane, planeErr: d.PlaneErr,
		arts: d.Artifacts, browser: d.Browser, notesDir: d.NotesDir,
		now:     time.Now,
		split:   ui.NewSplitPane(d.Theme),
		reviews: prSearch{which: "reviews", query: reviewsQuery},
		mine:    prSearch{which: "mine", query: mineQuery},
	}
	t.split.SetRatio(listRatio)
	t.table = ui.NewTable(d.Theme, d.Keys, t.columns())
	t.table.SetEmptyView(t.emptyView)
	return t
}

func (t *Tab) columns() []ui.Column {
	return []ui.Column{
		{Title: "id", Hidden: true},
		{Title: "", Width: 1, Render: t.renderIcon},
		{Title: "Needs you", Flex: true, Width: 16},
		{Title: "Status", Width: 16, Render: t.renderStatus},
		{Title: "Next", Width: 12, Render: t.renderNext},
	}
}

// Init implements tabs.Tab.
func (t *Tab) Init() tea.Cmd { return t.Refresh() }

// Refresh implements tabs.Tab: every source reloads (the PR searches
// cache-first).
func (t *Tab) Refresh() tea.Cmd {
	return tea.Batch(t.loadSessions(), t.loadPRs(), t.loadPlane(), t.loadPins(), t.loadNote())
}

// RefreshSource implements tabs.SourceRefresher: a tick reloads only what
// it is about. Pins and the note ride on the sessions tick.
func (t *Tab) RefreshSource(src refresh.Source) tea.Cmd {
	switch src {
	case refresh.GitHub:
		return t.loadPRs()
	case refresh.Plane:
		return t.loadPlane()
	case refresh.Sessions:
		return tea.Batch(t.loadSessions(), t.loadPins(), t.loadNote())
	}
	return t.Refresh()
}

// SessionsChanged implements tabs.SessionWatcher: a hook ping reloads the
// agents at once.
func (t *Tab) SessionsChanged() tea.Cmd { return t.loadSessions() }

// Sources implements tabs.AutoRefresher.
func (t *Tab) Sources() []refresh.Source {
	return []refresh.Source{refresh.GitHub, refresh.Plane, refresh.Sessions}
}

// RefreshWhenHidden implements tabs.AutoRefresher: Home keeps up while
// hidden, so it is right whenever you come back to it.
func (t *Tab) RefreshWhenHidden() bool { return true }

// Update implements tabs.Tab.
func (t *Tab) Update(msg tea.Msg) (tabs.Tab, tea.Cmd) {
	cmd := t.update(msg)
	if _, isKey := msg.(tea.KeyPressMsg); !isKey {
		t.resize() // a source may have started or stopped failing
	}
	return t, cmd
}

func (t *Tab) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case sessionsMsg:
		t.sessionsLoaded(msg)
		return nil
	case tabkit.CacheLoadedMsg:
		t.prCacheLoaded(msg)
		return nil
	case tabkit.FetchedMsg:
		t.prFetched(msg)
		return nil
	case planeMsg:
		t.planeLoaded(msg)
		return nil
	case pinsMsg:
		if msg.gen == t.gen {
			t.pins, t.pinsErr = msg.pins, msg.err
		}
		return nil
	case noteMsg:
		if msg.gen == t.gen && msg.err == nil {
			t.note, t.noteOK = msg.lines, true
		}
		return nil
	case tea.KeyPressMsg:
		return t.handleKey(msg)
	}
	return nil
}

func (t *Tab) handleKey(kp tea.KeyPressMsg) tea.Cmd {
	l, hk := t.keys.List, t.keys.Home
	switch {
	case key.Matches(kp, hk.Open):
		return t.open()
	case key.Matches(kp, hk.Fix):
		return t.fix()
	case key.Matches(kp, hk.Merge):
		return t.merge()
	case key.Matches(kp, hk.Browse):
		return t.browse()
	case key.Matches(kp, l.GrowPane):
		t.split.Grow()
		t.resize()
	case key.Matches(kp, l.ShrinkPane):
		t.split.Shrink()
		t.resize()
	default:
		return t.table.Update(kp)
	}
	return nil
}

func (t *Tab) sessionsLoaded(msg sessionsMsg) {
	if msg.gen != t.gen {
		return
	}
	t.agents.fetching = false
	t.agents.err = msg.err
	if msg.err != nil {
		return
	}
	t.rows, t.agents.loaded, t.agents.at = msg.rows, true, msg.at
	t.rerank()
}

func (t *Tab) search(k string) *prSearch {
	for _, s := range []*prSearch{&t.reviews, &t.mine} {
		if t.prKey(s.which) == k {
			return s
		}
	}
	return nil
}

func (t *Tab) prCacheLoaded(msg tabkit.CacheLoadedMsg) {
	s := t.search(msg.Key)
	if s == nil || s.fresh {
		return
	}
	var res github.SearchResult
	if json.Unmarshal(msg.Value, &res) != nil {
		return // unreadable cache: wait for the fetch
	}
	s.res, s.loaded, s.at = res, true, msg.FetchedAt
	t.rerank()
}

func (t *Tab) prFetched(msg tabkit.FetchedMsg) {
	s := t.search(msg.Key)
	if s == nil {
		return // fetched for a project we have since left
	}
	s.fetching = false
	if msg.Err != nil {
		s.err = msg.Err
		if inner := errors.Unwrap(msg.Err); inner != nil {
			s.err = inner // drop FetchAndCache's "home:" prefix
		}
		if github.IsAuthError(msg.Err) {
			s.err = errors.New("not logged in; run `gh auth login`")
		}
		return
	}
	var res github.SearchResult
	if err := json.Unmarshal(msg.Value, &res); err != nil {
		s.err = err
		return
	}
	s.res, s.loaded, s.fresh, s.at, s.err = res, true, true, msg.At, nil
	t.rerank()
}

func (t *Tab) planeLoaded(msg planeMsg) {
	if msg.gen != t.gen {
		return
	}
	t.issues.fetching = false
	t.issues.err = msg.err
	if msg.me.ID != "" {
		t.me = msg.me
	}
	if msg.err != nil && len(msg.items) == 0 {
		return
	}
	t.items, t.issues.loaded, t.issues.at = msg.items, true, msg.at
	t.rerank()
}

// rerank rebuilds the list, keeping the cursor on the same item.
func (t *Tab) rerank() {
	keep := ""
	if row := t.table.SelectedRow(); row != nil {
		keep = row[colID]
	}
	t.list = rank(inputs{
		sessions: t.rows, reviews: t.reviews.res.PRs, mine: t.mine.res.PRs, issues: t.items,
	})
	rows := make([][]string, len(t.list))
	for i, it := range t.list {
		rows[i] = t.row(it)
	}
	t.table.SetRows(rows)
	for i, r := range t.table.VisibleRows() {
		if r[colID] == keep {
			t.table.SetCursor(i)
			break
		}
	}
}

// row is the table row of it. The icon, status and next cells start with
// the kind's number and "|", which their renderers strip to pick a style.
func (t *Tab) row(it item) []string {
	k := strconv.Itoa(int(it.kind)) + "|"
	return []string{it.id, k, t.itemText(it), k + t.statusText(it), k + t.nextText(it)}
}

func (t *Tab) multi() bool { return t.project == nil }

func (t *Tab) itemText(it item) string {
	switch it.kind {
	case kindWaiting, kindErrored:
		s := it.agent.Name
		if it.agent.Link != "" {
			s += " · " + it.agent.Link
		}
		if t.multi() && it.agent.Project != "" {
			s += " · " + it.agent.Project
		}
		return s
	case kindIssue:
		return it.issue.Key() + " " + it.issue.Name
	case kindReview:
		s := prTitle(it.pr, t.multi())
		if it.pr.Author != "" {
			s += " · @" + it.pr.Author
		}
		return s
	}
	return prTitle(it.pr, t.multi())
}

func (t *Tab) age(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return " " + ui.ShortAge(t.now().Sub(at))
}

func (t *Tab) statusText(it item) string {
	switch it.kind {
	case kindWaiting:
		return "waiting" + t.age(it.at)
	case kindErrored:
		return "errored" + t.age(it.at)
	case kindReview:
		return "review" + t.age(it.at)
	case kindFailing:
		return "CI failing"
	case kindApproved:
		return "approved, green"
	case kindIssue:
		// "<group>|<name>": the renderer colours the name by its group.
		if st := it.issue.State; st != nil {
			return string(st.Group) + "|" + st.Name
		}
		return "|"
	}
	return ""
}

// nextText is the row's one-key action, e.g. "enter attach".
func (t *Tab) nextText(it item) string {
	hk := t.keys.Home
	b, verb := hk.Open, "open"
	switch it.kind {
	case kindWaiting, kindErrored:
		verb = "attach"
	case kindReview:
		verb = "diff"
	case kindFailing:
		b, verb = hk.Fix, "fix"
	case kindApproved:
		b, verb = hk.Merge, "merge"
	}
	return b.Help().Key + " " + verb
}

// cellKind splits a "<kind>|text" cell.
func cellKind(cell string) (kind, string) {
	n, text, ok := strings.Cut(cell, "|")
	if !ok {
		return 0, cell
	}
	k, _ := strconv.Atoi(n)
	return kind(k), text
}

func (t *Tab) renderIcon(cell string, width int, row lipgloss.Style) string {
	k, _ := cellKind(cell)
	icon, st := t.theme.SourceIcon(k.source())
	return fitStyled(st.Inherit(row).Render(icon), width, row)
}

func (t *Tab) renderStatus(cell string, width int, row lipgloss.Style) string {
	k, text := cellKind(cell)
	var st lipgloss.Style
	switch k {
	case kindWaiting:
		st = t.theme.SessionStatus("waiting")
	case kindErrored:
		st = t.theme.SessionStatus("error")
	case kindReview:
		_, st = t.theme.ReviewIcon(github.ReviewRequired)
	case kindFailing:
		_, st = t.theme.CIIcon(github.CIFailure)
	case kindApproved:
		_, st = t.theme.CIIcon(github.CISuccess)
	default:
		var group string
		group, text, _ = strings.Cut(text, "|")
		st = t.theme.StateGroupStyle(group)
	}
	return fitStyled(st.Inherit(row).Render(ansi.Truncate(text, width, "…")), width, row)
}

func (t *Tab) renderNext(cell string, width int, row lipgloss.Style) string {
	_, text := cellKind(cell)
	return fitStyled(t.theme.Hint.Inherit(row).Render(ansi.Truncate(text, width, "…")), width, row)
}

// fitStyled pads s to exactly width cells on the row's background.
func fitStyled(s string, width int, row lipgloss.Style) string {
	s = ansi.Truncate(s, width, "")
	if pad := width - ansi.StringWidth(s); pad > 0 {
		s += row.Render(strings.Repeat(" ", pad))
	}
	return s
}

// selected is the item under the cursor.
func (t *Tab) selected() (item, bool) {
	row := t.table.SelectedRow()
	if row == nil {
		return item{}, false
	}
	for _, it := range t.list {
		if it.id == row[colID] {
			return it, true
		}
	}
	return item{}, false
}

// SetSize implements tabs.Tab.
func (t *Tab) SetSize(width, height int) {
	t.split.SetSize(width, height)
	t.resize()
}

func (t *Tab) resize() {
	w, h := t.split.LeftSize()
	t.table.SetSize(w, h-t.footerHeight())
}

// footerHeight is the lines under the list for source errors.
func (t *Tab) footerHeight() int { return len(t.problems()) }

// SetProject implements tabs.Tab: everything is dropped and reloaded for
// the new project.
func (t *Tab) SetProject(p *config.Project) {
	t.project = p
	t.gen++
	t.agents, t.issues = loadState{}, loadState{}
	t.rows, t.items, t.pins, t.pinsErr, t.note, t.noteOK = nil, nil, nil, nil, nil, false
	t.reviews = prSearch{which: t.reviews.which, query: t.reviews.query}
	t.mine = prSearch{which: t.mine.which, query: t.mine.query}
	t.rerank()
	t.resize()
}

// Title implements tabs.Tab.
func (t *Tab) Title() string { return Title }

// LastUpdated implements tabs.Updater: the oldest load among the sources
// that loaded, so the status bar never claims more freshness than there is.
func (t *Tab) LastUpdated() time.Time {
	var oldest time.Time
	for _, s := range t.sources() {
		if s.loaded && !s.at.IsZero() && (oldest.IsZero() || s.at.Before(oldest)) {
			oldest = s.at
		}
	}
	return oldest
}

// Fetching implements tabs.Fetcher.
func (t *Tab) Fetching() bool {
	return slices.ContainsFunc(t.sources(), func(s loadState) bool { return s.fetching })
}

// sources are the enabled sources' load states.
func (t *Tab) sources() []loadState {
	var out []loadState
	if t.svc != nil {
		out = append(out, t.agents)
	}
	if t.gh != nil && t.hasRepo() {
		out = append(out, t.reviews.loadState, t.mine.loadState)
	}
	if t.plane != nil && len(t.planeProjects()) > 0 {
		out = append(out, t.issues)
	}
	return out
}

// hasRepo reports whether there are PRs to search for.
func (t *Tab) hasRepo() bool { return t.project == nil || t.project.Repo != "" }

// settled reports whether every enabled source finished its first load.
func (t *Tab) settled() bool {
	return !slices.ContainsFunc(t.sources(), func(s loadState) bool { return !s.settled() })
}

// problems are one line per source that failed (or isn't available).
func (t *Tab) problems() []string {
	var out []string
	if t.agents.err != nil {
		out = append(out, "agents: "+t.agents.err.Error())
	}
	for _, s := range []*prSearch{&t.reviews, &t.mine} {
		if s.err != nil {
			out = append(out, "GitHub: "+s.err.Error())
			break // one line for both searches
		}
	}
	if err := t.issues.err; err != nil {
		msg := err.Error()
		if !strings.HasPrefix(msg, "plane:") {
			msg = "plane: " + msg
		}
		out = append(out, msg)
	}
	return out
}

// StatusDetail implements tabs.StatusDetailer: "3 need you".
func (t *Tab) StatusDetail() string {
	switch n := len(t.list); {
	case n == 0 && t.settled() && len(t.problems()) > 0:
		return "nothing listed · a source failed"
	case n == 0 && t.settled():
		return "all clear"
	case n == 1:
		return "1 needs you"
	case n > 1:
		return fmt.Sprintf("%d need you", n)
	}
	return ""
}

// Animating implements tabs.Animator: a running agent's flame flickers.
func (t *Tab) Animating() bool {
	s := t.anim.Settings()
	return slices.ContainsFunc(t.running(), func(r sessions.Row) bool {
		return anim.FlameAnimating(flameStatus(r.Status), s)
	})
}

// flameStatus is a session status as anim.Flame draws it.
func flameStatus(st store.Status) anim.Status {
	switch st {
	case store.StatusWorking:
		return anim.StatusWorking
	case store.StatusWaiting:
		return anim.StatusWaiting
	case store.StatusError:
		return anim.StatusError
	case store.StatusDead, store.StatusArchived:
		return anim.StatusDead
	}
	return anim.StatusIdle
}
