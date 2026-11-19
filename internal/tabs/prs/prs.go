package prs

import (
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
)

// New returns the PRs tab. It is a placeholder until P2.1.
func New(theme *ui.Theme, keys *ui.KeyMap) tabs.Tab {
	return tabkit.NewPlaceholder(theme, keys, tabkit.PlaceholderSpec{
		Title:   "PRs",
		Feature: "P2.1",
		Blurb:   "Pull requests grouped by your configured sections, with CI and review status.",
		Columns: []ui.Column{
			{Title: "#", Width: 5},
			{Title: "Title", Width: 0, Flex: true},
			{Title: "Author", Width: 10},
			{Title: "CI", Width: 4},
		},
		Rows: [][]string{
			{"#101", "sample: Add retry to webhook sender", "alice", "ok"},
			{"#98", "sample: Bump dependencies", "bot", "fail"},
			{"#95", "sample: Refactor config loader", "you", "run"},
		},
	})
}
