package plane

import (
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
)

// New returns the Plane tab. It is a placeholder until P3.2.
func New(theme *ui.Theme, keys *ui.KeyMap) tabs.Tab {
	return tabkit.NewPlaceholder(theme, keys, tabkit.PlaceholderSpec{
		Title:   "Plane",
		Feature: "P3.2",
		Blurb:   "Issues from your Plane workspace as a list or kanban board.",
		Columns: []ui.Column{
			{Title: "ID", Width: 8},
			{Title: "Title", Width: 0, Flex: true},
			{Title: "State", Width: 10},
		},
		Rows: [][]string{
			{"FRG-12", "sample: Project switcher", "In Progress"},
			{"FRG-9", "sample: Palette fuzzy rank", "Todo"},
			{"FRG-4", "sample: Store migrations", "Done"},
		},
	})
}
