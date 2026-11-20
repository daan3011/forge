package sessions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/refresh"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/worktree"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
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

// Column order puts what matters most first: narrow panes drop columns from
// the right. Name is flexible but never narrower than 16 cells.
var columns = []ui.Column{
	{Title: "Name", Flex: true, Width: 16},
	{Title: "Status", Width: 9},
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
	anim  *anim.Engine
	now   func() time.Time
	tick  func(time.Duration, func(time.Time) tea.Msg) tea.Cmd

	split   ui.SplitPane
	divider anim.Spring
	sliding bool
	table   ui.Table
	filter  ui.Filter

	project  *config.Project
	archived bool // showing the archive
	rows     map[string]agent.Row
	updated  time.Time
	listGen  int // bumps on project/view change; stale lists are dropped
	fetching bool
	busy     int    // start/kill/delete actions in flight
	pending  string // select this session once it shows up in the list

	visible     bool
	selected    string // name of the selected session, for change detection
	previewGen  int    // bumps on selection/visibility change; stale ticks are dropped
	preview     string
	previewFor  string
	previewErr  error
	previewBusy bool

	dialog *dialog
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
)

// New returns the Sessions tab. Without d.Sessions it shows an empty list
// and explains why.
func New(d tabkit.Deps) *Tab {
	cfg := d.Config
	if cfg == nil {
		cfg = config.Default()
	}
	t := &Tab{
		theme:  d.Theme,
		keys:   d.Keys,
		cfg:    cfg,
		svc:    d.Sessions,
		anim:   d.Engine(),
		now:    time.Now,
		tick:   tea.Tick,
		split:  ui.NewSplitPane(d.Theme),
		table:  ui.NewTable(d.Theme, d.Keys, columns),
		filter: ui.NewFilter(d.Theme, d.Keys),
		rows:   map[string]agent.Row{},
	}
	t.table.SetEmptyView(func(w, h int) string { return anim.EmptyForge(w, h, d.Theme) })
	return t
}

// --- messages ---------------------------------------------------------------

type listedMsg struct {
	gen  int
	rows []agent.Row
	err  error
	at   time.Time
}

type previewTickMsg struct{ gen int }

type previewMsg struct {
	gen  int
	name string
	text string
	err  error
}

type startedMsg struct {
	name string
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

// --- tea plumbing -------------------------------------------------------------

// Init implements tabs.Tab.
func (t *Tab) Init() tea.Cmd { return t.Refresh() }

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
	case startedMsg:
		return t, t.started(msg)
	case killedMsg:
		return t, t.killed(msg)
	case deletedMsg:
		return t, t.deleted(msg)
	}
	if cmd, ok := t.updateTmux(msg); ok {
		return t, cmd
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
		return t, t.selectionChanged()
	}
	l, s := t.keys.List, t.keys.Sessions
	switch {
	case key.Matches(kp, s.New):
		return t, t.openNew()
	case key.Matches(kp, s.Kill):
		return t, t.openKill()
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
	if t.svc == nil || t.fetching {
		return nil
	}
	t.fetching = true
	svc, gen, archived, now := t.svc, t.listGen, t.archived, t.now
	project := ""
	if t.project != nil {
		project = t.project.Name
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
		defer cancel()
		rows, err := svc.List(ctx, project, archived)
		return listedMsg{gen: gen, rows: rows, err: err, at: now()}
	}
}

func (t *Tab) listed(msg listedMsg) tea.Cmd {
	if msg.gen != t.listGen {
		return nil // for a project or view we have since left
	}
	t.fetching = false
	if msg.err != nil {
		return errCmd(fmt.Errorf("sessions: %w", msg.err))
	}
	t.setRows(msg.rows)
	t.updated = msg.at
	if t.pending != "" {
		if _, ok := t.rows[t.pending]; ok {
			t.selectName(t.pending)
			t.pending = ""
		}
	}
	return t.selectionChanged()
}

func (t *Tab) setRows(rows []agent.Row) {
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
	return t.Refresh()
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
	if !v {
		return nil
	}
	return t.capture()
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
	if t.previewFor != name {
		t.clearPreview()
	}
	return t.capture()
}

func (t *Tab) clearPreview() {
	t.preview, t.previewFor, t.previewErr = "", "", nil
}

// live reports whether r's pane is worth polling.
func live(r agent.Row) bool {
	return r.TmuxWindow != "" && r.Status != store.StatusDead && r.Status != store.StatusArchived
}

// capture grabs the selected session's pane now, if the tab is visible and
// no capture is in flight. The next tick is scheduled when it lands.
func (t *Tab) capture() tea.Cmd {
	r, ok := t.current()
	if !ok || !t.visible || t.svc == nil || t.previewBusy || r.TmuxWindow == "" {
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
	if !ok || r.Name != msg.name || !t.visible || !live(r) {
		return nil // dead panes don't change; stop polling
	}
	gen := t.previewGen
	return t.tick(PreviewInterval, func(time.Time) tea.Msg { return previewTickMsg{gen: gen} })
}

// detailFields is the number of lines above the preview in the detail pane
// (fields, a blank line and the "output" rule).
const detailFields = 11

func (t *Tab) previewLines() int {
	_, h := t.split.RightSize()
	return h - detailFields
}

// --- new session ----------------------------------------------------------------

func (t *Tab) openNew() tea.Cmd {
	if t.svc == nil {
		return errCmd(errUnavailable)
	}
	vals := agent.StartOpts{}
	if t.project != nil {
		vals.Project = t.project.Name
	}
	vals.Template = t.cfg.Sessions.DefaultTemplate
	f, v, err := NewForm(FormInput{
		Theme: t.theme, Keys: t.keys, Projects: t.cfg.Projects, Values: vals,
		Taken: func(name string) bool { _, ok := t.rows[name]; return ok },
	})
	if err != nil {
		return errCmd(err)
	}
	return t.openDialog("", f, func() tea.Cmd { return t.start(*v) })
}

func (t *Tab) start(o agent.StartOpts) tea.Cmd {
	t.busy++
	svc := t.svc
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		res, err := svc.Start(ctx, o)
		return startedMsg{name: o.Name, res: res, err: err}
	}
}

func (t *Tab) started(msg startedMsg) tea.Cmd {
	t.busy--
	if msg.err != nil {
		return errCmd(startError(msg.name, msg.err))
	}
	cmds := []tea.Cmd{success("session " + msg.name + " started")}
	if msg.res.FetchErr != nil {
		cmds = append(cmds, info("couldn't fetch origin; branched from the local base"))
	}
	if t.archived {
		t.archived = false
		t.resetList()
	}
	t.pending = msg.name
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
	killWindow = "kill"
	killRemove = "remove"
	killCancel = "cancel"
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
	f := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Key("kill").
			Title("Kill session "+r.Name+"?").
			Description("the row is kept: a killed session can be resumed later").
			Options(
				huh.NewOption("Kill session (keep worktree)", killWindow),
				huh.NewOption("Kill and remove worktree (keep branch, archive)", killRemove),
				huh.NewOption("Cancel", killCancel),
			).Value(&choice),
	)).WithTheme(t.theme.Huh()).WithKeyMap(t.keys.HuhKeyMap()).WithShowHelp(false)
	x := r.Session
	return t.openDialog(r.Name, f, func() tea.Cmd {
		switch choice {
		case killWindow:
			return t.kill(x, agent.KillOpts{})
		case killRemove:
			return t.kill(x, agent.KillOpts{RemoveWorktree: true})
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
			return t.kill(x, agent.KillOpts{RemoveWorktree: true, Force: true})
		})
		return tea.Batch(refresh, open)
	case msg.err != nil:
		return tea.Batch(refresh, errCmd(fmt.Errorf("kill %s: %w", name, msg.err)))
	case msg.res.Archived:
		text := name + " archived — worktree removed, branch kept"
		if msg.res.BranchMerged {
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
	if !t.archived || r.Status != store.StatusArchived {
		return info("only archived sessions can be deleted — kill it and remove its worktree first")
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
}

func (t *Tab) openDialog(subject string, f *huh.Form, done func() tea.Cmd) tea.Cmd {
	t.dialog = &dialog{form: f, subject: subject, done: done}
	init := f.Init()
	w, h := t.dialogSize()
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
		return tea.Batch(cmd, d.done(), t.selectionChanged())
	case huh.StateAborted:
		t.dialog = nil
		return nil
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

// Animating implements tabs.Animator: true while the divider springs.
func (t *Tab) Animating() bool {
	return t.sliding && !t.divider.Settled(t.anim.Frame())
}

// SetSize implements tabs.Tab.
func (t *Tab) SetSize(width, height int) {
	t.split.SetSize(width, height)
	t.resizeTable()
	if t.dialog != nil {
		w, h := t.dialogSize()
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
	list := t.table.View()
	if t.filter.Visible() {
		w, _ := t.split.LeftSize()
		list = t.filter.View(w) + "\n" + list
	}
	return t.split.View(list, t.detail(), true)
}

func (t *Tab) dialogView() string {
	w, h := t.dialogSize()
	return t.theme.PaneFocused.Render(ui.Box(t.dialog.form.View(), w, h))
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
		th.DetailTitle.Render(r.Name) + "  " + th.SessionStatus(string(r.Status)).Render(string(r.Status)),
		label("project", r.Project),
		label("branch", r.Branch+" (from "+dash(r.BaseBranch)+")"),
		label("worktree", dash(r.Worktree)),
		label("tmux", target),
		label("claude", r.ClaudeSessionID),
		label("link", dash(r.Link)),
		label("created", r.CreatedAt.Format("2006-01-02 15:04")),
		label("activity", Ago(t.now(), r.LastActivityAt)),
		"",
	}
	if r.Template != "" {
		lines[len(lines)-1] = label("template", r.Template) // in place of the blank line
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
		return Title + " · archived"
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
	case t.filter.Active():
		return []key.Binding{t.keys.Input.Cancel, t.keys.Input.Accept}
	case t.archived:
		return []key.Binding{t.keys.Global.Refresh, t.keys.List.Filter, t.keys.Sessions.ArchiveView, t.keys.Sessions.Delete}
	}
	s := t.keys.Sessions
	return []key.Binding{t.keys.List.Filter, s.ArchiveView, s.Kill, s.New}
}

// FullHelp implements tabs.Tab.
func (t *Tab) FullHelp() [][]key.Binding {
	l, in, s := t.keys.List, t.keys.Input, t.keys.Sessions
	return [][]key.Binding{
		{s.New, s.Kill, s.ArchiveView, s.Delete},
		{l.Up, l.Down, l.Top, l.Bottom, l.PageDown, l.PageUp},
		{l.Filter, l.ShrinkPane, l.GrowPane},
		append([]key.Binding{in.Accept, in.Cancel}, t.keys.FormHelp()[1:]...),
	}
}

// Commands implements tabs.Commander.
func (t *Tab) Commands() []tabs.Command {
	l, s := t.keys.List, t.keys.Sessions
	do := func(f func()) func() tea.Cmd { return func() tea.Cmd { f(); return nil } }
	view := "Show archived sessions"
	if t.archived {
		view = "Show active sessions"
	}
	cmds := []tabs.Command{
		{ID: "session_new", Title: "New session…", Key: s.New, Run: t.openNew},
		{ID: "session_kill", Title: "Kill session…", Key: s.Kill, Run: t.openKill},
		{ID: "session_archive_view", Title: view, Key: s.ArchiveView, Run: t.toggleArchive},
		{ID: "session_delete", Title: "Delete archived session…", Key: s.Delete, Run: t.openDelete},
		{ID: "tmux_restart", Title: "tmux: restart forge server…", Run: t.restartTmux},
		{ID: "tmux_config", Title: "tmux: edit forge tmux config", Run: t.editTmuxConfig},
		{ID: "tmux_config_reset", Title: "tmux: reset forge tmux config…", Run: t.resetTmuxConfig},
	}
	return append(cmds,
		tabs.Command{ID: "filter", Title: "Filter sessions", Key: l.Filter, Run: do(t.startFilter)},
		tabs.Command{ID: "grow_pane", Title: "Widen list", Key: l.GrowPane, Run: do(func() { t.resize(t.split.Grow) })},
		tabs.Command{ID: "shrink_pane", Title: "Narrow list", Key: l.ShrinkPane, Run: do(func() { t.resize(t.split.Shrink) })},
	)
}

// Entities implements tabs.EntityProvider.
func (t *Tab) Entities() []tabs.Entity {
	out := make([]tabs.Entity, 0, len(t.rows))
	for _, row := range t.table.Rows() {
		r := t.rows[row[0]]
		out = append(out, tabs.Entity{
			Kind: "Session", ID: r.Name, Title: r.Name,
			Detail: strings.Join([]string{string(r.Status), r.Project, r.Branch}, " · "),
		})
	}
	return out
}

// Select implements tabs.EntityProvider.
func (t *Tab) Select(id string) tea.Cmd {
	if t.filter.Visible() {
		t.clearFilter()
	}
	t.selectName(id)
	return t.selectionChanged()
}

// CapturingInput implements tabs.InputCapturer.
func (t *Tab) CapturingInput() bool { return t.dialog != nil || t.filter.Active() }

// LastUpdated implements tabs.Updater.
func (t *Tab) LastUpdated() time.Time { return t.updated }

// Fetching implements tabs.Fetcher: a list refresh or an action is running.
func (t *Tab) Fetching() bool { return t.fetching || t.busy > 0 }

// Sources implements tabs.AutoRefresher.
func (t *Tab) Sources() []refresh.Source { return []refresh.Source{refresh.Sessions} }

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
