package app

import (
	"context"

	"github.com/daan3011/forge/internal/github"

	tea "charm.land/bubbletea/v2"

	artfs "github.com/daan3011/forge/internal/artifacts"
	"github.com/daan3011/forge/internal/browser"
	"github.com/daan3011/forge/internal/checkout"
	"github.com/daan3011/forge/internal/clipboard"
	"github.com/daan3011/forge/internal/delta"
	docfs "github.com/daan3011/forge/internal/docs"
	"github.com/daan3011/forge/internal/editor"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/templates"
	"github.com/daan3011/forge/internal/ui"
)

// WithTemplates gives the new-session form its templates folder and the
// context providers that fill {issue_*} / {pr_*} variables (P2.1 and P3.1
// register theirs; nil has none). It also enables "Edit templates".
func WithTemplates(dir string, providers *templates.Registry) Option {
	return func(m *Model) { m.templatesDir, m.providers = dir, providers }
}

// WithPacks sets the context packs folder (P5.2) for the Context tab's
// Packs view and the new-session form.
func WithPacks(dir string) Option {
	return func(m *Model) { m.packsDir = dir }
}

// WithNotes sets the daily notes folder (P6.2); Home shows today's note.
func WithNotes(dir string) Option {
	return func(m *Model) { m.notesDir = dir }
}

// WithBrowser gives tabs a way to open URLs (linked issues and PRs).
func WithBrowser(b browser.Opener) Option {
	return func(m *Model) { m.browser = b }
}

// WithDocsWatcher sets the watcher that reloads the Docs tab when files in
// a docs folder change (P4.1). Without it the tab reloads on refresh only.
func WithDocsWatcher(w docfs.Watcher) Option {
	return func(m *Model) { m.docsWatcher = w }
}

// WithArtifacts sets the artifact index the Artifacts tab keeps (P4.5).
func WithArtifacts(idx tabs.ArtifactIndex) Option {
	return func(m *Model) { m.artifactIdx = idx }
}

// WithCaches gives the Context tab's Cache view its controller (P5.3).
func WithCaches(c tabs.CacheControl) Option {
	return func(m *Model) { m.caches = c }
}

// WithUsage gives the Context tab's Usage view and the Sessions tab's
// totals their token usage source (P5.4).
func WithUsage(u tabs.UsageSource) Option {
	return func(m *Model) { m.usage = u }
}

// WithArtifactWatcher makes the Artifacts tab rescan when an artifact
// changes (P4.5).
func WithArtifactWatcher(w artfs.Watcher) Option {
	return func(m *Model) { m.artifactWatch = w }
}

// WithFiles sets how local files (artifacts) are opened.
func WithFiles(f browser.FileOpener) Option {
	return func(m *Model) { m.files = f }
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

// WithPlane gives the Plane tab its client (P3.2). err says why there is
// none (Plane not configured); the tab shows it instead of issues.
func WithPlane(api plane.API, err error) Option {
	return func(m *Model) {
		if err != nil {
			api = nil // never a typed nil client
		}
		m.plane, m.planeErr = api, err
	}
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
