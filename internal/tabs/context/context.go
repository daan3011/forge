package context

import (
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
)

// New returns the Context tab. It is a placeholder until P5.1.
func New(d tabkit.Deps) tabs.Tab {
	return tabkit.NewPlaceholder(d, tabkit.PlaceholderSpec{
		Title:   "Context",
		Feature: "P5.1",
		Blurb:   "CLAUDE.md files, context packs, cache controls and usage.",
		Columns: []ui.Column{
			{Title: "File", Width: 0, Flex: true},
			{Title: "Tokens", Width: 7},
		},
		Rows: [][]string{
			{"sample: CLAUDE.md", "1.2k"},
			{"sample: ~/.claude/CLAUDE.md", "480"},
		},
	})
}
