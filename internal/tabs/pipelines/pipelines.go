package pipelines

import (
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
)

// New returns the Pipelines tab. It is a placeholder until P2.6.
func New(theme *ui.Theme) tabs.Tab {
	return tabkit.NewPlaceholder(theme, tabkit.PlaceholderSpec{
		Title:   "Pipelines",
		Feature: "P2.6",
		Blurb:   "Recent workflow runs for the active project, with log tail and rerun.",
		Columns: []ui.Column{
			{Title: "Workflow", Width: 0, Flex: true},
			{Title: "Branch", Width: 12},
			{Title: "Status", Width: 8},
		},
		Rows: [][]string{
			{"sample: ci", "main", "failed"},
			{"sample: release", "v0.3.0", "success"},
			{"sample: ci", "feat/x", "running"},
		},
	})
}
