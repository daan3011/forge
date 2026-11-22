package artifacts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	artfs "github.com/daan3011/forge/internal/artifacts"
	"github.com/daan3011/forge/internal/browser"
	"github.com/daan3011/forge/internal/chafa"
	"github.com/daan3011/forge/internal/clipboard"
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/rsvg"
	"github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
)

// Title is the tab's title, which also routes its messages.
const Title = "Artifacts"

// EntityKind names artifacts in palette search.
const EntityKind = "Artifact"

// scanTimeout bounds one refresh (listing sessions, walking, the index).
const scanTimeout = 30 * time.Second

// sessionLister is the part of sessions.API the tab needs.
type sessionLister interface {
	List(ctx context.Context, project string, archived bool) ([]sessions.Row, error)
}

// Tab is the Artifacts tab (P4.5): files matching the watch globs in every
// project checkout and session worktree, grouped by project, newest first.
type Tab struct {
	theme *ui.Theme
	keys  *ui.KeyMap
	cfg   *config.Config
	spec  artfs.Spec
	now   func() time.Time

	sessions sessionLister
	index    tabs.ArtifactIndex
	watcher  artfs.Watcher
	files    browser.FileOpener
	clip     clipboard.Copier
	chafa    chafa.Renderer
	rsvg     rsvg.Rasterizer

	split  ui.SplitPane
	width  int
	height int

	project  *config.Project
	gen      int // bumped by SetProject; older scans are dropped
	scanned  bool
	scanning bool
	updated  time.Time
	scanErr  error

	items  []store.Artifact // newest first
	rows   []row
	cursor int
	top    int
	selKey string // path of the selected artifact, kept across rescans
	want   string // a path to select once a scan shows it

	filt    filters
	allTags []string // every tag in use, for completion and the filter form
	dialog  *huh.Form
	onDone  func() tea.Cmd

	pv previewState // inline preview (P4.7), see preview.go
}

var (
	_ tabs.Tab            = (*Tab)(nil)
	_ tabs.Commander      = (*Tab)(nil)
	_ tabs.Updater        = (*Tab)(nil)
	_ tabs.Fetcher        = (*Tab)(nil)
	_ tabs.EntityProvider = (*Tab)(nil)
	_ tabs.StatusDetailer = (*Tab)(nil)
	_ tabs.InputCapturer  = (*Tab)(nil)
)

// New returns the Artifacts tab.
func New(d tabkit.Deps) tabs.Tab { return newTab(d) }

func newTab(d tabkit.Deps) *Tab {
	cfg := d.Config
	if cfg == nil {
		cfg = config.Default()
	}
	t := &Tab{
		theme: d.Theme, keys: d.Keys, cfg: cfg, now: time.Now,
		spec:    artfs.NewSpec(cfg.Artifacts.Watch),
		index:   d.Artifacts,
		watcher: d.ArtifactWatcher,
		files:   d.Files,
		clip:    d.Clipboard,
		chafa:   d.Chafa,
		rsvg:    d.Rsvg,
		split:   ui.NewSplitPane(d.Theme),
	}
	if d.Sessions != nil {
		t.sessions = d.Sessions
	}
	t.split.SetRatio(0.55)
	return t
}

// --- messages --------------------------------------------------------------------

// cachedMsg is the index as the last run left it, shown until the first
// scan lands.
type cachedMsg struct {
	gen   int
	items []store.Artifact
}

type scannedMsg struct {
	gen   int
	items []store.Artifact
	tags  []string // every tag in use
	err   error
	at    time.Time
}

type watchMsg struct{ ok bool }

// keptMsg reports a Keep.
type keptMsg struct {
	dest  string
	wrote bool
	err   error
}

// clipboardFallbackMsg asks the terminal to copy text (OSC 52) when no
// copy tool is installed.
type clipboardFallbackMsg struct{ text string }

// TargetTab implements tabs.TargetedMsg.
func (cachedMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (scannedMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (watchMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (keptMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (clipboardFallbackMsg) TargetTab() string { return Title }

// --- loading ---------------------------------------------------------------------

// projects are the projects shown: the active one, or all.
func (t *Tab) projects() []config.Project {
	if t.project != nil {
		return []config.Project{*t.project}
	}
	return t.cfg.Projects
}

// projectName filters the index; "" is every project.
func (t *Tab) projectName() string {
	if t.project != nil {
		return t.project.Name
	}
	return ""
}

func (t *Tab) all() bool { return t.project == nil }

// Init implements tabs.Tab: the remembered index first, then a scan, and
// the watcher loop.
func (t *Tab) Init() tea.Cmd {
	return tea.Batch(t.loadCached(), t.Refresh(), t.waitWatch())
}

func (t *Tab) loadCached() tea.Cmd {
	if t.index == nil {
		return nil
	}
	idx, gen, project := t.index, t.gen, t.projectName()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		items, err := idx.List(ctx, project)
		if err != nil {
			return nil // the scan shows what's there
		}
		return cachedMsg{gen: gen, items: items}
	}
}

// Refresh implements tabs.Tab: it lists the sessions, points the watcher
// at every checkout and worktree, scans them, records the result in the
// index and reads it back.
func (t *Tab) Refresh() tea.Cmd {
	t.scanning = true
	gen, projects, project := t.gen, t.projects(), t.projectName()
	spec, lister, idx, w, now := t.spec, t.sessions, t.index, t.watcher, t.now
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
		defer cancel()
		var errs []error
		var sess []store.Session
		byID := map[int64]store.Session{}
		if lister != nil {
			for _, archived := range []bool{false, true} {
				rows, err := lister.List(ctx, project, archived)
				if err != nil {
					errs = append(errs, fmt.Errorf("list sessions: %w", err))
				}
				for _, r := range rows {
					sess = append(sess, r.Session)
					byID[r.ID] = r.Session
				}
			}
		}
		// Pinned copies sync before the scan so it sees them fresh: only
		// copies in the shown checkouts, whose sessions were all listed,
		// and only when listing worked (else every copy would look
		// orphaned).
		if idx != nil && lister != nil && len(errs) == 0 {
			if err := syncLinks(ctx, idx, byID, checkouts(projects), now()); err != nil {
				errs = append(errs, err)
			}
		}
		targets := artfs.Targets(projects, sess, spec)
		if w != nil {
			_ = w.SetTargets(targets) // best effort: refresh still rescans
		}
		found, err := artfs.Scan(targets)
		if err != nil {
			errs = append(errs, err)
		}
		items := toStore(found)
		var tags []string
		if idx != nil {
			if err := idx.Sync(ctx, artfs.Roots(targets), items); err != nil {
				errs = append(errs, err)
			} else if listed, err := idx.List(ctx, project); err != nil {
				errs = append(errs, err)
			} else {
				items = pruneMissing(ctx, idx, listed)
			}
			if tags, err = idx.AllTags(ctx); err != nil {
				errs = append(errs, err)
			}
		}
		return scannedMsg{gen: gen, items: items, tags: tags, err: errors.Join(errs...), at: now()}
	}
}

// checkouts are the main checkouts of projects.
func checkouts(projects []config.Project) []string {
	var out []string
	for _, p := range projects {
		if p.Path != "" {
			out = append(out, filepath.Clean(p.Path))
		}
	}
	return out
}

func toStore(found []artfs.Artifact) []store.Artifact {
	out := make([]store.Artifact, len(found))
	for i, a := range found {
		out[i] = store.Artifact{
			Path: a.Path, Project: a.Project, SessionID: a.SessionID, SessionName: a.SessionName,
			Type: a.Type, Size: a.Size, CreatedAt: a.ModTime, ModifiedAt: a.ModTime,
		}
	}
	return out
}

// pruneMissing drops (and forgets) indexed files that are gone: ones in a
// worktree that was removed, or under a glob no longer configured.
func pruneMissing(ctx context.Context, idx tabs.ArtifactIndex, items []store.Artifact) []store.Artifact {
	kept := items[:0]
	var gone []string
	for _, a := range items {
		if _, err := os.Stat(a.Path); errors.Is(err, os.ErrNotExist) {
			gone = append(gone, a.Path)
			continue
		}
		kept = append(kept, a)
	}
	_ = idx.Delete(ctx, gone) // best effort: they're pruned again next time
	return kept
}

// waitWatch waits for the next artifact change; every watchMsg re-arms it.
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

// --- update ----------------------------------------------------------------------

// Update implements tabs.Tab.
func (t *Tab) Update(msg tea.Msg) (tabs.Tab, tea.Cmd) {
	cmd := t.update(msg)
	return t, tea.Batch(cmd, t.syncPreview())
}

func (t *Tab) update(msg tea.Msg) tea.Cmd {
	if cmd, ok := t.updatePreview(msg); ok {
		return cmd
	}
	switch msg := msg.(type) {
	case cachedMsg:
		if msg.gen == t.gen && !t.scanned {
			t.setItems(msg.items)
		}
		return nil
	case scannedMsg:
		if msg.gen != t.gen {
			return nil
		}
		t.scanning, t.scanned, t.updated, t.scanErr = false, true, msg.at, msg.err
		t.allTags = msg.tags
		t.setItems(msg.items)
		return nil
	case watchMsg:
		if !msg.ok {
			return nil // watcher closed: forge is exiting
		}
		return tea.Batch(t.waitWatch(), t.Refresh())
	case keptMsg:
		return t.kept(msg)
	case metaDoneMsg:
		return t.metaDone(msg)
	case clipboardFallbackMsg:
		return tea.Batch(tea.SetClipboard(msg.text), info("copied "+msg.text))
	}
	if t.dialog != nil {
		return t.updateDialog(msg)
	}
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	ak, l := t.keys.Artifacts, t.keys.List
	switch {
	case key.Matches(kp, ak.Open):
		return t.openSelected()
	case key.Matches(kp, ak.CopyPath):
		return t.copyPath()
	case key.Matches(kp, ak.Keep):
		return t.keepSelected()
	case key.Matches(kp, ak.Pin):
		return t.togglePin()
	case key.Matches(kp, ak.Tags):
		return t.editTags()
	case key.Matches(kp, ak.Rename):
		return t.renameSelected()
	case key.Matches(kp, ak.Filters):
		return t.openFilters()
	case key.Matches(kp, ak.PinnedOnly):
		t.togglePinnedOnly()
	case key.Matches(kp, ak.ClearFilters):
		t.clearFilters()
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
	case key.Matches(kp, l.GrowPane):
		t.split.Grow()
		t.clampTop()
	case key.Matches(kp, l.ShrinkPane):
		t.split.Shrink()
		t.clampTop()
	}
	return nil
}

func info(s string) tea.Cmd    { return func() tea.Msg { return ui.InfoMsg{Text: s} } }
func success(s string) tea.Cmd { return func() tea.Msg { return ui.SuccessMsg{Text: s} } }
func errCmd(err error) tea.Cmd { return func() tea.Msg { return ui.ErrMsg{Err: err} } }

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
	t.scanned, t.scanning, t.scanErr = false, false, nil
	t.items, t.rows, t.cursor, t.top, t.selKey = nil, nil, 0, 0, ""
	t.dialog, t.onDone = nil, nil
}

// Title implements tabs.Tab.
func (t *Tab) Title() string { return Title }

// LastUpdated implements tabs.Updater.
func (t *Tab) LastUpdated() time.Time { return t.updated }

// Fetching implements tabs.Fetcher.
func (t *Tab) Fetching() bool { return t.scanning }

// StatusDetail implements tabs.StatusDetailer.
func (t *Tab) StatusDetail() string {
	if !t.scanned && len(t.items) == 0 {
		return ""
	}
	s := count(len(t.items), "artifact")
	if t.filt.any() {
		s = fmt.Sprintf("%d of %s · %s", t.shown(), s, t.filterLabel())
	}
	if t.scanErr != nil {
		s += " · scan: " + t.scanErr.Error()
	}
	return s
}

// ShortHelp implements tabs.Tab.
func (t *Tab) ShortHelp() []key.Binding {
	ak := t.keys.Artifacts
	return []key.Binding{t.keys.List.Down, ak.Pin, ak.Tags, ak.Filters, ak.Open}
}

// FullHelp implements tabs.Tab.
func (t *Tab) FullHelp() [][]key.Binding {
	l, ak := t.keys.List, t.keys.Artifacts
	return [][]key.Binding{
		{l.Up, l.Down, l.Top, l.Bottom, l.PageDown, l.PageUp},
		{ak.Open, ak.CopyPath, ak.Keep, l.ShrinkPane, l.GrowPane},
		{ak.Pin, ak.Tags, ak.Rename, ak.Filters, ak.PinnedOnly, ak.ClearFilters},
	}
}

// Commands implements tabs.Commander.
func (t *Tab) Commands() []tabs.Command {
	l, ak := t.keys.List, t.keys.Artifacts
	do := func(f func()) func() tea.Cmd { return func() tea.Cmd { f(); return nil } }
	return []tabs.Command{
		{ID: "artifacts_open", Title: "Artifacts: open in default app / browser", Key: ak.Open, Run: t.openSelected},
		{ID: "artifacts_copy_path", Title: "Artifacts: copy path", Key: ak.CopyPath, Run: t.copyPath},
		{ID: "artifacts_keep", Title: "Artifacts: keep a copy in .forge/artifacts", Key: ak.Keep, Run: t.keepSelected},
		{ID: "artifacts_pin", Title: "Artifacts: pin / unpin", Key: ak.Pin, Run: t.togglePin},
		{ID: "artifacts_tags", Title: "Artifacts: edit tags", Key: ak.Tags, Run: t.editTags},
		{ID: "artifacts_rename", Title: "Artifacts: rename (display title)", Key: ak.Rename, Run: t.renameSelected},
		{ID: "artifacts_filters", Title: "Artifacts: filter by tag / pinned", Key: ak.Filters, Run: t.openFilters},
		{ID: "artifacts_pinned_only", Title: "Artifacts: toggle pinned only", Key: ak.PinnedOnly, Run: do(t.togglePinnedOnly)},
		{ID: "artifacts_clear_filters", Title: "Artifacts: clear filters", Key: ak.ClearFilters, Run: do(t.clearFilters)},
		{ID: "artifacts_detach", Title: "Artifacts: detach from session (stop syncing the pinned copy)", Run: t.detachSelected},
		{ID: "artifacts_redraw_preview", Title: "Artifacts: redraw preview", Run: t.redrawPreview},
		{ID: "grow_pane", Title: "Widen list", Key: l.GrowPane, Run: do(t.split.Grow)},
		{ID: "shrink_pane", Title: "Narrow list", Key: l.ShrinkPane, Run: do(t.split.Shrink)},
	}
}

// Entities implements tabs.EntityProvider: every artifact shown.
func (t *Tab) Entities() []tabs.Entity {
	out := make([]tabs.Entity, 0, len(t.items))
	for _, a := range t.items {
		detail := t.source(a)
		if a.Project != "" {
			detail = a.Project + " · " + detail
		}
		search := a.Path + " " + a.Type
		if len(a.Tags) > 0 {
			search += " " + hashTags(a.Tags)
		}
		out = append(out, tabs.Entity{
			Kind: EntityKind, ID: a.Path, Title: displayName(a), Detail: detail, Search: search,
		})
	}
	return out
}

// Select implements tabs.EntityProvider.
func (t *Tab) Select(id string) tea.Cmd {
	t.want = id
	if i := slices.IndexFunc(t.items, func(a store.Artifact) bool { return a.Path == id }); i >= 0 && !t.filt.match(t.items[i]) {
		t.filt = filters{} // show what was asked for
	}
	t.rebuild()
	return nil
}

func count(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}
