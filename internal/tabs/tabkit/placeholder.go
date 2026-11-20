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

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/refresh"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
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
	split   ui.SplitPane
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
)

// NewPlaceholder returns a placeholder tab. It starts empty; Init loads the
// cached rows and fetches the sample rows.
func NewPlaceholder(d Deps, spec PlaceholderSpec) *Placeholder {
	return &Placeholder{
		spec:   spec,
		theme:  d.Theme,
		keys:   d.Keys,
		store:  d.Store,
		delay:  d.FetchDelay,
		split:  ui.NewSplitPane(d.Theme),
		table:  ui.NewTable(d.Theme, d.Keys, spec.Columns),
		filter: ui.NewFilter(d.Theme, d.Keys),
		now:    time.Now,
		key:    CacheKey(spec.Title, nil),
	}
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

func (p *Placeholder) grow() {
	p.split.Grow()
	p.resizeTable()
}

func (p *Placeholder) shrink() {
	p.split.Shrink()
	p.resizeTable()
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
