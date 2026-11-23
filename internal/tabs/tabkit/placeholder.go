// Package tabkit holds building blocks shared by tabs.
package tabkit

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/artifacts"
	"github.com/daan3011/forge/internal/browser"
	"github.com/daan3011/forge/internal/chafa"
	"github.com/daan3011/forge/internal/checkout"
	"github.com/daan3011/forge/internal/clipboard"
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/delta"
	"github.com/daan3011/forge/internal/docs"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/refresh"
	"github.com/daan3011/forge/internal/rsvg"
	"github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/live"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/templates"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
)

// SampleFetchDelay is the simulated latency of a placeholder "fetch", so the
// loading state is visible.
const SampleFetchDelay = 700 * time.Millisecond

// Deps are what every tab is built with.
type Deps struct {
	Theme *ui.Theme
	Keys  *ui.KeyMap
	// Store caches tab data; nil disables caching.
	Store tabs.KVStore
	// FetchDelay is the simulated latency of placeholder fetches.
	FetchDelay time.Duration
	// Anim is the shared animation clock; nil means animations off.
	Anim *anim.Engine
	// Config is the loaded config; nil means the defaults.
	Config *config.Config
	// Sessions starts and manages agent sessions; nil when forge runs
	// without its database (tests, previews).
	Sessions sessions.API
	// Live delivers `forge hook` pings (P1.4); nil when nothing listens.
	Live <-chan live.Ping
	// TemplatesDir is the session templates folder (P1.7), created with
	// the defaults on first use; "" offers no templates.
	TemplatesDir string
	// PacksDir is the context packs folder (P5.2); "" offers none.
	PacksDir string
	// NotesDir holds the daily notes (P6.2); Home shows today's. "" shows
	// none.
	NotesDir string
	// Providers resolve {issue_*} / {pr_*} template variables; nil has
	// none, so those are asked for in the form.
	Providers *templates.Registry
	// Browser opens URLs (linked issues and PRs); nil can't open any.
	Browser browser.Opener
	// GitHub runs the PRs tab searches (P2.1) and detail fetches (P2.2); nil shows that GitHub
	// isn't available.
	GitHub github.PRClient
	// Checkout checks PRs out locally (P2.3); nil can't.
	Checkout checkout.Checker
	// Clipboard copies text (PR URLs); nil falls back to OSC 52.
	Clipboard clipboard.Copier
	// Delta colours PR diffs (P2.4); nil, or not installed, falls back to
	// Lip Gloss colours.
	Delta delta.Renderer
	// Runs lists workflow runs and their jobs for the Pipelines tab
	// (P2.6); nil shows that GitHub isn't available.
	Runs github.RunsClient
	// Pulls finds the PR opened from a failed run's branch for "fix this"
	// (P2.8); nil treats every run as having no PR.
	Pulls github.PullFinder
	// Plane lists and reads work items for the Plane tab (P3.2); nil shows
	// PlaneErr, why it isn't available.
	Plane    plane.API
	PlaneErr error
	// ProjectLinks saves the Plane project picked or matched for a repo;
	// nil keeps links for this run only.
	ProjectLinks tabs.ProjectLinker
	// DocsWatcher signals changes in the docs folders (P4.1); nil means
	// the Docs tab only reloads on refresh.
	DocsWatcher docs.Watcher
	// Artifacts persists the artifact index (P4.5); nil shows each scan
	// without remembering it.
	Artifacts tabs.ArtifactIndex
	// Proposals are the issues agents proposed through forge mcp (P6.5),
	// shown on Home; nil shows none.
	Proposals tabs.ProposalQueue
	// Links reads forge's links (the Docs tab marks docs agents wrote
	// through forge mcp); nil marks none.
	Links tabs.LinkReader
	// Caches measures and clears forge's stored data for the Context tab's
	// Cache view (P5.3); nil shows it as unavailable.
	Caches tabs.CacheControl
	// Usage scans transcripts for token usage (P5.4): the Context tab's
	// Usage view and the Sessions tab's totals; nil shows it as unavailable.
	Usage tabs.UsageSource
	// ArtifactWatcher signals artifact changes (P4.5); nil means the
	// Artifacts tab only rescans on refresh.
	ArtifactWatcher artifacts.Watcher
	// Files opens local files (artifacts) in their default app; nil can't.
	Files browser.FileOpener
	// Chafa draws image previews as text art when the terminal has no
	// graphics (P4.7); nil means none.
	Chafa chafa.Renderer
	// Rsvg rasterises SVGs for previews (P4.7); nil shows SVGs as text.
	Rsvg rsvg.Rasterizer
}

// Engine returns d.Anim, or a clock that never ticks.
func (d Deps) Engine() *anim.Engine {
	if d.Anim != nil {
		return d.Anim
	}
	return anim.NewEngine(anim.Settings{Level: config.AnimationsOff})
}

// PlaceholderSpec configures a placeholder tab.
type PlaceholderSpec struct {
	Title   string
	Feature string // spec ID of the feature that will replace the placeholder
	Blurb   string // one line on what the tab will show
	Columns []ui.Column
	Rows    [][]string // sample rows
	// EntityKind names the sample rows in palette search ("PR", "Issue", ...).
	// Empty means the tab contributes no entities.
	EntityKind string
	// EntityTitleCells is how many leading cells form the entity title (the
	// rest become its detail); 0 means 1.
	EntityTitleCells int
	// Sources are the data sources whose ticks refresh the tab.
	Sources []refresh.Source
	// RefreshWhenHidden keeps refreshing while another tab is shown.
	RefreshWhenHidden bool
}

// Placeholder is a tab with the standard list/detail layout and sample rows,
// standing in until the real tab is built. It loads its rows cache-first:
// the cached copy renders immediately, then a background "fetch" of the
// sample rows replaces it and is written back to the cache.
type Placeholder struct {
	spec    PlaceholderSpec
	theme   *ui.Theme
	keys    *ui.KeyMap
	store   tabs.KVStore
	delay   time.Duration
	anim    *anim.Engine
	split   ui.SplitPane
	divider anim.Spring // springs the split ratio after grow/shrink
	sliding bool        // divider is in motion
	table   ui.Table
	filter  ui.Filter
	project *config.Project
	rows    [][]string
	updated time.Time
	now     func() time.Time

	key        string // cache key for the current project
	cacheAsked string // key whose cached copy has been requested
	fetching   bool
	fresh      bool // a fetch for key has landed; ignore older cached data
}

var (
	_ tabs.Tab            = (*Placeholder)(nil)
	_ tabs.InputCapturer  = (*Placeholder)(nil)
	_ tabs.Commander      = (*Placeholder)(nil)
	_ tabs.EntityProvider = (*Placeholder)(nil)
	_ tabs.Updater        = (*Placeholder)(nil)
	_ tabs.Fetcher        = (*Placeholder)(nil)
	_ tabs.AutoRefresher  = (*Placeholder)(nil)
	_ tabs.Animator       = (*Placeholder)(nil)
)

// NewPlaceholder returns a placeholder tab. It starts empty; Init loads the
// cached rows and fetches the sample rows.
func NewPlaceholder(d Deps, spec PlaceholderSpec) *Placeholder {
	p := &Placeholder{
		spec:   spec,
		theme:  d.Theme,
		keys:   d.Keys,
		store:  d.Store,
		delay:  d.FetchDelay,
		anim:   d.Engine(),
		split:  ui.NewSplitPane(d.Theme),
		table:  ui.NewTable(d.Theme, d.Keys, spec.Columns),
		filter: ui.NewFilter(d.Theme, d.Keys),
		now:    time.Now,
		key:    CacheKey(spec.Title, nil),
	}
	p.table.SetEmptyView(func(w, h int) string { return anim.EmptyForge(w, h, d.Theme) })
	return p
}

// Init implements tabs.Tab.
func (p *Placeholder) Init() tea.Cmd { return p.Refresh() }

// Update implements tabs.Tab.
func (p *Placeholder) Update(msg tea.Msg) (tabs.Tab, tea.Cmd) {
	switch msg := msg.(type) {
	case CacheLoadedMsg:
		p.cacheLoaded(msg)
		return p, nil
	case FetchedMsg:
		return p, p.fetched(msg)
	}
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	if p.filter.Active() {
		p.updateFilter(kp)
		return p, nil
	}
	l := p.keys.List
	switch {
	case key.Matches(kp, l.Filter):
		p.startFilter()
	case key.Matches(kp, p.keys.Input.Cancel) && p.filter.Value() != "":
		p.clearFilter()
	case key.Matches(kp, l.GrowPane):
		p.grow()
	case key.Matches(kp, l.ShrinkPane):
		p.shrink()
	default:
		return p, p.table.Update(kp)
	}
	return p, nil
}

func (p *Placeholder) startFilter() {
	p.filter.Start()
	p.resizeTable()
}

func (p *Placeholder) grow() { p.resize(p.split.Grow) }

func (p *Placeholder) shrink() { p.resize(p.split.Shrink) }

// resize changes the split ratio and springs the divider from where it is
// shown now to the new ratio.
func (p *Placeholder) resize(change func()) {
	from := p.split.ShownRatio()
	change()
	p.divider = anim.NewSpring(from, p.split.Ratio(), p.anim.Frame(), p.anim.Settings())
	p.sliding = true
	p.slide()
}

// slide lays the panes out at the divider's position for the current frame.
func (p *Placeholder) slide() {
	if !p.sliding {
		return
	}
	pos, settled := p.divider.At(p.anim.Frame())
	if settled {
		pos, p.sliding = p.split.Ratio(), false
	}
	if pos != p.split.ShownRatio() {
		p.split.ShowRatio(pos)
		p.resizeTable()
	}
}

// Animating implements tabs.Animator: true while the divider springs.
func (p *Placeholder) Animating() bool {
	return p.sliding && !p.divider.Settled(p.anim.Frame())
}

// Commands implements tabs.Commander.
func (p *Placeholder) Commands() []tabs.Command {
	l := p.keys.List
	do := func(f func()) func() tea.Cmd { return func() tea.Cmd { f(); return nil } }
	return []tabs.Command{
		{ID: "filter", Title: "Filter " + p.spec.Title, Key: l.Filter, Run: do(p.startFilter)},
		{ID: "grow_pane", Title: "Widen list", Key: l.GrowPane, Run: do(p.grow)},
		{ID: "shrink_pane", Title: "Narrow list", Key: l.ShrinkPane, Run: do(p.shrink)},
	}
}

// Entities implements tabs.EntityProvider: the loaded rows, keyed by their
// first cell.
func (p *Placeholder) Entities() []tabs.Entity {
	if p.spec.EntityKind == "" {
		return nil
	}
	out := make([]tabs.Entity, 0, len(p.rows))
	for _, row := range p.rows {
		if len(row) == 0 {
			continue
		}
		n := min(max(p.spec.EntityTitleCells, 1), len(row))
		out = append(out, tabs.Entity{
			Kind:   p.spec.EntityKind,
			ID:     row[0],
			Title:  strings.Join(row[:n], " "),
			Detail: strings.Join(row[n:], " · "),
		})
	}
	return out
}

// Select implements tabs.EntityProvider: it clears any filter and moves the
// cursor to the row whose first cell is id.
func (p *Placeholder) Select(id string) tea.Cmd {
	if p.filter.Visible() {
		p.clearFilter()
	}
	for i, row := range p.table.VisibleRows() {
		if len(row) > 0 && row[0] == id {
			p.table.SetCursor(i)
			break
		}
	}
	return nil
}

func (p *Placeholder) updateFilter(msg tea.KeyPressMsg) {
	in := p.keys.Input
	switch {
	case key.Matches(msg, in.Accept):
		p.filter.Stop()
		if p.filter.Value() == "" {
			p.resizeTable()
		}
	case key.Matches(msg, in.Cancel):
		p.clearFilter()
	default:
		if p.filter.Update(msg) {
			p.table.SetFilter(p.filter.Value())
		}
	}
}

func (p *Placeholder) clearFilter() {
	p.filter.Clear()
	p.table.SetFilter("")
	p.resizeTable()
}

// CapturingInput implements tabs.InputCapturer.
func (p *Placeholder) CapturingInput() bool { return p.filter.Active() }

// View implements tabs.Tab.
func (p *Placeholder) View() string {
	p.slide()
	list := p.table.View()
	if p.filter.Visible() {
		w, _ := p.split.LeftSize()
		list = p.filter.View(w) + "\n" + list
	}
	return p.split.View(list, p.detail(), true)
}

func (p *Placeholder) detail() string {
	t := p.theme
	w, _ := p.split.RightSize()
	lines := []string{t.DetailTitle.Render(p.spec.Title), ""}
	if row := p.table.SelectedRow(); row != nil {
		for i, c := range p.spec.Columns {
			if i < len(row) {
				lines = append(lines, t.DetailLabel.Render(c.Title+": ")+t.Body.Render(row[i]))
			}
		}
		lines = append(lines, "")
	}
	project := "all projects"
	if p.project != nil {
		project = p.project.Name
	}
	lines = append(lines,
		t.DetailLabel.Render("project: ")+t.Body.Render(project),
		"",
		t.Hint.Render(wrap(p.spec.Blurb, w)),
		t.Hint.Render("sample data · coming in "+p.spec.Feature),
		"",
		ui.HelpLine(t, p.ShortHelp()),
	)
	return strings.Join(lines, "\n")
}

// Title implements tabs.Tab.
func (p *Placeholder) Title() string { return p.spec.Title }

// ShortHelp implements tabs.Tab.
func (p *Placeholder) ShortHelp() []key.Binding {
	if p.filter.Active() {
		return []key.Binding{p.keys.Input.Cancel, p.keys.Input.Accept}
	}
	l := p.keys.List
	return []key.Binding{p.keys.Global.Refresh, l.Filter, l.Up, l.Down}
}

// FullHelp implements tabs.Tab.
func (p *Placeholder) FullHelp() [][]key.Binding {
	l, in := p.keys.List, p.keys.Input
	return [][]key.Binding{
		{l.Up, l.Down, l.Top, l.Bottom, l.PageDown, l.PageUp},
		{l.Filter, l.ShrinkPane, l.GrowPane},
		{in.Accept, in.Cancel, in.DeleteBack, in.ClearInput},
	}
}

// Refresh implements tabs.Tab. The first refresh for a project also asks for
// the cached rows; a refresh while a fetch is running does nothing.
func (p *Placeholder) Refresh() tea.Cmd {
	var load tea.Cmd
	if p.cacheAsked != p.key {
		p.cacheAsked = p.key
		load = LoadCached(p.store, p.spec.Title, p.key)
	}
	if p.fetching {
		return load
	}
	p.fetching = true
	rows, delay := p.spec.Rows, p.delay
	fetch := FetchAndCache(p.store, Fetch{
		Tab:     p.spec.Title,
		Key:     p.key,
		Project: p.project,
		Now:     p.now,
		Run: func(ctx context.Context) ([]byte, error) {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return json.Marshal(rows)
		},
	})
	return tea.Batch(load, fetch)
}

func (p *Placeholder) cacheLoaded(msg CacheLoadedMsg) {
	if msg.Key != p.key || p.fresh {
		return
	}
	var rows [][]string
	if json.Unmarshal(msg.Value, &rows) != nil {
		return // unreadable cache: wait for the fetch
	}
	p.setRows(rows)
	p.updated = msg.FetchedAt
}

func (p *Placeholder) fetched(msg FetchedMsg) tea.Cmd {
	if msg.Key != p.key {
		return nil // fetched for a project we have since left
	}
	p.fetching = false
	if msg.Err != nil {
		return errCmd(msg.Err)
	}
	var rows [][]string
	if err := json.Unmarshal(msg.Value, &rows); err != nil {
		return errCmd(fmt.Errorf("%s: %w", strings.ToLower(p.spec.Title), err))
	}
	p.fresh = true
	p.setRows(rows)
	p.updated = msg.At
	if msg.CacheErr != nil {
		return errCmd(msg.CacheErr)
	}
	return nil
}

func (p *Placeholder) setRows(rows [][]string) {
	p.rows = rows
	p.table.SetRows(rows)
}

func errCmd(err error) tea.Cmd {
	return func() tea.Msg { return ui.ErrMsg{Err: err} }
}

// LastUpdated implements tabs.Updater: when the shown rows were fetched
// (possibly in an earlier run, for cached rows).
func (p *Placeholder) LastUpdated() time.Time { return p.updated }

// Fetching implements tabs.Fetcher.
func (p *Placeholder) Fetching() bool { return p.fetching }

// Sources implements tabs.AutoRefresher.
func (p *Placeholder) Sources() []refresh.Source { return slices.Clone(p.spec.Sources) }

// RefreshWhenHidden implements tabs.AutoRefresher.
func (p *Placeholder) RefreshWhenHidden() bool { return p.spec.RefreshWhenHidden }

// SetSize implements tabs.Tab.
func (p *Placeholder) SetSize(width, height int) {
	p.split.SetSize(width, height)
	p.resizeTable()
}

func (p *Placeholder) resizeTable() {
	w, h := p.split.LeftSize()
	if p.filter.Visible() {
		h = max(h-1, 0)
	}
	p.table.SetSize(w, h)
}

// SetProject implements tabs.Tab. A new project means a new cache key and
// no rows until the next Refresh loads them; the fetch in flight for the old
// project is dropped when it lands.
func (p *Placeholder) SetProject(proj *config.Project) {
	p.project = proj
	if key := CacheKey(p.spec.Title, proj); key != p.key {
		p.key = key
		p.fetching, p.fresh = false, false
		p.updated = time.Time{} // never shown for this project
		p.setRows(nil)
	}
}

// wrap breaks s into lines of at most width cells on word boundaries.
func wrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	var lines []string
	var cur string
	for _, w := range strings.Fields(s) {
		switch {
		case cur == "":
			cur = w
		case len(cur)+1+len(w) <= width:
			cur += " " + w
		default:
			lines = append(lines, cur)
			cur = w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return strings.Join(lines, "\n")
}
