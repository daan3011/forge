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
}

// Placeholder is a tab with the standard list/detail layout and sample rows,
// standing in until the real tab is built.
type Placeholder struct {
	spec    PlaceholderSpec
	theme   *ui.Theme
	split   ui.SplitPane
	table   ui.Table
	project *config.Project
	updated time.Time
	now     func() time.Time
}

var _ tabs.Tab = (*Placeholder)(nil)

// NewPlaceholder returns a placeholder tab.
func NewPlaceholder(theme *ui.Theme, spec PlaceholderSpec) *Placeholder {
	t := ui.NewTable(theme, spec.Columns)
	t.SetRows(spec.Rows)
	return &Placeholder{
		spec:  spec,
		theme: theme,
		split: ui.NewSplitPane(theme),
		table: t,
		now:   time.Now,
	}
}

// Init implements tabs.Tab.
func (p *Placeholder) Init() tea.Cmd { return p.Refresh() }

// Update implements tabs.Tab.
func (p *Placeholder) Update(msg tea.Msg) (tabs.Tab, tea.Cmd) {
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(msg, ui.Keys.GrowPane):
			p.split.Grow()
			p.resizeTable()
		case key.Matches(msg, ui.Keys.ShrinkPane):
			p.split.Shrink()
			p.resizeTable()
		default:
			return p, p.table.Update(msg)
		}
	}
	return p, nil
}

// View implements tabs.Tab.
func (p *Placeholder) View() string {
	return p.split.View(p.table.View(), p.detail(), true)
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
	return []key.Binding{ui.Keys.Up, ui.Keys.Down, ui.Keys.ShrinkPane, ui.Keys.GrowPane}
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
	p.table.SetSize(p.split.LeftSize())
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
