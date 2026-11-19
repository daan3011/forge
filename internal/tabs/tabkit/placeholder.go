// Package tabkit holds building blocks shared by tabs.
package tabkit

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

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
}

// Placeholder is a tab with the standard list/detail layout and sample rows,
// standing in until the real tab is built.
type Placeholder struct {
	spec    PlaceholderSpec
	theme   *ui.Theme
	keys    *ui.KeyMap
	split   ui.SplitPane
	table   ui.Table
	filter  ui.Filter
	project *config.Project
	updated time.Time
	now     func() time.Time
}

var (
	_ tabs.Tab            = (*Placeholder)(nil)
	_ tabs.InputCapturer  = (*Placeholder)(nil)
	_ tabs.Commander      = (*Placeholder)(nil)
	_ tabs.EntityProvider = (*Placeholder)(nil)
)

// NewPlaceholder returns a placeholder tab.
func NewPlaceholder(theme *ui.Theme, keys *ui.KeyMap, spec PlaceholderSpec) *Placeholder {
	t := ui.NewTable(theme, keys, spec.Columns)
	t.SetRows(spec.Rows)
	return &Placeholder{
		spec:   spec,
		theme:  theme,
		keys:   keys,
		split:  ui.NewSplitPane(theme),
		table:  t,
		filter: ui.NewFilter(theme, keys),
		now:    time.Now,
	}
}

// Init implements tabs.Tab.
func (p *Placeholder) Init() tea.Cmd { return p.Refresh() }

// Update implements tabs.Tab.
func (p *Placeholder) Update(msg tea.Msg) (tabs.Tab, tea.Cmd) {
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

// Entities implements tabs.EntityProvider: the sample rows, keyed by their
// first cell.
func (p *Placeholder) Entities() []tabs.Entity {
	if p.spec.EntityKind == "" {
		return nil
	}
	out := make([]tabs.Entity, 0, len(p.spec.Rows))
	for _, row := range p.spec.Rows {
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

// Refresh implements tabs.Tab. Placeholders have nothing to load, so it only
// stamps the update time.
func (p *Placeholder) Refresh() tea.Cmd {
	p.updated = p.now()
	return nil
}

// LastUpdated implements tabs.Updater.
func (p *Placeholder) LastUpdated() time.Time { return p.updated }

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

// SetProject implements tabs.Tab.
func (p *Placeholder) SetProject(proj *config.Project) { p.project = proj }

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
