package docs

import (
	"context"
	"fmt"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/daan3011/forge/internal/config"
	docfs "github.com/daan3011/forge/internal/docs"
	"github.com/daan3011/forge/internal/editor"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
)

// Title is the tab's title, which also routes its messages.
const Title = "Docs"

// root is one project's docs folder.
type root struct {
	name string // project name
	path string // absolute docs folder
	repo string // the project's checkout, whose CLAUDE.md points at decisions
	tree *docfs.Node
	err  error

	decisions []docfs.Decision // newest first (P4.4)
}

// Tab is the Docs tab (P4.1): a tree of each project's markdown docs with a
// Glamour preview, $EDITOR editing, new / rename / trash and live reload.
type Tab struct {
	theme *ui.Theme
	keys  *ui.KeyMap
	cfg   *config.Config
	now   func() time.Time
	// edit opens path in $EDITOR and reports editedMsg (tests replace it).
	edit    func(path string) tea.Cmd
	watcher docfs.Watcher

	split  ui.SplitPane
	width  int
	height int

	project *config.Project
	roots   []root
	gen     int // bumped by SetProject; older scans are dropped
	scanned bool
	updated time.Time

	rows     []row
	cursor   int
	top      int             // first visible row
	expanded map[string]bool // folder paths shown open
	known    map[string]bool // folder paths seen before (new ones start closed)
	selPath  string          // path of the selected row, kept across rescans
	want     string          // a path to select once a scan shows it

	focusDetail bool
	det         detailState

	dialog *huh.Form
	onDone func() tea.Cmd

	// Search (P4.2): the index is rebuilt with every scan.
	index     *docfs.Index
	search    ui.Filter
	hits      []docfs.Hit
	hitCursor int
	hitTop    int
	jump      string // heading to scroll the detail to once rendered
	jumpPath  string // the doc jump belongs to

	// marks are the docs marked for sending to an agent (P4.3), in the
	// order they were marked.
	marks []string

	// decisionsView shows the decision log instead of the tree (P4.4).
	decisionsView bool
}

var (
	_ tabs.Tab            = (*Tab)(nil)
	_ tabs.InputCapturer  = (*Tab)(nil)
	_ tabs.Commander      = (*Tab)(nil)
	_ tabs.Updater        = (*Tab)(nil)
	_ tabs.EntityProvider = (*Tab)(nil)
	_ tabs.StatusDetailer = (*Tab)(nil)
)

// New returns the Docs tab.
func New(d tabkit.Deps) tabs.Tab { return newTab(d) }

func newTab(d tabkit.Deps) *Tab {
	cfg := d.Config
	if cfg == nil {
		cfg = config.Default()
	}
	t := &Tab{
		theme: d.Theme, keys: d.Keys, cfg: cfg, now: time.Now,
		watcher:  d.DocsWatcher,
		split:    ui.NewSplitPane(d.Theme),
		expanded: map[string]bool{},
		known:    map[string]bool{},
		det:      newDetailState(),
		search:   ui.NewFilter(d.Theme, d.Keys),
	}
	t.split.SetRatio(0.32)
	t.edit = func(path string) tea.Cmd {
		c := editor.Exec{}.Cmd(context.Background(), path)
		return tea.ExecProcess(c, func(err error) tea.Msg { return editedMsg{path: path, err: err} })
	}
	t.setRoots()
	return t
}

// --- messages --------------------------------------------------------------------

type scannedMsg struct {
	gen   int
	trees []*docfs.Node
	errs  []error
	decs  [][]docfs.Decision
	index *docfs.Index
	at    time.Time
}

type watchMsg struct{ ok bool }

type editedMsg struct {
	path string
	err  error
}

// opDoneMsg reports a create / rename / trash. path is the doc to select
// (the new path; "" after a trash).
type opDoneMsg struct {
	path    string
	text    string // success toast
	openEd  bool   // open path in $EDITOR next
	err     error
	reveal  bool // expand path's folders
	trashed bool
}

// TargetTab implements tabs.TargetedMsg.
func (scannedMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (watchMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (editedMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (opDoneMsg) TargetTab() string { return Title }

// --- loading ---------------------------------------------------------------------

// setRoots picks the folders shown: the project's, or every project's.
func (t *Tab) setRoots() {
	var ps []config.Project
	if t.project != nil {
		ps = []config.Project{*t.project}
	} else {
		ps = t.cfg.Projects
	}
	t.roots = make([]root, len(ps))
	for i, p := range ps {
		t.roots[i] = root{name: p.Name, path: p.DocsPath(), repo: p.Path}
	}
}

// all reports whether the tree has a top level per project.
func (t *Tab) all() bool { return t.project == nil }

// Init implements tabs.Tab.
func (t *Tab) Init() tea.Cmd { return tea.Batch(t.Refresh(), t.waitWatch()) }

// Refresh implements tabs.Tab: rescans every root and points the watcher at
// them.
func (t *Tab) Refresh() tea.Cmd {
	gen, w := t.gen, t.watcher
	paths := make([]string, len(t.roots))
	names := make([]string, len(t.roots))
	for i, r := range t.roots {
		paths[i], names[i] = r.path, r.name
	}
	now := t.now
	return func() tea.Msg {
		msg := scannedMsg{
			gen: gen, trees: make([]*docfs.Node, len(paths)), errs: make([]error, len(paths)),
			decs: make([][]docfs.Decision, len(paths)),
		}
		if w != nil {
			_ = w.Set(paths) // best effort: without it, r still reloads
		}
		roots := make([]docfs.IndexRoot, len(paths))
		for i, p := range paths {
			msg.trees[i], msg.errs[i] = docfs.Scan(p)
			msg.decs[i], _ = docfs.ListDecisions(p) // unreadable: the tree shows the error
			roots[i] = docfs.IndexRoot{Name: names[i], Tree: msg.trees[i]}
		}
		msg.index = docfs.BuildIndex(roots)
		msg.at = now()
		return msg
	}
}

// waitWatch waits for the next change under the roots, in its own
// goroutine; every watchMsg re-arms it.
func (t *Tab) waitWatch() tea.Cmd {
	if t.watcher == nil {
		return nil
	}
	ch := t.watcher.Events()
	return func() tea.Msg {
		_, ok := <-ch
		return watchMsg{ok: ok}
	}
}

func (t *Tab) scannedIn(msg scannedMsg) {
	if msg.gen != t.gen || len(msg.trees) != len(t.roots) {
		return
	}
	for i := range t.roots {
		t.roots[i].tree, t.roots[i].err = msg.trees[i], msg.errs[i]
		if i < len(msg.decs) {
			t.roots[i].decisions = msg.decs[i]
		}
		if t.all() && !t.known[t.roots[i].path] {
			t.expanded[t.roots[i].path] = true // project levels start open
		}
		t.learn(msg.trees[i])
	}
	t.scanned, t.updated = true, msg.at
	t.index = msg.index
	t.pruneMarks()
	if t.want != "" {
		t.reveal(t.want)
	}
	t.rebuild()
	if t.searching() {
		keep := ""
		if h, ok := t.currentHit(); ok {
			keep = h.Doc.Path
		}
		t.runSearch(keep)
	}
}

// learn records n's folders so later scans know which are new.
func (t *Tab) learn(n *docfs.Node) {
	if n == nil {
		return
	}
	t.known[n.Path] = true
	for _, c := range n.Children {
		if c.IsDir {
			t.learn(c)
		}
	}
}

// --- update ----------------------------------------------------------------------

// Update implements tabs.Tab.
func (t *Tab) Update(msg tea.Msg) (tabs.Tab, tea.Cmd) {
	cmd := t.update(msg)
	sync := t.syncDetail()
	t.applyJump()
	return t, tea.Batch(cmd, sync)
}

func (t *Tab) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case scannedMsg:
		t.scannedIn(msg)
		return nil
	case watchMsg:
		if !msg.ok {
			return nil // watcher closed: forge is exiting
		}
		return tea.Batch(t.waitWatch(), t.Refresh())
	case renderedMsg:
		t.rendered(msg)
		return nil
	case editedMsg:
		if msg.err != nil {
			return tea.Batch(errCmd(msg.err), t.Refresh())
		}
		return t.Refresh()
	case opDoneMsg:
		return t.opDone(msg)
	case tabs.ReindexDocsMsg:
		return t.Refresh()
	case LogDecisionMsg:
		if t.dialog != nil {
			return nil // finish the open dialog first
		}
		t.clearSearch()
		return t.logDecision()
	}
	if t.dialog != nil {
		return t.updateDialog(msg)
	}
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	if t.searching() {
		if t.searchKey(kp) {
			return nil
		}
		cmd, _ := t.actionKey(kp)
		return cmd
	}
	if !t.focusDetail && key.Matches(kp, t.keys.List.Filter) {
		return t.startSearch()
	}
	if cmd, ok := t.actionKey(kp); ok {
		return cmd
	}
	if t.focusDetail {
		t.detailKey(kp)
		return nil
	}
	t.treeKey(kp)
	return nil
}

// actionKey handles the keys that work in both panes.
func (t *Tab) actionKey(kp tea.KeyPressMsg) (tea.Cmd, bool) {
	dk, l := t.keys.Docs, t.keys.List
	switch {
	case key.Matches(kp, dk.New):
		return t.newDoc(), true
	case key.Matches(kp, dk.Edit):
		return t.editSelected(), true
	case key.Matches(kp, dk.Rename):
		return t.renameSelected(), true
	case key.Matches(kp, dk.Delete):
		return t.deleteSelected(), true
	case key.Matches(kp, dk.Send):
		return t.sendToAgent(), true
	case key.Matches(kp, dk.Decision):
		return t.logDecision(), true
	case key.Matches(kp, dk.Supersede):
		return t.supersedeSelected(), true
	case key.Matches(kp, dk.Decisions):
		if t.searching() {
			t.clearSearch()
		}
		t.toggleDecisions()
	case key.Matches(kp, dk.Mark):
		t.toggleMark()
	case key.Matches(kp, dk.DetailDown):
		t.scrollDetail(1)
	case key.Matches(kp, dk.DetailUp):
		t.scrollDetail(-1)
	case key.Matches(kp, l.GrowPane):
		t.split.Grow()
	case key.Matches(kp, l.ShrinkPane):
		t.split.Shrink()
	default:
		return nil, false
	}
	return nil, true
}

func (t *Tab) treeKey(kp tea.KeyPressMsg) {
	dk, l := t.keys.Docs, t.keys.List
	switch {
	case key.Matches(kp, l.Up):
		t.move(-1)
	case key.Matches(kp, l.Down):
		t.move(1)
	case key.Matches(kp, l.Top):
		t.move(-len(t.rows))
	case key.Matches(kp, l.Bottom):
		t.move(len(t.rows))
	case key.Matches(kp, l.PageDown):
		t.move(max(t.treeHeight()/2, 1))
	case key.Matches(kp, l.PageUp):
		t.move(-max(t.treeHeight()/2, 1))
	case key.Matches(kp, dk.Open):
		t.open()
	case key.Matches(kp, dk.Back):
		t.back()
	case key.Matches(kp, t.keys.Input.Cancel):
		t.clearMarks()
	}
}

func (t *Tab) detailKey(kp tea.KeyPressMsg) {
	dk, l := t.keys.Docs, t.keys.List
	half := max(t.detailBodyHeight()/2, 1)
	switch {
	case key.Matches(kp, dk.Back), key.Matches(kp, t.keys.Input.Cancel):
		t.focusDetail = false
	case key.Matches(kp, l.Up):
		t.scrollDetail(-1)
	case key.Matches(kp, l.Down):
		t.scrollDetail(1)
	case key.Matches(kp, l.PageDown):
		t.scrollDetail(half)
	case key.Matches(kp, l.PageUp):
		t.scrollDetail(-half)
	case key.Matches(kp, l.Top):
		t.det.offset = 0
	case key.Matches(kp, l.Bottom):
		t.scrollDetail(1 << 30)
	}
}

// open expands or collapses a folder, or focuses the detail on a doc.
func (t *Tab) open() {
	r, ok := t.selected()
	if !ok {
		return
	}
	if r.node.IsDir {
		t.expanded[r.node.Path] = !t.expanded[r.node.Path]
		t.rebuild()
		return
	}
	t.focusDetail = true
}

// back collapses an open folder, or moves to the parent folder.
func (t *Tab) back() {
	r, ok := t.selected()
	if !ok {
		return
	}
	if r.node.IsDir && t.expanded[r.node.Path] {
		t.expanded[r.node.Path] = false
		t.rebuild()
		return
	}
	for i := t.cursor - 1; i >= 0; i-- {
		if t.rows[i].depth < r.depth {
			t.setCursor(i)
			return
		}
	}
}

// --- dialogs ---------------------------------------------------------------------

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

func errCmd(err error) tea.Cmd {
	return func() tea.Msg { return ui.ErrMsg{Err: err} }
}

func success(s string) tea.Cmd { return func() tea.Msg { return ui.SuccessMsg{Text: s} } }

// --- interfaces ------------------------------------------------------------------

// SetSize implements tabs.Tab.
func (t *Tab) SetSize(width, height int) {
	t.width, t.height = width, height
	t.split.SetSize(width, height)
	t.sizeDialog()
	t.clampTop()
}

// SetProject implements tabs.Tab.
func (t *Tab) SetProject(p *config.Project) {
	t.project = p
	t.gen++
	t.setRoots()
	t.scanned = false
	t.rows, t.cursor, t.top, t.selPath = nil, 0, 0, ""
	t.focusDetail = false
	t.index, t.jump, t.jumpPath = nil, "", ""
	t.clearSearch()
	t.clearMarks()
}

// Title implements tabs.Tab.
func (t *Tab) Title() string { return Title }

// CapturingInput implements tabs.InputCapturer.
func (t *Tab) CapturingInput() bool { return t.dialog != nil || t.search.Active() }

// LastUpdated implements tabs.Updater.
func (t *Tab) LastUpdated() time.Time { return t.updated }

// ShortHelp implements tabs.Tab.
func (t *Tab) ShortHelp() []key.Binding {
	dk := t.keys.Docs
	switch {
	case t.dialog != nil:
		return t.keys.FormHelp()
	case t.search.Active():
		return []key.Binding{t.keys.Input.Cancel, t.keys.Input.Accept}
	case t.searching():
		jump := key.NewBinding(key.WithKeys(), key.WithHelp(dk.Open.Help().Key, "jump to match"))
		clearSearch := key.NewBinding(key.WithKeys(), key.WithHelp(t.keys.Input.Cancel.Help().Key, "clear search"))
		return []key.Binding{t.keys.List.Down, t.keys.List.Up, clearSearch, jump}
	case t.focusDetail:
		back := key.NewBinding(key.WithKeys(), key.WithHelp(t.keys.Input.Cancel.Help().Key, "back to tree"))
		return []key.Binding{t.keys.List.Down, t.keys.List.Up, back, dk.Edit}
	}
	if len(t.marks) > 0 {
		return []key.Binding{dk.Mark, dk.Send, t.keys.Input.Cancel, dk.Open}
	}
	if t.decisionsView {
		tree := key.NewBinding(key.WithKeys(), key.WithHelp(dk.Decisions.Help().Key, "tree view"))
		return []key.Binding{dk.Decision, dk.Supersede, dk.Open, tree}
	}
	return []key.Binding{dk.New, t.keys.List.Filter, dk.Open, dk.Edit}
}

// FullHelp implements tabs.Tab.
func (t *Tab) FullHelp() [][]key.Binding {
	l, dk := t.keys.List, t.keys.Docs
	return [][]key.Binding{
		{l.Up, l.Down, l.Top, l.Bottom, l.PageDown, l.PageUp},
		{dk.Open, dk.Back, t.keys.Input.Cancel, dk.DetailDown, dk.DetailUp, l.ShrinkPane, l.GrowPane},
		{l.Filter, dk.New, dk.Edit, dk.Rename, dk.Delete},
		{dk.Mark, dk.Send},
		{dk.Decision, dk.Decisions, dk.Supersede},
	}
}

// Commands implements tabs.Commander.
func (t *Tab) Commands() []tabs.Command {
	l, dk := t.keys.List, t.keys.Docs
	do := func(f func()) func() tea.Cmd { return func() tea.Cmd { f(); return nil } }
	cmds := []tabs.Command{
		{ID: "filter", Title: "Docs: search titles and content…", Key: l.Filter, Run: t.startSearch},
		{ID: "docs_new", Title: "Docs: new doc…", Key: dk.New, Run: t.newDoc},
		{ID: "docs_edit", Title: "Docs: edit in $EDITOR", Key: dk.Edit, Run: t.editSelected},
		{ID: "docs_rename", Title: "Docs: rename / move…", Key: dk.Rename, Run: t.renameSelected},
		{ID: "docs_delete", Title: "Docs: delete (move to .trash)…", Key: dk.Delete, Run: t.deleteSelected},
		{ID: "docs_open", Title: "Docs: open doc / expand folder", Key: dk.Open, Run: do(t.open)},
		{ID: "docs_back", Title: "Docs: back to tree / collapse folder", Key: dk.Back, Run: do(func() {
			if t.focusDetail {
				t.focusDetail = false
				return
			}
			t.back()
		})},
		{ID: "docs_mark", Title: "Docs: mark / unmark for agent", Key: dk.Mark, Run: do(t.toggleMark)},
		{ID: "docs_send", Title: "Docs: send to agent (new or running session)…", Key: dk.Send, Run: t.sendToAgent},
		{ID: "docs_decision", Title: "Docs: log a decision (ADR)…", Key: dk.Decision, Run: t.logDecision},
		{ID: "docs_decisions", Title: "Docs: toggle decisions view", Key: dk.Decisions, Run: do(func() {
			t.clearSearch()
			t.toggleDecisions()
		})},
		{ID: "docs_supersede", Title: "Docs: mark decision superseded…", Key: dk.Supersede, Run: t.supersedeSelected},
		{ID: "docs_detail_down", Title: "Docs: scroll doc down", Key: dk.DetailDown, Run: do(func() { t.scrollDetail(1) })},
		{ID: "docs_detail_up", Title: "Docs: scroll doc up", Key: dk.DetailUp, Run: do(func() { t.scrollDetail(-1) })},
		{ID: "grow_pane", Title: "Widen list", Key: l.GrowPane, Run: do(t.split.Grow)},
		{ID: "shrink_pane", Title: "Narrow list", Key: l.ShrinkPane, Run: do(t.split.Shrink)},
	}
	if t.searching() {
		cmds = append(cmds,
			tabs.Command{ID: "docs_jump", Title: "Docs: jump to search match", Key: dk.Open, Run: do(t.jumpToHit)},
			tabs.Command{ID: "docs_clear_search", Title: "Docs: clear search", Key: t.keys.Input.Cancel, Run: do(t.clearSearch)},
		)
	}
	return cmds
}

// --- view ------------------------------------------------------------------------

// View implements tabs.Tab.
func (t *Tab) View() string {
	if t.dialog != nil {
		w, h := t.dialogSize()
		return t.theme.PaneFocused.Render(ui.Box(t.dialog.View(), w, h))
	}
	if t.split.Collapsed() && t.focusDetail {
		w, h := t.split.LeftSize()
		return t.split.View(t.detailView(w, h), "", true)
	}
	lw, lh := t.split.LeftSize()
	rw, rh := t.split.RightSize()
	left := t.treeView(lw, lh)
	if t.searching() {
		left = t.searchView(lw, lh)
	}
	return t.split.View(left, t.detailView(rw, rh), !t.focusDetail)
}

// docCount is "1 doc" / "3 docs".
func docCount(n int) string {
	if n == 1 {
		return "1 doc"
	}
	return fmt.Sprintf("%d docs", n)
}
