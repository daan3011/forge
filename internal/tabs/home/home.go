package home

import (
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
)

// New returns the Home tab. It is a placeholder until P6.1.
func New(theme *ui.Theme) tabs.Tab {
	return tabkit.NewPlaceholder(theme, tabkit.PlaceholderSpec{
		Title:   "Home",
		Feature: "P6.1",
		Blurb:   "Everything that needs you right now: reviews, failing CI, waiting agents, assigned issues.",
		Columns: []ui.Column{
			{Title: "Kind", Width: 8},
			{Title: "Item", Width: 0, Flex: true},
			{Title: "Age", Width: 5},
		},
		Rows: [][]string{
			{"review", "sample: Add retry to webhook sender", "2h"},
			{"ci", "sample: main failing on lint", "15m"},
			{"agent", "sample: session fix-login is waiting", "3m"},
		},
	})
}
