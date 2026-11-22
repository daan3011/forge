package context

import (
	stdctx "context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/editor"
	"github.com/daan3011/forge/internal/memory"
	"github.com/daan3011/forge/internal/sessions/worktree"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
)

// Title is the tab's title, which also routes its messages.
const Title = "Context"

// worktreeTimeout bounds `git worktree list`.
const worktreeTimeout = 5 * time.Second

// worktreeLister lists a repo's git worktrees; *worktree.Exec satisfies it.
type worktreeLister interface {
	WorktreeList(ctx stdctx.Context, repo string) ([]worktree.Worktree, error)
}

// Tab is the Context tab (P5.1): every memory file Claude Code loads for
// the active project, with rough token counts, a rendered preview, $EDITOR
// editing and which worktrees inherit which files.
type Tab struct {
	theme *ui.Theme
	keys  *ui.KeyMap
	cfg   *config.Config
	now   func() time.Time
	// edit opens path in $EDITOR and reports editedMsg (tests replace it).
	edit    func(path string) tea.Cmd
	git     worktreeLister
	home    string
	managed string
	// packsDir holds the context packs (P5.2); "" hides the Packs view's
	// actions. lister lists a checkout's files for the picker.
	packsDir string
	lister   fileLister

	split  ui.SplitPane
	width  int
	height int

	project *config.Project
	gen     int // bumped by SetProject; older scans are dropped
	scanned bool
	updated time.Time

	files   []memory.File
	trees   []memory.Tree // [0] is the main checkout
	inherit []memory.InheritRow
	wtErr   error

	rows    []row
	cursor  int // index into rows; always a file row when there is one
	top     int
	selPath string

	focusDetail bool
	det         detailState
	matrix      bool // the right pane shows worktree inheritance

	view section
	pk   packState // the Packs view (P5.2)

	// caches measures and clears what forge stores (P5.3); nil when there
	// is no database.
	caches tabs.CacheControl
	cs     cacheState // the Cache view
}

var (
	_ tabs.Tab             = (*Tab)(nil)
	_ tabs.Commander       = (*Tab)(nil)
	_ tabs.Updater         = (*Tab)(nil)
	_ tabs.StatusDetailer  = (*Tab)(nil)
	_ tabs.VisibilityAware = (*Tab)(nil)
	_ tabs.InputCapturer   = (*Tab)(nil)
)

// New returns the Context tab.
func New(d tabkit.Deps) tabs.Tab { return newTab(d) }

func newTab(d tabkit.Deps) *Tab {
	cfg := d.Config
	if cfg == nil {
		cfg = config.Default()
	}
	home, _ := os.UserHomeDir()
	git := worktree.NewExec()
	t := &Tab{
		theme: d.Theme, keys: d.Keys, cfg: cfg, now: time.Now,
		git:      git,
		lister:   git,
		packsDir: d.PacksDir,
		caches:   d.Caches,
		home:     home,
		managed:  memory.DefaultManaged(),
		split:    ui.NewSplitPane(d.Theme),
		det:      newDetailState(),
	}
	t.split.SetRatio(0.4)
	t.edit = func(path string) tea.Cmd {
		c := editor.Exec{}.Cmd(stdctx.Background(), path)
		return tea.ExecProcess(c, func(err error) tea.Msg { return editedMsg{path: path, err: err} })
	}
	return t
}

// --- messages --------------------------------------------------------------------

type scannedMsg struct {
	gen   int
	files []memory.File
	trees []memory.Tree
	wtErr error
	at    time.Time
}

type editedMsg struct {
	path string
	err  error
}

// createdMsg reports a missing memory file created so it can be edited.
type createdMsg struct {
	path string
	err  error
}

// TargetTab implements tabs.TargetedMsg.
func (scannedMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (editedMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (createdMsg) TargetTab() string { return Title }

// --- loading ---------------------------------------------------------------------

// root is the checkout Claude Code would start in, "" without one.
func (t *Tab) root() string {
	if t.project == nil {
		return ""
	}
	return t.project.Path
}

// budget is context.token_budget; 0 means no warning.
func (t *Tab) budget() int { return t.cfg.Context.TokenBudget }

// overBudget reports whether the startup total passes the budget.
func (t *Tab) overBudget() bool {
	return t.budget() > 0 && memory.Total(t.files) > t.budget()
}

// Init implements tabs.Tab.
func (t *Tab) Init() tea.Cmd { return nil }

// Refresh implements tabs.Tab: resolves the memory files of the checkout
// and of each of its worktrees, and reloads the context packs (and, while
// it shows, the cache view).
func (t *Tab) Refresh() tea.Cmd {
	gen, root, home, managed, git, now := t.gen, t.root(), t.home, t.managed, t.git, t.now
	var cache tea.Cmd
	if t.view == viewCache {
		cache = t.loadCache()
	}
	return tea.Batch(func() tea.Msg {
		msg := scanned(root, home, managed, git)
		msg.gen, msg.at = gen, now()
		return msg
	}, t.loadPacks(), cache)
}

// scanned does the work of Refresh, off the update loop.
func scanned(root, home, managed string, git worktreeLister) scannedMsg {
	if root == "" {
		files := memory.Resolve(memory.Env{Home: home, Managed: managed, UserOnly: true})
		return scannedMsg{files: files}
	}
	env := memory.Env{Home: home, Managed: managed, Cwd: root, MainRoot: root}
	files := memory.Resolve(env)
	msg := scannedMsg{files: files, trees: []memory.Tree{{Root: root, Label: "checkout", Files: files}}}
	if git == nil {
		return msg
	}
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), worktreeTimeout)
	defer cancel()
	wts, err := git.WorktreeList(ctx, root)
	if err != nil {
		msg.wtErr = err
		return msg
	}
	for _, wt := range wts {
		if wt.Bare || wt.Prunable || samePath(wt.Path, root) {
			continue
		}
		e := env
		e.Cwd, e.SkipNested = wt.Path, true
		msg.trees = append(msg.trees, memory.Tree{Root: wt.Path, Label: wtLabel(wt), Files: memory.Resolve(e)})
	}
	return msg
}

func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

func wtLabel(wt worktree.Worktree) string {
	switch {
	case wt.Session != "":
		return wt.Session
	case wt.Branch != "":
		return wt.Branch
	case len(wt.Head) >= 7:
		return "detached " + wt.Head[:7]
	}
	return filepath.Base(wt.Path)
}

func (t *Tab) scannedIn(msg scannedMsg) {
	if msg.gen != t.gen {
		return
	}
	t.files, t.trees, t.wtErr = msg.files, msg.trees, msg.wtErr
	t.inherit = memory.Inheritance(t.trees)
	t.scanned, t.updated = true, msg.at
	t.rebuild()
}

// --- update ----------------------------------------------------------------------

// Update implements tabs.Tab.
func (t *Tab) Update(msg tea.Msg) (tabs.Tab, tea.Cmd) {
	cmd := t.update(msg)
	return t, tea.Batch(cmd, t.syncDetail())
}

func (t *Tab) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case packsLoadedMsg:
		t.packsLoaded(msg)
		return nil
	case filesListedMsg:
		return t.filesListed(msg)
	case packSavedMsg:
		return t.packSaved(msg)
	case cacheLoadedMsg:
		t.cacheLoaded(msg)
		return nil
	case cachePlanMsg:
		if t.pk.dialog != nil {
			t.cs.busy = false
			return nil // another dialog is open; the action is dropped
		}
		return t.planned(msg)
	case cacheDoneMsg:
		return t.applied(msg)
	}
	if t.pk.dialog != nil {
		return t.updatePackDialog(msg)
	}
	switch msg := msg.(type) {
	case scannedMsg:
		t.scannedIn(msg)
		return nil
	case renderedMsg:
		t.rendered(msg)
		return nil
	case editedMsg:
		if msg.err != nil {
			return tea.Batch(errCmd(msg.err), t.Refresh())
		}
		return t.Refresh()
	case createdMsg:
		if msg.err != nil {
			return errCmd(msg.err)
		}
		return t.edit(msg.path)
	}
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	ck, l := t.keys.Context, t.keys.List
	switch {
	case key.Matches(kp, ck.Packs):
		return t.togglePacks()
	case key.Matches(kp, ck.Cache):
		return t.toggleCache()
	case t.view == viewPacks:
		return t.packKey(kp)
	case t.view == viewCache:
		return t.cacheKey(kp)
	}
	switch {
	case key.Matches(kp, ck.Edit):
		return t.editSelected()
	case key.Matches(kp, ck.Worktrees):
		t.toggleMatrix()
	case key.Matches(kp, ck.DetailDown):
		t.scrollDetail(1)
	case key.Matches(kp, ck.DetailUp):
		t.scrollDetail(-1)
	case key.Matches(kp, l.GrowPane):
		t.split.Grow()
	case key.Matches(kp, l.ShrinkPane):
		t.split.Shrink()
	default:
		if t.focusDetail {
			t.detailKey(kp)
		} else {
			t.listKey(kp)
		}
	}
	return nil
}

func (t *Tab) listKey(kp tea.KeyPressMsg) {
	ck, l := t.keys.Context, t.keys.List
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
		t.move(max(t.listHeight()/2, 1))
	case key.Matches(kp, l.PageUp):
		t.move(-max(t.listHeight()/2, 1))
	case key.Matches(kp, ck.Open):
		t.focus()
	}
}

func (t *Tab) detailKey(kp tea.KeyPressMsg) {
	ck, l := t.keys.Context, t.keys.List
	half := max(t.detailBodyHeight()/2, 1)
	switch {
	case key.Matches(kp, ck.Back), key.Matches(kp, t.keys.Input.Cancel):
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

// focus moves the keys to the detail pane (showing the file, not the
// matrix).
func (t *Tab) focus() {
	if _, ok := t.selected(); ok {
		t.matrix = false
		t.focusDetail = true
	}
}

func (t *Tab) toggleMatrix() {
	t.matrix = !t.matrix
	if t.matrix {
		t.focusDetail = false
	}
}

func errCmd(err error) tea.Cmd {
	return func() tea.Msg { return ui.ErrMsg{Err: err} }
}

// --- interfaces ------------------------------------------------------------------

// SetSize implements tabs.Tab.
func (t *Tab) SetSize(width, height int) {
	t.width, t.height = width, height
	t.split.SetSize(width, max(height-sectionBarLines, 1))
	t.clampTop()
	t.setCacheCursor(t.cs.cursor)
	t.setProjectCursor(t.cs.pcursor)
	if t.pk.dialog != nil && t.pk.dialog.form != nil {
		w, h := t.dialogSize()
		_, _ = t.pk.dialog.form.Update(tea.WindowSizeMsg{Width: w, Height: h})
	}
}

// sectionBarLines is the Memory | Packs | Cache bar above the panes.
const sectionBarLines = 1

// CapturingInput implements tabs.InputCapturer: a pack or cache form, or
// the file picker, takes every key.
func (t *Tab) CapturingInput() bool { return t.pk.dialog != nil }

// SetProject implements tabs.Tab.
func (t *Tab) SetProject(p *config.Project) {
	t.project = p
	t.gen++
	t.scanned = false
	t.files, t.trees, t.inherit, t.wtErr = nil, nil, nil, nil
	t.rows, t.cursor, t.top, t.selPath = nil, 0, 0, ""
	t.focusDetail = false
	t.pk.loaded, t.pk.entries, t.pk.errs, t.pk.offset = false, nil, nil, 0
}

// SetVisible implements tabs.VisibilityAware: memory files change outside
// forge (Claude writes MEMORY.md), so showing the tab rescans; the cache
// view is measured again too.
func (t *Tab) SetVisible(visible bool) tea.Cmd {
	switch {
	case !visible:
		return nil
	case t.scanned:
		return t.Refresh()
	case t.view == viewCache:
		return t.loadCache()
	}
	return nil
}

// Title implements tabs.Tab.
func (t *Tab) Title() string { return Title }

// LastUpdated implements tabs.Updater.
func (t *Tab) LastUpdated() time.Time { return t.updated }

// StatusDetail implements tabs.StatusDetailer.
func (t *Tab) StatusDetail() string {
	if t.view == viewCache {
		return t.cacheStatus()
	}
	if t.view == viewPacks {
		e, ok := t.selectedPack()
		if !ok {
			return ""
		}
		return fmt.Sprintf("pack %s ≈%s tokens", e.pack.Name, ui.CompactCount(e.tokens))
	}
	if !t.scanned {
		return ""
	}
	s := fmt.Sprintf("≈%s tokens at start", ui.CompactCount(memory.Total(t.files)))
	if b := t.budget(); b > 0 {
		s = fmt.Sprintf("≈%s / %s tokens at start", ui.CompactCount(memory.Total(t.files)), ui.CompactCount(b))
	}
	if t.overBudget() {
		s += " · ⚠ over budget"
	}
	return s
}

// ShortHelp implements tabs.Tab.
func (t *Tab) ShortHelp() []key.Binding {
	ck := t.keys.Context
	switch {
	case t.pk.dialog != nil && t.pk.dialog.picker != nil:
		return t.pk.dialog.picker.Help()
	case t.pk.dialog != nil:
		return t.keys.FormHelp()
	case t.view == viewPacks:
		return []key.Binding{ck.Packs, ck.NewPack, ck.AddFiles, ck.Edit, ck.SendPack, ck.DeletePack}
	case t.view == viewCache && t.cs.drill != "":
		return []key.Binding{ck.Back, ck.ClearCache, ck.PruneCache, ck.Cache}
	case t.view == viewCache:
		return []key.Binding{ck.Cache, ck.Open, ck.ClearCache, ck.PruneCache, ck.RemoveOrphans, ck.Vacuum}
	}
	if t.focusDetail {
		back := key.NewBinding(key.WithKeys(), key.WithHelp(t.keys.Input.Cancel.Help().Key, "back to list"))
		return []key.Binding{t.keys.List.Down, t.keys.List.Up, back, ck.Edit}
	}
	return []key.Binding{ck.Packs, ck.Cache, ck.Worktrees, ck.Open, ck.Edit}
}

// FullHelp implements tabs.Tab.
func (t *Tab) FullHelp() [][]key.Binding {
	l, ck := t.keys.List, t.keys.Context
	return [][]key.Binding{
		{l.Up, l.Down, l.Top, l.Bottom, l.PageDown, l.PageUp},
		{ck.Open, ck.Back, t.keys.Input.Cancel, ck.DetailDown, ck.DetailUp, l.ShrinkPane, l.GrowPane},
		{ck.Edit, ck.Worktrees, ck.Packs, ck.Cache},
		{ck.NewPack, ck.AddFiles, ck.DeletePack, ck.SendPack},
		{ck.ClearCache, ck.PruneCache, ck.RemoveOrphans, ck.Vacuum},
	}
}

// Commands implements tabs.Commander.
func (t *Tab) Commands() []tabs.Command {
	l, ck := t.keys.List, t.keys.Context
	do := func(f func()) func() tea.Cmd { return func() tea.Cmd { f(); return nil } }
	return []tabs.Command{
		{ID: "context_edit", Title: "Context: edit memory file / pack in $EDITOR", Key: ck.Edit, Run: t.editCurrent},
		{ID: "context_worktrees", Title: "Context: toggle worktree inheritance", Key: ck.Worktrees, Run: do(t.toggleMatrix)},
		{ID: "context_packs", Title: "Context: switch memory files / packs", Key: ck.Packs, Run: t.togglePacks},
		{ID: "context_new_pack", Title: "Context: new context pack", Key: ck.NewPack, Run: t.inPacks(t.newPack)},
		{ID: "context_add_files", Title: "Context: pick the pack's files", Key: ck.AddFiles, Run: t.inPacks(t.addFiles)},
		{ID: "context_delete_pack", Title: "Context: delete context pack", Key: ck.DeletePack, Run: t.inPacks(t.deletePack)},
		{ID: "context_send_pack", Title: "Context: send pack to a session", Key: ck.SendPack, Run: t.inPacks(t.sendPack)},
		{ID: "context_cache", Title: "Context: switch memory files / cache", Key: ck.Cache, Run: t.toggleCache},
		{ID: "context_cache_clear", Title: "Context: clear the selected cache", Key: ck.ClearCache, Run: t.inCache(t.clearCache)},
		{ID: "context_cache_prune", Title: "Context: prune cache entries older than N days", Key: ck.PruneCache, Run: t.inCache(t.pruneCache)},
		{ID: "context_cache_orphans", Title: "Context: remove orphaned worktrees", Key: ck.RemoveOrphans, Run: t.inCache(t.removeOrphans)},
		{ID: "context_cache_vacuum", Title: "Context: vacuum the database", Key: ck.Vacuum, Run: t.inCache(t.vacuum)},
		{ID: "context_open", Title: "Context: focus the rendered file", Key: ck.Open, Run: do(t.focus)},
		{ID: "context_back", Title: "Context: back to the file list", Key: ck.Back, Run: do(func() { t.focusDetail = false })},
		{ID: "context_detail_down", Title: "Context: scroll file down", Key: ck.DetailDown, Run: do(func() { t.scrollDetail(1) })},
		{ID: "context_detail_up", Title: "Context: scroll file up", Key: ck.DetailUp, Run: do(func() { t.scrollDetail(-1) })},
		{ID: "grow_pane", Title: "Widen list", Key: l.GrowPane, Run: do(t.split.Grow)},
		{ID: "shrink_pane", Title: "Narrow list", Key: l.ShrinkPane, Run: do(t.split.Shrink)},
	}
}

// --- view ------------------------------------------------------------------------

// editCurrent edits the selected pack in the Packs view, else the
// selected memory file.
func (t *Tab) editCurrent() tea.Cmd {
	if t.view == viewPacks {
		return t.editPack()
	}
	return t.editSelected()
}

// inPacks wraps a Packs view action for the palette: it shows the view
// first (loading the packs if needed).
func (t *Tab) inPacks(f func() tea.Cmd) func() tea.Cmd {
	return func() tea.Cmd {
		var load tea.Cmd
		if t.view != viewPacks {
			load = t.togglePacks()
		}
		if !t.pk.loaded {
			return load // the packs aren't there yet; the view shows them
		}
		return tea.Batch(load, f())
	}
}

// View implements tabs.Tab.
func (t *Tab) View() string {
	if t.pk.dialog != nil {
		return t.packDialogView()
	}
	packCount := -1
	if t.pk.loaded {
		packCount = len(t.pk.entries)
	}
	bar := ui.SectionBar(t.theme, []string{"Memory", "Packs", "Cache"}, []int{-1, packCount, -1}, int(t.view), t.width)
	return bar + "\n" + t.panes()
}

func (t *Tab) panes() string {
	if t.view == viewCache {
		lw, lh := t.split.LeftSize()
		rw, rh := t.split.RightSize()
		return t.split.View(t.cacheListView(lw, lh), t.cacheDetailView(rw, rh), true)
	}
	if t.view == viewPacks {
		lw, lh := t.split.LeftSize()
		rw, rh := t.split.RightSize()
		return t.split.View(t.packsListView(lw, lh), t.packDetailView(rw, rh), true)
	}
	if t.split.Collapsed() && t.focusDetail {
		w, h := t.split.LeftSize()
		return t.split.View(t.detailView(w, h), "", true)
	}
	lw, lh := t.split.LeftSize()
	rw, rh := t.split.RightSize()
	right := t.detailView(rw, rh)
	if t.matrix {
		right = t.matrixView(rw, rh)
	}
	return t.split.View(t.listView(lw, lh), right, !t.focusDetail)
}
