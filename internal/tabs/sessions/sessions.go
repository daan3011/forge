package sessions

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/browser"
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/packs"
	"github.com/daan3011/forge/internal/refresh"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/claude"
	"github.com/daan3011/forge/internal/sessions/live"
	"github.com/daan3011/forge/internal/sessions/worktree"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/templates"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
	"github.com/daan3011/forge/internal/usage"
)

// Title is the tab's title, which routes its targeted messages.
const Title = "Sessions"

// PreviewInterval is how often the selected session's pane is captured
// while the tab is visible.
const PreviewInterval = 2 * time.Second

const (
	listTimeout    = 10 * time.Second
	previewTimeout = 3 * time.Second
	actionTimeout  = 2 * time.Minute // worktree creation fetches from origin
)

// promptMisses is how many captures in a row must lack the permission
// prompt before a waiting session is moved back to working (one could be a
// redraw mid-frame).
const promptMisses = 2

// statusCol is the index of the Status column.
const statusCol = 1

// Column order puts what matters most first: narrow panes drop columns from
// the right. Name is flexible but never narrower than 16 cells. Status is
// "<flame> <status>".
var columns = []ui.Column{
	{Title: "Name", Flex: true, Width: 16},
	{Title: "Status", Width: 10},
	{Title: "Activity", Width: 8},
	{Title: "Project", Width: 10},
	{Title: "Branch", Width: 18},
	{Title: "Link", Width: 8},
}

// Tab is the Sessions tab: the session list, a detail pane with a live
// output preview, and the new / kill / delete flows.
type Tab struct {
	theme *ui.Theme
	keys  *ui.KeyMap
	cfg   *config.Config
	svc   agent.API
	live  <-chan live.Ping // forge hook pings; nil when not listening
	// browser opens linked issues and PRs (P1.9)
	browser browser.Opener
	// session templates (P1.7)
	tmplDir   string
	packsDir  string // context packs (P5.2); "" offers none
	providers *templates.Registry
	anim      *anim.Engine
	now       func() time.Time
	tick      func(time.Duration, func(time.Time) tea.Msg) tea.Cmd

	split   ui.SplitPane
	divider anim.Spring
	sliding bool
	table   ui.Table
	filter  ui.Filter

	project  *config.Project
	archived bool // showing the archive
	rows     map[string]agent.Row
	list     []agent.Row // rows in display order
	// both lists as last loaded, whichever is on screen (P1.10): the
	// palette searches the archive from either view
	activeRows, archivedRows []agent.Row
	// archived sessions whose cleanup question is still to be asked
	cleanupQueue []agent.Row
	updated      time.Time
	listGen      int // bumps on project/view change; stale lists are dropped
	fetching     bool
	dirty        bool            // a ping arrived during a refresh: refresh again after
	busy         int             // start/kill/delete actions in flight
	pending      string          // select this session once it shows up in the list
	resuming     map[string]bool // sessions with an attach/resume in flight

	syncing      bool // a PR link sync is running (P1.9)
	syncErrShown bool // a failed sync was reported once already

	visible     bool
	selected    string // name of the selected session, for change detection
	previewGen  int    // bumps on selection/visibility change; stale ticks are dropped
	preview     string
	previewFor  string
	previewErr  error
	previewBusy bool

	// capture-pane fallback for permission prompts (see checkPrompt)
	promptMiss int
	clearTried string // clearKey of the last waiting spell ClearWaiting declined

	dialog *dialog

	// transcript viewer (P1.6); nil when closed
	transcript     *transcriptView
	transcriptID   int // bumps per opened viewer
	transcriptPoll int // bumps on open/close/visibility; stale polls are dropped
	width, height  int

	// token usage (P5.4); usage is nil without a database
	usage         tabs.UsageSource
	usageTot      map[string]usage.Totals // by Claude session id
	usageErr      error
	usageScanAt   time.Time
	usageScanning bool
}

var (
	_ tabs.Tab             = (*Tab)(nil)
	_ tabs.InputCapturer   = (*Tab)(nil)
	_ tabs.Commander       = (*Tab)(nil)
	_ tabs.EntityProvider  = (*Tab)(nil)
	_ tabs.Updater         = (*Tab)(nil)
	_ tabs.Fetcher         = (*Tab)(nil)
	_ tabs.AutoRefresher   = (*Tab)(nil)
	_ tabs.Animator        = (*Tab)(nil)
	_ tabs.VisibilityAware = (*Tab)(nil)
	_ tabs.SessionGoer     = (*Tab)(nil)
	_ tabs.SessionStarter  = (*Tab)(nil)
	_ tabs.SourceRefresher = (*Tab)(nil)
)

// New returns the Sessions tab. Without d.Sessions it shows an empty list
// and explains why.
func New(d tabkit.Deps) *Tab {
	cfg := d.Config
	if cfg == nil {
		cfg = config.Default()
	}
	t := &Tab{
		theme:     d.Theme,
		keys:      d.Keys,
		cfg:       cfg,
		svc:       d.Sessions,
		live:      d.Live,
		browser:   d.Browser,
		tmplDir:   d.TemplatesDir,
		packsDir:  d.PacksDir,
		providers: d.Providers,
		anim:      d.Engine(),
		now:       time.Now,
		tick:      tea.Tick,
		split:     ui.NewSplitPane(d.Theme),
		table:     ui.NewTable(d.Theme, d.Keys, nil),
		filter:    ui.NewFilter(d.Theme, d.Keys),
		rows:      map[string]agent.Row{},

		resuming: map[string]bool{},
		usage:    d.Usage,
		usageTot: map[string]usage.Totals{},
	}
	cols := slices.Clone(columns)
	cols[statusCol].Render = t.renderStatus
	t.table = ui.NewTable(d.Theme, d.Keys, cols)
	t.table.SetEmptyView(func(w, h int) string { return anim.EmptyForge(w, h, d.Theme) })
	return t
}

// flameStatus maps a session status to its anim.Flame icon.
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
	return anim.StatusIdle // idle, starting
}

// flame is st's status icon at the current animation frame.
func (t *Tab) flame(st store.Status) string {
	return anim.Flame(flameStatus(st), t.anim.Frame(), t.anim.Settings(), t.theme)
}

// renderStatus draws a Status cell: the flame, then the status word in its
// colour on the row's background.
func (t *Tab) renderStatus(cell string, width int, row lipgloss.Style) string {
	st := store.Status(cell)
	word := t.theme.SessionStatus(cell).Inherit(row)
	if width < 3 {
		return row.Render(t.flame(st))
	}
	return row.Render(t.flame(st)) + row.Render(" ") + word.Render(ansi.Truncate(cell, width-2, "…"))
}

// --- messages ---------------------------------------------------------------

type listedMsg struct {
	gen     int
	rows    []agent.Row // active
	archive []agent.Row
	err     error
	at      time.Time
}

type previewTickMsg struct{ gen int }

// pingMsg is a `forge hook` ping from the live socket; ok is false once the
// socket is closed.
type pingMsg struct {
	ping live.Ping
	ok   bool
}

type clearedMsg struct {
	name    string
	changed bool
	err     error
}

type previewMsg struct {
	gen  int
	name string
	text string
	err  error
}

type templatesLoadedMsg struct {
	req       NewRequest
	templates []templates.Template
	packs     []packs.Pack
	errs      []error
}

type templateResolvedMsg struct {
	req     NewRequest
	tmpl    templates.Template
	vals    map[string]string
	missing []string
	errs    []error
}

type startedMsg struct {
	fix  bool // a "fix this" session (P2.8)
	opts agent.StartOpts
	res  agent.Started
	err  error
}

type killedMsg struct {
	session store.Session
	opts    agent.KillOpts
	res     agent.Killed
	err     error
}

type deletedMsg struct {
	name string
	err  error
}

func (listedMsg) TargetTab() string      { return Title }
func (previewTickMsg) TargetTab() string { return Title }
func (previewMsg) TargetTab() string     { return Title }
func (startedMsg) TargetTab() string     { return Title }
func (killedMsg) TargetTab() string      { return Title }
func (deletedMsg) TargetTab() string     { return Title }
func (pingMsg) TargetTab() string        { return Title }
func (clearedMsg) TargetTab() string     { return Title }

// --- tea plumbing -------------------------------------------------------------

// Init implements tabs.Tab.
func (t *Tab) Init() tea.Cmd { return tea.Batch(t.Refresh(), t.waitPing(), t.syncPRs()) }

// Update implements tabs.Tab.
func (t *Tab) Update(msg tea.Msg) (tabs.Tab, tea.Cmd) {
	switch msg := msg.(type) {
	case listedMsg:
		return t, t.listed(msg)
	case previewTickMsg:
		if msg.gen != t.previewGen {
			return t, nil
		}
		return t, t.capture()
	case previewMsg:
		return t, t.previewed(msg)
	case usageTotalsMsg:
		return t, t.usageLoaded(msg)
	case templatesLoadedMsg:
		return t, t.templatesLoaded(msg)
	case templateResolvedMsg:
		return t, t.templateResolved(msg)
	case fixReadyMsg:
		return t, t.fixReady(msg)
	case docsPickMsg:
		if len(msg.src.Packs) > 0 {
			t.busy--
			return t, t.packsPicked(msg)
		}
		return t, t.docsPicked(msg)
	case packsResolvedMsg:
		return t, t.packsResolved(msg)
	case packsForMsg:
		return t, t.packsFor(msg)
	case packSentMsg:
		return t, t.packSent(msg)
	case docsSentMsg:
		return t, t.docsSent(msg)
	case startedMsg:
		return t, t.started(msg)
	case killedMsg:
		return t, t.killed(msg)
	case deletedMsg:
		return t, t.deleted(msg)
	case archivedMsg:
		return t, t.archivedOne(msg)
	case restoredMsg:
		return t, t.restored(msg)
	case pingMsg:
		return t, t.pinged(msg)
	case syncedMsg:
		return t, t.synced(msg)
	case openedMsg:
		return t, t.opened(msg)
	case transcriptLoadedMsg:
		return t, t.transcriptLoaded(msg)
	case transcriptPollMsg:
		return t, t.transcriptPolled(msg)
	case transcriptRenderedMsg:
		return t, t.markdownRendered(msg)
	case clearedMsg:
		if msg.err != nil {
			return t, errCmd(fmt.Errorf("sessions: %w", msg.err))
		}
		if msg.changed {
			return t, t.liveRefresh()
		}
		return t, nil
	}
	if cmd, ok := t.updateTmux(msg); ok {
		return t, cmd
	}
	if cmd, ok := t.updateAttach(msg); ok {
		return t, cmd
	}
	if t.dialog != nil {
		return t, t.updateDialog(msg)
	}
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return t, nil
	}
	if t.transcript != nil {
		return t, t.updateTranscript(kp)
	}
	if t.filter.Active() {
		t.updateFilter(kp)
		return t, t.selectionChanged()
	}
	l, s := t.keys.List, t.keys.Sessions
	switch {
	case key.Matches(kp, s.Attach):
		return t, t.attachSelected()
	case key.Matches(kp, s.Resume):
		return t, t.resumeSelected()
	case key.Matches(kp, s.Transcript):
		return t, t.openTranscript()
	case key.Matches(kp, s.OpenPR):
		return t, t.openLinked(store.KindGitHubPR)
	case key.Matches(kp, s.OpenIssue):
		return t, t.openLinked(store.KindPlaneIssue)
	case key.Matches(kp, s.AttachPack):
		return t, t.attachPack()
	case key.Matches(kp, s.New):
		return t, t.openNew()
	case key.Matches(kp, s.Kill):
		if t.archived {
			return t, t.openCleanup()
		}
		return t, t.openKill()
	case key.Matches(kp, s.Archive):
		return t, t.archiveOrRestore()
	case key.Matches(kp, s.Delete):
		return t, t.openDelete()
	case key.Matches(kp, s.ArchiveView):
		return t, t.toggleArchive()
	case key.Matches(kp, l.Filter):
		t.startFilter()
	case key.Matches(kp, t.keys.Input.Cancel) && t.filter.Value() != "":
		t.clearFilter()
	case key.Matches(kp, l.GrowPane):
		t.resize(t.split.Grow)
	case key.Matches(kp, l.ShrinkPane):
		t.resize(t.split.Shrink)
	default:
		t.table.Update(kp)
	}
	return t, t.selectionChanged()
}

// --- list ---------------------------------------------------------------------

// Refresh implements tabs.Tab. A refresh while one runs does nothing.
func (t *Tab) Refresh() tea.Cmd {
	if t.svc == nil {
		return nil
	}
	if t.fetching {
		return t.scanUsage()
	}
	scan := t.scanUsage()
	t.fetching = true
	svc, gen, now := t.svc, t.listGen, t.now
	project := ""
	if t.project != nil {
		project = t.project.Name
	}
	list := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
		defer cancel()
		rows, err := svc.List(ctx, project, false)
		if err != nil {
			return listedMsg{gen: gen, err: err, at: now()}
		}
		archive, err := svc.List(ctx, project, true)
		return listedMsg{gen: gen, rows: rows, archive: archive, err: err, at: now()}
	}
	return tea.Batch(list, scan)
}

func (t *Tab) listed(msg listedMsg) tea.Cmd {
	if msg.gen != t.listGen {
		return nil // for a project or view we have since left
	}
	t.fetching = false
	if msg.err != nil {
		return errCmd(fmt.Errorf("sessions: %w", msg.err))
	}
	t.activeRows, t.archivedRows = msg.rows, msg.archive
	if t.archived {
		t.setRows(msg.archive)
	} else {
		t.setRows(msg.rows)
	}
	t.updated = msg.at
	if t.pending != "" {
		if _, ok := t.rows[t.pending]; ok {
			t.selectName(t.pending)
			t.pending = ""
		}
	}
	cmd := tea.Batch(t.selectionChanged(), t.nextCleanupAsk())
	if t.dirty {
		t.dirty = false
		cmd = tea.Batch(cmd, t.Refresh())
	}
	return cmd
}

// --- live updates ---------------------------------------------------------------

// waitPing waits for the next `forge hook` ping. It blocks in its own
// goroutine, never in Update; every pingMsg re-arms it.
func (t *Tab) waitPing() tea.Cmd {
	ch := t.live
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		p, ok := <-ch
		return pingMsg{ping: p, ok: ok}
	}
}

// pinged shows a hook's status at once, then reloads the list from the
// database (which has the full picture) without piling up refreshes.
func (t *Tab) pinged(msg pingMsg) tea.Cmd {
	if !msg.ok {
		return nil // socket closed: forge is exiting
	}
	p := msg.ping
	if r, ok := t.rows[p.Session]; ok && p.Status != "" && !t.archived {
		if p.Changed {
			r.Status, r.LastActivityAt = store.Status(p.Status), t.now()
		}
		for i := range t.list {
			if t.list[i].Name == r.Name {
				t.list[i] = r
			}
		}
		t.setRows(t.list)
	}
	pinged := func() tea.Msg { return tabs.SessionPingMsg{Ping: p} }
	return tea.Batch(t.waitPing(), t.liveRefresh(), pinged)
}

// liveRefresh reloads the list now, or right after the refresh in flight
// (which may have read the database before the change).
func (t *Tab) liveRefresh() tea.Cmd {
	if t.fetching {
		t.dirty = true
		return nil
	}
	return t.Refresh()
}

// sortRows orders rows as the store does: waiting sessions first, otherwise
// keeping the given order.
func sortRows(rows []agent.Row) []agent.Row {
	out := slices.Clone(rows)
	slices.SortStableFunc(out, func(a, b agent.Row) int {
		aw, bw := a.Status == store.StatusWaiting, b.Status == store.StatusWaiting
		switch {
		case aw && !bw:
			return -1
		case bw && !aw:
			return 1
		}
		return 0
	})
	return out
}

func (t *Tab) setRows(rows []agent.Row) {
	rows = sortRows(rows)
	t.list = rows
	t.rows = make(map[string]agent.Row, len(rows))
	cells := make([][]string, len(rows))
	now := t.now()
	for i, r := range rows {
		t.rows[r.Name] = r
		cells[i] = []string{r.Name, string(r.Status), Ago(now, r.LastActivityAt), r.Project, r.Branch, dash(r.Link)}
	}
	// Keep the cursor on the same session across reorders.
	keep := t.selected
	t.table.SetRows(cells)
	if keep != "" {
		t.selectName(keep)
	}
}

func (t *Tab) selectName(name string) {
	for i, row := range t.table.VisibleRows() {
		if len(row) > 0 && row[0] == name {
			t.table.SetCursor(i)
			return
		}
	}
}

// current is the selected session.
func (t *Tab) current() (agent.Row, bool) {
	row := t.table.SelectedRow()
	if len(row) == 0 {
		return agent.Row{}, false
	}
	r, ok := t.rows[row[0]]
	return r, ok
}

func (t *Tab) toggleArchive() tea.Cmd {
	t.archived = !t.archived
	t.resetList()
	// Both lists are loaded on every refresh: show the cached one at once.
	if t.archived {
		t.setRows(t.archivedRows)
	} else {
		t.setRows(t.activeRows)
	}
	return tea.Batch(t.Refresh(), t.selectionChanged())
}

// resetList forgets the rows of the previous project or view.
func (t *Tab) resetList() {
	t.listGen++
	t.fetching = false
	t.updated = time.Time{}
	t.selected = ""
	t.setRows(nil)
	t.clearPreview()
}

// --- preview ------------------------------------------------------------------

// SetVisible implements tabs.VisibilityAware: the preview only refreshes
// while the tab is on screen.
func (t *Tab) SetVisible(v bool) tea.Cmd {
	if v == t.visible {
		return nil
	}
	t.visible = v
	t.previewGen++
	t.previewBusy = false
	poll := t.transcriptVisible(v)
	if !v {
		return nil
	}
	return tea.Batch(t.capture(), poll)
}

// selectionChanged restarts the preview when another session is selected.
func (t *Tab) selectionChanged() tea.Cmd {
	r, ok := t.current()
	name := ""
	if ok {
		name = r.Name
	}
	if name == t.selected {
		return nil
	}
	t.selected = name
	t.previewGen++
	t.previewBusy = false
	t.promptMiss = 0
	if t.previewFor != name {
		t.clearPreview()
	}
	return tea.Batch(t.capture(), t.loadUsage())
}

func (t *Tab) clearPreview() {
	t.preview, t.previewFor, t.previewErr = "", "", nil
}

// isLive reports whether r's pane is worth polling.
func isLive(r agent.Row) bool {
	return r.TmuxWindow != "" && r.Status != store.StatusDead && r.Status != store.StatusArchived
}

// capture grabs the selected session's pane now, if the tab is visible and
// no capture is in flight. The next tick is scheduled when it lands.
func (t *Tab) capture() tea.Cmd {
	r, ok := t.current()
	if !ok || !t.visible || t.svc == nil || t.previewBusy || r.TmuxWindow == "" || r.Archived() {
		return nil
	}
	t.previewBusy = true
	svc, gen, lines := t.svc, t.previewGen, max(t.previewLines(), 1)
	x := r.Session
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
		defer cancel()
		text, err := svc.Preview(ctx, x, lines)
		return previewMsg{gen: gen, name: x.Name, text: text, err: err}
	}
}

func (t *Tab) previewed(msg previewMsg) tea.Cmd {
	if msg.gen != t.previewGen {
		return nil
	}
	t.previewBusy = false
	t.preview, t.previewFor, t.previewErr = msg.text, msg.name, msg.err
	r, ok := t.current()
	if !ok || r.Name != msg.name || !t.visible || !isLive(r) {
		return nil // dead panes don't change; stop polling
	}
	gen := t.previewGen
	next := t.tick(PreviewInterval, func(time.Time) tea.Msg { return previewTickMsg{gen: gen} })
	if msg.err != nil {
		return next
	}
	return tea.Batch(next, t.checkPrompt(r, msg.text))
}

// clearKey identifies one waiting spell of a session.
func clearKey(r agent.Row) string {
	return r.Name + "@" + r.StatusChangedAt.String()
}

// checkPrompt is the capture-pane fallback for permission prompts: Claude
// Code sends no hook when a prompt is answered, so a session would read
// "waiting" until its next tool call. When the selected waiting session's
// pane has shown no permission prompt for promptMisses captures in a row,
// ask the service to move it back to working (it checks the session really
// is waiting on a permission prompt).
func (t *Tab) checkPrompt(r agent.Row, pane string) tea.Cmd {
	if r.Status != store.StatusWaiting || clearKey(r) == t.clearTried || claude.PermissionPromptVisible(pane) {
		t.promptMiss = 0
		return nil
	}
	if t.promptMiss++; t.promptMiss < promptMisses {
		return nil
	}
	t.promptMiss = 0
	t.clearTried = clearKey(r)
	svc, x := t.svc, r.Session
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
		defer cancel()
		changed, err := svc.ClearWaiting(ctx, x)
		return clearedMsg{name: x.Name, changed: changed, err: err}
	}
}

// detailFields is the number of lines above the preview in the detail pane
// (fields, a blank line and the "output" rule).
const detailFields = 12

func (t *Tab) previewLines() int {
	_, h := t.split.RightSize()
	return h - detailFields
}

// --- new session ----------------------------------------------------------------

func (t *Tab) openNew() tea.Cmd {
	vals := agent.StartOpts{}
	if t.project != nil {
		vals.Project = t.project.Name
	}
	vals.Template = t.cfg.Sessions.DefaultTemplate
	return t.openNewWith(NewRequest{Opts: vals})
}

// openNewWith loads the templates (off the update loop), then opens the
// new-session form pre-filled from req.
func (t *Tab) openNewWith(req NewRequest) tea.Cmd {
	if t.svc == nil {
		return errCmd(errUnavailable)
	}
	dir, pdir := t.tmplDir, t.packsDir
	return func() tea.Msg {
		ts, errs := loadTemplates(dir)
		ps, perrs := LoadPacks(pdir)
		return templatesLoadedMsg{req: req, templates: ts, packs: ps, errs: append(errs, perrs...)}
	}
}

func (t *Tab) templatesLoaded(msg templatesLoadedMsg) tea.Cmd {
	if t.dialog != nil {
		return nil // something else opened meanwhile
	}
	var cmds []tea.Cmd
	if err := errors.Join(msg.errs...); err != nil {
		cmds = append(cmds, errCmd(err))
	}
	vals := msg.req.Opts
	if _, ok := templates.Find(msg.templates, vals.Template); vals.Template != "" && !ok {
		vals.Template = t.cfg.Sessions.DefaultTemplate // e.g. a source's review-pr was deleted
	}
	f, v, err := NewForm(FormInput{
		Theme: t.theme, Keys: t.keys, Projects: t.cfg.Projects, Values: vals,
		Templates: msg.templates, Packs: msg.packs,
		Taken: func(name string) bool { _, ok := t.rows[name]; return ok },
	})
	if err != nil {
		return tea.Batch(append(cmds, errCmd(err))...)
	}
	req := msg.req
	in := FormInput{Projects: t.cfg.Projects, Templates: msg.templates}
	open := t.openDialog("", f, func() tea.Cmd {
		req.Opts = *v
		return t.resolvePacks(req, msg.packs, func(req NewRequest) tea.Cmd {
			tmpl, ok := templates.Find(msg.templates, req.Opts.Template)
			if !ok {
				o := t.withRefLinks(req.Opts, req.Refs)
				return t.start(t.withPacks(o, req, templates.Template{}, o.Prompt))
			}
			return t.askRefs(req, tmpl)
		})
	})
	t.dialog.preview = func() string { return previewText(in, *v) }
	if len(msg.templates) > 0 {
		w, h := t.formSize() // narrower now that the preview takes half
		_, _ = f.Update(tea.WindowSizeMsg{Width: w, Height: h})
	}
	return tea.Batch(append(cmds, open)...)
}

// askRefs asks for the issue / PR the template needs, then resolves.
func (t *Tab) askRefs(req NewRequest, tmpl templates.Template) tea.Cmd {
	f, get := RefsForm(t.theme, t.keys, tmpl, req.Refs)
	if f == nil {
		return t.resolveTemplate(req, tmpl)
	}
	return t.openDialog("", f, func() tea.Cmd {
		req.Refs = get()
		return t.resolveTemplate(req, tmpl)
	})
}

// resolveTemplate asks the context providers for the template's variables
// in a command; templateResolved continues the flow.
func (t *Tab) resolveTemplate(req NewRequest, tmpl templates.Template) tea.Cmd {
	req = t.withDocsVar(req)
	t.busy++
	reg, projects := t.providers, t.cfg.Projects
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
		defer cancel()
		vals, missing, errs := templates.Resolve(ctx, reg, tmpl, TemplateInput(projects, req.Opts, req.Refs, req.Vars))
		return templateResolvedMsg{req: req, tmpl: tmpl, vals: vals, missing: missing, errs: errs}
	}
}

func (t *Tab) templateResolved(msg templateResolvedMsg) tea.Cmd {
	t.busy--
	var cmds []tea.Cmd
	for _, err := range msg.errs {
		cmds = append(cmds, info(err.Error()+"; fill it in below"))
	}
	f, get := VarsForm(t.theme, t.keys, msg.tmpl, msg.missing)
	if f == nil {
		return tea.Batch(append(cmds, t.confirmPrompt(msg.req, msg.tmpl, msg.vals))...)
	}
	open := t.openDialog("", f, func() tea.Cmd {
		vals := maps.Clone(msg.vals)
		maps.Copy(vals, get())
		return t.confirmPrompt(msg.req, msg.tmpl, vals)
	})
	return tea.Batch(append(cmds, open)...)
}

// confirmPrompt shows the rendered prompt, editable, then starts.
func (t *Tab) confirmPrompt(req NewRequest, tmpl templates.Template, vals map[string]string) tea.Cmd {
	o := t.withPacks(t.withRefLinks(req.Opts, req.Refs), req, tmpl, RenderPrompt(tmpl, vals))
	f := PromptForm(t.theme, t.keys, &o.Prompt)
	return t.openDialog("", f, func() tea.Cmd { return t.start(o) })
}

func (t *Tab) start(o agent.StartOpts) tea.Cmd { return t.startAs(o, false) }

// startAs starts o; fix marks a "fix this" session for the toast.
func (t *Tab) startAs(o agent.StartOpts, fix bool) tea.Cmd {
	t.busy++
	svc := t.svc
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		res, err := svc.Start(ctx, o)
		return startedMsg{fix: fix, opts: o, res: res, err: err}
	}
}

func (t *Tab) started(msg startedMsg) tea.Cmd {
	t.busy--
	name := msg.opts.Name
	var inUse *agent.BranchInUseError
	if errors.As(msg.err, &inUse) {
		return t.branchInUse(msg.opts, inUse)
	}
	if msg.err != nil {
		return errCmd(startError(name, msg.err))
	}
	label := "session "
	if msg.fix {
		label = "fix session "
	}
	cmds := []tea.Cmd{success(label + name + " started")}
	if msg.res.FetchErr != nil {
		cmds = append(cmds, info("couldn't fetch origin; branched from the local base"))
	}
	if t.archived {
		t.archived = false
		t.resetList()
	}
	t.pending = name
	return tea.Batch(append(cmds, t.forceRefresh())...)
}

func startError(name string, err error) error {
	var be *worktree.BranchExistsError
	switch {
	case errors.Is(err, agent.ErrNameTaken):
		return fmt.Errorf("a session called %s already exists (archived ones count too)", name)
	case errors.As(err, &be):
		return fmt.Errorf("branch %s already exists; pick another name or delete the branch", be.Branch)
	}
	return fmt.Errorf("start %s: %w", name, err)
}

// --- kill / delete ----------------------------------------------------------------

const (
	killWindow       = "kill"
	killRemove       = "remove"
	killRemoveBranch = "remove-branch"
	killCancel       = "cancel"
)

func (t *Tab) openKill() tea.Cmd {
	r, ok := t.current()
	if !ok || t.svc == nil {
		return nil
	}
	if r.Status == store.StatusArchived {
		return info(r.Name + " is already archived")
	}
	choice := killWindow
	opts := []huh.Option[string]{huh.NewOption("Kill session (keep worktree)", killWindow)}
	switch {
	case r.WorktreeExternal:
		// Not forge's worktree (e.g. the main checkout): never removed.
		opts = append(opts, huh.NewOption("Kill and archive (worktree left in place)", killRemove))
	default:
		opts = append(opts, huh.NewOption("Kill and remove worktree (keep branch, archive)", killRemove))
		if agent.CanDeleteBranch(r.Session) {
			// Only branches forge made; never a pre-existing or PR head branch.
			opts = append(opts, huh.NewOption("Kill, remove worktree and delete branch "+r.Branch+" (unpushed commits are lost)", killRemoveBranch))
		}
	}
	opts = append(opts, huh.NewOption("Cancel", killCancel))
	f := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Key("kill").
			Title("Kill session " + r.Name + "?").
			Description("the row is kept: a killed session can be resumed later").
			Options(opts...).Value(&choice),
	)).WithTheme(t.theme.Huh()).WithKeyMap(t.keys.HuhKeyMap()).WithShowHelp(false)
	x := r.Session
	return t.openDialog(r.Name, f, func() tea.Cmd {
		switch choice {
		case killWindow:
			return t.kill(x, agent.KillOpts{})
		case killRemove:
			return t.kill(x, agent.KillOpts{RemoveWorktree: true})
		case killRemoveBranch:
			return t.kill(x, agent.KillOpts{RemoveWorktree: true, DeleteBranch: true})
		}
		return nil
	})
}

func (t *Tab) kill(x store.Session, o agent.KillOpts) tea.Cmd {
	t.busy++
	svc := t.svc
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		res, err := svc.Kill(ctx, x, o)
		return killedMsg{session: x, opts: o, res: res, err: err}
	}
}

func (t *Tab) killed(msg killedMsg) tea.Cmd {
	t.busy--
	name := msg.session.Name
	refresh := t.forceRefresh()
	var dirty *worktree.DirtyError
	switch {
	case errors.As(msg.err, &dirty):
		force := false
		f := huh.NewForm(huh.NewGroup(
			huh.NewConfirm().Key("force").
				Title(fmt.Sprintf("%s has %d uncommitted change(s). Remove the worktree anyway?", name, len(dirty.Files))).
				Description("the session is already stopped; the changes will be lost").
				Affirmative("Remove").Negative("Keep").Value(&force),
		)).WithTheme(t.theme.Huh()).WithKeyMap(t.keys.HuhKeyMap()).WithShowHelp(false)
		x := msg.session
		x.Status = store.StatusDead
		open := t.openDialog(name, f, func() tea.Cmd {
			if !force {
				return info(name + " stopped; worktree kept")
			}
			o := msg.opts
			o.Force = true
			return t.kill(x, o)
		})
		return tea.Batch(refresh, open)
	case msg.err != nil:
		return tea.Batch(refresh, errCmd(fmt.Errorf("kill %s: %w", name, msg.err)))
	case msg.res.Archived:
		text := name + " archived — worktree removed, branch kept"
		switch {
		case msg.session.WorktreeExternal:
			text = name + " archived — worktree left in place"
		case msg.res.BranchDeleted:
			text = name + " archived — worktree and branch " + msg.session.Branch + " removed"
		case msg.res.BranchMerged:
			text += " (already merged)"
		}
		return tea.Batch(refresh, success(text))
	}
	return tea.Batch(refresh, success(name+" killed"))
}

func (t *Tab) openDelete() tea.Cmd {
	r, ok := t.current()
	if !ok || t.svc == nil {
		return nil
	}
	switch {
	case !t.archived || !r.Archived():
		return info("only archived sessions can be deleted — archive it (" + t.keys.Sessions.Archive.Help().Key + ") first")
	case r.Status != store.StatusArchived:
		return info(r.Name + " still has its worktree — remove it (" + t.keys.Sessions.Kill.Help().Key + ") before deleting")
	}
	yes := false
	f := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Key("delete").
			Title("Delete session " + r.Name + " for good?").
			Description("removes it from forge with its notes and links; the branch " + r.Branch + " stays").
			Affirmative("Delete").Negative("Keep").Value(&yes),
	)).WithTheme(t.theme.Huh()).WithKeyMap(t.keys.HuhKeyMap()).WithShowHelp(false)
	x := r.Session
	return t.openDialog(r.Name, f, func() tea.Cmd {
		if !yes {
			return nil
		}
		t.busy++
		svc := t.svc
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
			defer cancel()
			return deletedMsg{name: x.Name, err: svc.Delete(ctx, x)}
		}
	})
}

func (t *Tab) deleted(msg deletedMsg) tea.Cmd {
	t.busy--
	refresh := t.forceRefresh()
	if msg.err != nil {
		return tea.Batch(refresh, errCmd(fmt.Errorf("delete %s: %w", msg.name, msg.err)))
	}
	return tea.Batch(refresh, success("deleted "+msg.name))
}

// forceRefresh reloads the list even if a refresh is running (that one may
// predate the change).
func (t *Tab) forceRefresh() tea.Cmd {
	t.fetching = false
	t.listGen++
	return t.Refresh()
}

// --- dialogs ----------------------------------------------------------------------

// dialog is a Huh form shown in place of the tab.
type dialog struct {
	form    *huh.Form
	subject string // session the dialog is about, for the header
	done    func() tea.Cmd
	aborted func() // optional: the user abandoned the form (esc)
	// preview, if set, renders text shown beside the form (the new-session
	// template preview). It runs in View, so it may read the form's values.
	preview func() string
	// displaced, if set, is called when another dialog replaces this one
	// before it was answered (a queued archive cleanup question re-queues).
	displaced func()
}

func (t *Tab) openDialog(subject string, f *huh.Form, done func() tea.Cmd) tea.Cmd {
	return t.openDialogOr(subject, f, done, nil)
}

// openDialogOr is openDialog with a function to call when the form is
// abandoned instead of completed.
func (t *Tab) openDialogOr(subject string, f *huh.Form, done func() tea.Cmd, aborted func()) tea.Cmd {
	if t.dialog != nil && t.dialog.displaced != nil {
		t.dialog.displaced()
	}
	t.dialog = &dialog{form: f, subject: subject, done: done, aborted: aborted}
	init := f.Init()
	w, h := t.formSize()
	_, _ = f.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return init
}

func (t *Tab) dialogSize() (w, h int) {
	w, h = t.split.LeftSize()
	rw, _ := t.split.RightSize()
	if rw > 0 {
		w += rw + 2
	}
	return max(w-2, 10), max(h, 3)
}

func (t *Tab) updateDialog(msg tea.Msg) tea.Cmd {
	d := t.dialog
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		return nil // the app owns window size; SetSize resizes the form
	}
	_, cmd := d.form.Update(msg)
	switch d.form.State {
	case huh.StateCompleted:
		t.dialog = nil
		done := d.done() // may open the next dialog
		return tea.Batch(cmd, done, t.selectionChanged(), t.nextCleanupAsk())
	case huh.StateAborted:
		t.dialog = nil
		if d.aborted != nil {
			d.aborted()
		}
		return t.nextCleanupAsk()
	}
	return cmd
}

// --- filter & layout ---------------------------------------------------------------

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

func (t *Tab) resize(change func()) {
	from := t.split.ShownRatio()
	change()
	t.divider = anim.NewSpring(from, t.split.Ratio(), t.anim.Frame(), t.anim.Settings())
	t.sliding = true
	t.slide()
}

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

// Animating implements tabs.Animator: true while the divider springs or a
// listed agent's flame moves (working, or waiting with pulses on).
func (t *Tab) Animating() bool {
	if t.sliding && !t.divider.Settled(t.anim.Frame()) {
		return true
	}
	if t.dialog != nil {
		return false
	}
	s := t.anim.Settings()
	if t.transcript != nil {
		return !t.transcript.loaded && s.Motion() // the hammer while reading
	}
	for _, row := range t.table.VisibleRows() {
		if r, ok := t.rows[row[0]]; ok && anim.FlameAnimating(flameStatus(r.Status), s) {
			return true
		}
	}
	return false
}

// SetSize implements tabs.Tab.
func (t *Tab) SetSize(width, height int) {
	t.width, t.height = width, height
	t.split.SetSize(width, height)
	t.layoutTranscript()
	t.resizeTable()
	if t.dialog != nil {
		w, h := t.formSize()
		_, _ = t.dialog.form.Update(tea.WindowSizeMsg{Width: w, Height: h})
	}
}

func (t *Tab) resizeTable() {
	w, h := t.split.LeftSize()
	if t.filter.Visible() {
		h = max(h-1, 0)
	}
	t.table.SetSize(w, h)
}

// SetProject implements tabs.Tab.
func (t *Tab) SetProject(p *config.Project) {
	same := (p == nil && t.project == nil) || (p != nil && t.project != nil && p.Name == t.project.Name)
	t.project = p
	if !same {
		t.resetList()
	}
}

// --- view -----------------------------------------------------------------------------

// View implements tabs.Tab.
func (t *Tab) View() string {
	t.slide()
	if t.dialog != nil {
		return t.dialogView()
	}
	if t.transcript != nil {
		return t.transcriptView()
	}
	list := t.table.View()
	if t.filter.Visible() {
		w, _ := t.split.LeftSize()
		list = t.filter.View(w) + "\n" + list
	}
	return t.split.View(list, t.detail(), true)
}

// previewMinWidth is the dialog width from which a preview sits beside the
// form instead of below it.
const previewMinWidth = 90

// formSize is the size the dialog's form gets: all of the dialog, or the
// left half when a preview sits beside it.
func (t *Tab) formSize() (w, h int) {
	w, h = t.dialogSize()
	if t.dialog != nil && t.dialog.preview != nil && w >= previewMinWidth {
		w = w/2 - 1
	}
	return w, h
}

func (t *Tab) dialogView() string {
	w, h := t.dialogSize()
	body := t.dialog.form.View()
	if t.dialog.preview != nil {
		if text := t.dialog.preview(); text != "" {
			fw, _ := t.formSize()
			pv := t.theme.DetailLabel.Render("preview") + "\n" + text
			if fw < w {
				pw := w - fw - 2
				body = lipgloss.JoinHorizontal(lipgloss.Top,
					ui.Box(body, fw, h), "  ", ui.Box(wrapText(pv, pw), pw, h))
			} else {
				body += "\n\n" + wrapText(pv, w)
			}
		}
	}
	return t.theme.PaneFocused.Render(ui.Box(body, w, h))
}

// wrapText wraps s to width cells, keeping its line breaks.
func wrapText(s string, width int) string {
	return ansi.Wrap(s, max(width, 1), "")
}

func (t *Tab) detail() string {
	th := t.theme
	w, h := t.split.RightSize()
	if t.svc == nil {
		return strings.Join([]string{
			th.DetailTitle.Render(Title), "",
			th.Hint.Render(errUnavailable.Error()),
		}, "\n")
	}
	r, ok := t.current()
	if !ok {
		hint := "no sessions yet — press " + t.keys.Sessions.New.Help().Key + " to start one"
		if t.archived {
			hint = "no archived sessions"
		}
		return strings.Join([]string{th.DetailTitle.Render(t.heading()), "", th.Hint.Render(hint)}, "\n")
	}
	label := func(k, v string) string { return th.DetailLabel.Render(k+": ") + th.Body.Render(v) }
	target := "—"
	if r.TmuxSession != "" && r.TmuxWindow != "" {
		target = r.TmuxSession + ":" + r.TmuxWindow
	}
	lines := []string{
		th.DetailTitle.Render(r.Name) + "  " + t.flame(r.Status) + " " + th.SessionStatus(string(r.Status)).Render(string(r.Status)),
		label("project", r.Project),
		label("branch", r.Branch+" (from "+dash(r.BaseBranch)+")"),
		label("worktree", dash(r.Worktree)),
		label("tmux", target),
		label("claude", r.ClaudeSessionID),
	}
	lines = append(lines, t.linkLines(r)...)
	lines = append(lines,
		label("created", r.CreatedAt.Format("2006-01-02 15:04")),
		label("activity", Ago(t.now(), r.LastActivityAt)),
	)
	if t.usage != nil {
		lines = append(lines, label("usage", t.usageLine(r.ClaudeSessionID)))
	}
	if r.Archived() {
		lines = append(lines, label("archived", t.archiveLine(r)))
	}
	lines = append(lines, "")
	if r.Template != "" {
		lines[len(lines)-1] = label("template", r.Template) // in place of the blank line
	}
	if r.Archived() {
		// The summary and notes in place of the output preview.
		lines = append(lines, t.archiveBody(r, w)...)
		return strings.Join(lines, "\n")
	}
	lines = append(lines, th.PreviewRule.Render(rule("output", w)))
	n := h - len(lines)
	if n <= 0 {
		return strings.Join(lines, "\n")
	}
	var body string
	switch {
	case r.TmuxWindow == "":
		body = th.Hint.Render("no tmux window (archived)")
	case t.previewFor != r.Name:
		body = th.Hint.Render("capturing…")
	case t.previewErr != nil:
		body = th.Hint.Render("no output: " + t.previewErr.Error())
	case strings.TrimSpace(t.preview) == "":
		body = th.Hint.Render("(no output yet)")
	default:
		body = th.PreviewText.Render(lastLines(t.preview, n))
	}
	return strings.Join(append(lines, body), "\n")
}

func (t *Tab) heading() string {
	if t.archived {
		return fmt.Sprintf("%s · archived (%d)", Title, len(t.archivedRows))
	}
	return Title
}

// rule is "── title ─────" filling width cells.
func rule(title string, width int) string {
	head := "── " + title + " "
	if n := width - len([]rune(head)); n > 0 {
		return head + strings.Repeat("─", n)
	}
	return head
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// Title implements tabs.Tab.
func (t *Tab) Title() string { return Title }

// ShortHelp implements tabs.Tab.
func (t *Tab) ShortHelp() []key.Binding {
	switch {
	case t.dialog != nil:
		return t.keys.FormHelp()
	case t.transcript != nil:
		return t.transcriptHelp()
	case t.filter.Active():
		return []key.Binding{t.keys.Input.Cancel, t.keys.Input.Accept}
	case t.archived:
		s := t.keys.Sessions
		return []key.Binding{s.Transcript, s.OpenPR, t.keys.List.Filter, s.ArchiveView, s.Archive, s.Kill, s.Delete}
	}
	s := t.keys.Sessions
	return []key.Binding{s.Attach, s.Resume, s.Transcript, s.OpenPR, t.keys.List.Filter, s.ArchiveView, s.Archive, s.Kill, s.New}
}

// FullHelp implements tabs.Tab.
func (t *Tab) FullHelp() [][]key.Binding {
	l, in, s, tr := t.keys.List, t.keys.Input, t.keys.Sessions, t.keys.Transcript
	return [][]key.Binding{
		{s.Attach, s.Resume, s.Transcript, s.OpenPR, s.OpenIssue, s.AttachPack, s.New, s.Kill, s.ArchiveView, s.Archive, s.Delete},
		{tr.Up, tr.Down, tr.PageUp, tr.PageDown, tr.Top, tr.Bottom, tr.Toggle, tr.ToggleAll, tr.Follow, tr.Close},
		{l.Up, l.Down, l.Top, l.Bottom, l.PageDown, l.PageUp},
		{l.Filter, l.ShrinkPane, l.GrowPane},
		append([]key.Binding{in.Accept, in.Cancel}, t.keys.FormHelp()[1:]...),
	}
}

// Commands implements tabs.Commander.
func (t *Tab) Commands() []tabs.Command {
	l, s := t.keys.List, t.keys.Sessions
	do := func(f func()) func() tea.Cmd { return func() tea.Cmd { f(); return nil } }
	view, archive, kill := "Show archived sessions", "Archive session", "Kill session…"
	if t.archived {
		view, archive, kill = "Show active sessions", "Restore archived session", "Remove archived session's worktree and tmux window…"
	}
	killRun := t.openKill
	if t.archived {
		killRun = t.openCleanup
	}
	var cmds []tabs.Command
	if t.transcript != nil {
		cmds = t.transcriptCommands()
	}
	cmds = append(cmds, []tabs.Command{
		{ID: "session_attach", Title: "Attach session (resume if needed)", Key: s.Attach, Run: t.attachSelected},
		{ID: "session_resume", Title: "Resume session in the background", Key: s.Resume, Run: t.resumeSelected},
		{ID: "session_transcript", Title: "Open session transcript", Key: s.Transcript, Run: t.openTranscript},
		{ID: "session_open_pr", Title: "Open linked PR in browser", Key: s.OpenPR, Run: func() tea.Cmd { return t.openLinked(store.KindGitHubPR) }},
		{ID: "session_open_issue", Title: "Open linked issue in browser", Key: s.OpenIssue, Run: func() tea.Cmd { return t.openLinked(store.KindPlaneIssue) }},
		{ID: "session_attach_pack", Title: "Attach context pack to session…", Key: s.AttachPack, Run: t.attachPack},
		{ID: "session_new", Title: "New session…", Key: s.New, Run: t.openNew},
		{ID: "session_kill", Title: kill, Key: s.Kill, Run: killRun},
		{ID: "session_archive_view", Title: view, Key: s.ArchiveView, Run: t.toggleArchive},
		{ID: "session_archive", Title: archive, Key: s.Archive, Run: t.archiveOrRestore},
		{ID: "session_delete", Title: "Delete archived session…", Key: s.Delete, Run: t.openDelete},
		{ID: "tmux_restart", Title: "tmux: restart forge server…", Run: t.restartTmux},
		{ID: "tmux_config", Title: "tmux: edit forge tmux config", Run: t.editTmuxConfig},
		{ID: "tmux_config_reset", Title: "tmux: reset forge tmux config…", Run: t.resetTmuxConfig},
	}...)
	return append(cmds,
		tabs.Command{ID: "filter", Title: "Filter sessions", Key: l.Filter, Run: do(t.startFilter)},
		tabs.Command{ID: "grow_pane", Title: "Widen list", Key: l.GrowPane, Run: do(func() { t.resize(t.split.Grow) })},
		tabs.Command{ID: "shrink_pane", Title: "Narrow list", Key: l.ShrinkPane, Run: do(func() { t.resize(t.split.Shrink) })},
	)
}

// Entity kinds: active sessions (also listed by the "Go to session…"
// picker) and archived ones, which the palette finds by summary and notes
// too (P1.10).
const (
	EntitySession         = "Session"
	EntityArchivedSession = "Archived session"
)

// Entities implements tabs.EntityProvider. Both lists are offered whichever
// is on screen.
func (t *Tab) Entities() []tabs.Entity {
	active, archive := t.activeRows, t.archivedRows
	if t.archived {
		archive = t.list
	} else {
		active = t.list // has the latest pinged statuses
	}
	out := make([]tabs.Entity, 0, len(active)+len(archive))
	for _, r := range active {
		detail := []string{string(r.Status), r.Project, r.Branch}
		if r.Link != "" {
			detail = append(detail, r.Link)
		}
		out = append(out, tabs.Entity{
			Kind: EntitySession, ID: r.Name, Title: r.Name,
			Detail: strings.Join(detail, " · "),
			Search: strings.Join(append(slices.Clone(r.Links), r.Branch), " "),
		})
	}
	for _, r := range archive {
		detail := []string{"archived " + Ago(t.now(), r.ArchivedAt) + " ago", r.Project}
		if why := agent.ArchiveReason(r.LinkList); why != "" {
			detail = append(detail, why)
		}
		search := append(slices.Clone(r.Links), r.Branch, r.Summary, r.Notes)
		out = append(out, tabs.Entity{
			Kind: EntityArchivedSession, ID: r.Name, Title: r.Name,
			Detail: strings.Join(detail, " · "),
			Search: strings.Join(search, " "),
		})
	}
	return out
}

// Select implements tabs.EntityProvider. An archived session switches to
// the archived view (and an active one back), then selects it.
func (t *Tab) Select(id string) tea.Cmd {
	if t.filter.Visible() {
		t.clearFilter()
	}
	var cmd tea.Cmd
	if _, ok := t.rows[id]; !ok && (hasRow(t.activeRows, id) || hasRow(t.archivedRows, id)) {
		cmd = t.toggleArchive()
	}
	t.selectName(id)
	return tea.Batch(cmd, t.selectionChanged())
}

func hasRow(rows []agent.Row, name string) bool {
	return slices.ContainsFunc(rows, func(r agent.Row) bool { return r.Name == name })
}

// CapturingInput implements tabs.InputCapturer.
func (t *Tab) CapturingInput() bool {
	return t.dialog != nil || t.filter.Active() || t.transcript != nil
}

// LastUpdated implements tabs.Updater.
func (t *Tab) LastUpdated() time.Time { return t.updated }

// Fetching implements tabs.Fetcher: a list refresh or an action is running.
func (t *Tab) Fetching() bool { return t.fetching || t.busy > 0 }

// Sources implements tabs.AutoRefresher: the list refreshes on Sessions
// ticks, PR links sync on GitHub ticks (RefreshSource).
func (t *Tab) Sources() []refresh.Source {
	return []refresh.Source{refresh.Sessions, refresh.GitHub}
}

// RefreshWhenHidden implements tabs.AutoRefresher: statuses keep updating
// for the Home screen and notifications.
func (t *Tab) RefreshWhenHidden() bool { return true }

// --- helpers --------------------------------------------------------------------------

var errUnavailable = errors.New("sessions are unavailable: forge was started without its database")

func errCmd(err error) tea.Cmd { return func() tea.Msg { return ui.ErrMsg{Err: err} } }

func success(s string) tea.Cmd { return func() tea.Msg { return ui.SuccessMsg{Text: s} } }

func info(s string) tea.Cmd { return func() tea.Msg { return ui.InfoMsg{Text: s} } }

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// Ago renders the time since at compactly: "now", "42s", "5m", "3h", "2d".
func Ago(now, at time.Time) string {
	if at.IsZero() {
		return "—"
	}
	d := now.Sub(at)
	switch {
	case d < 5*time.Second:
		return "now"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
