package app

import (
	"context"

	"github.com/daan3011/forge/internal/github"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/browser"
	"github.com/daan3011/forge/internal/checkout"
	"github.com/daan3011/forge/internal/clipboard"
	"github.com/daan3011/forge/internal/delta"
	"github.com/daan3011/forge/internal/editor"
	"github.com/daan3011/forge/internal/templates"
	"github.com/daan3011/forge/internal/ui"
)

// WithTemplates gives the new-session form its templates folder and the
// context providers that fill {issue_*} / {pr_*} variables (P2.1 and P3.1
// register theirs; nil has none). It also enables "Edit templates".
func WithTemplates(dir string, providers *templates.Registry) Option {
	return func(m *Model) { m.templatesDir, m.providers = dir, providers }
}

// WithBrowser gives tabs a way to open URLs (linked issues and PRs).
func WithBrowser(b browser.Opener) Option {
	return func(m *Model) { m.browser = b }
}

// WithGitHub sets the client the PRs tab searches and fetches details with (P2.1, P2.2). Without it
// the tab says GitHub isn't available.
func WithGitHub(g github.PRClient) Option {
	return func(m *Model) { m.github = g }
}

// WithRuns lets the Pipelines tab list workflow runs (P2.6).
func WithRuns(r github.RunsClient) Option {
	return func(m *Model) { m.runs = r }
}

// WithPulls lets "fix this" (P2.8) find the PR opened from a failed run's
// branch.
func WithPulls(p github.PullFinder) Option {
	return func(m *Model) { m.pulls = p }
}

// WithCheckout lets the PRs tab check PRs out locally (P2.3).
func WithCheckout(c checkout.Checker) Option {
	return func(m *Model) { m.checkout = c }
}

// WithClipboard lets tabs copy text with the system clipboard; without it
// they use OSC 52.
func WithClipboard(c clipboard.Copier) Option {
	return func(m *Model) { m.clipboard = c }
}

// WithDelta lets the PRs diff viewer colour diffs with delta (P2.4); without
// it, or when delta isn't installed, Lip Gloss colours them.
func WithDelta(d delta.Renderer) Option {
	return func(m *Model) { m.delta = d }
}

// editTemplates creates the templates folder with the defaults if needed,
// then opens it in $EDITOR. Both happen in the command, off the update loop.
func editTemplates(dir string) tea.Cmd {
	return func() tea.Msg {
		if _, err := templates.EnsureDefaults(dir); err != nil {
			return ui.ErrMsg{Err: err}
		}
		c := editor.Exec{}.Cmd(context.Background(), dir)
		return tea.ExecProcess(c, func(err error) tea.Msg {
			if err != nil {
				return ui.ErrMsg{Err: err}
			}
			return ui.InfoMsg{Text: "templates saved; the next new session uses them"}
		})()
	}
}
