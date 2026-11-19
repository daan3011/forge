package artifacts

import (
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
)

// New returns the Artifacts tab. It is a placeholder until P4.5.
func New(theme *ui.Theme) tabs.Tab {
	return tabkit.NewPlaceholder(theme, tabkit.PlaceholderSpec{
		Title:   "Artifacts",
		Feature: "P4.5",
		Blurb:   "Design artifacts from your watched folders, with pins, tags and previews.",
		Columns: []ui.Column{
			{Title: "Artifact", Width: 0, Flex: true},
			{Title: "Type", Width: 6},
			{Title: "Age", Width: 5},
		},
		Rows: [][]string{
			{"sample: tab-bar-mock.png", "png", "1d"},
			{"sample: flow.svg", "svg", "3d"},
		},
	})
}
