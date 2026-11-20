package sessions

import (
	"github.com/daan3011/forge/internal/refresh"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
)

// New returns the Sessions tab. It is a placeholder until P1.3.
func New(d tabkit.Deps) tabs.Tab {
	return tabkit.NewPlaceholder(d, tabkit.PlaceholderSpec{
		Title:             "Sessions",
		Feature:           "P1.3",
		Blurb:             "Named Claude Code sessions, each with a worktree, tmux window and live status.",
		EntityKind:        "Session",
		Sources:           []refresh.Source{refresh.Sessions},
		RefreshWhenHidden: true,
		Columns: []ui.Column{
			{Title: "Name", Width: 0, Flex: true},
			{Title: "Status", Width: 9},
			{Title: "Branch", Width: 14},
		},
		Rows: [][]string{
			{"sample: fix-login", "waiting", "fix/login"},
			{"sample: shell", "working", "p0.4-app-shell"},
			{"sample: docs", "idle", "docs/spec"},
		},
	})
}
