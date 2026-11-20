package app

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/browser"
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
