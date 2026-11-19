package docs

import (
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
)

// New returns the Docs tab. It is a placeholder until P4.1.
func New(theme *ui.Theme, keys *ui.KeyMap) tabs.Tab {
	return tabkit.NewPlaceholder(theme, keys, tabkit.PlaceholderSpec{
		Title:      "Docs",
		Feature:    "P4.1",
		Blurb:      "Project docs and the decision log from .forge/docs.",
		EntityKind: "Doc",
		Columns: []ui.Column{
			{Title: "Doc", Width: 0, Flex: true},
			{Title: "Updated", Width: 10},
		},
		Rows: [][]string{
			{"sample: architecture.md", "2d"},
			{"sample: decisions/0001-sqlite.md", "1w"},
		},
	})
}
