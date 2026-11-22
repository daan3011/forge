// Package tabs defines the Tab interface every forge tab implements. Each tab
// lives in its own subpackage.
package tabs

import (
	"context"
	"time"

	"github.com/daan3011/forge/internal/cachectl"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/refresh"
	"github.com/daan3011/forge/internal/sessions/live"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/ui/termcaps"
)

// SessionPingMsg tells the root model that `forge hook` recorded an event,
// so it can recount waiting sessions and notify (P1.8). The Sessions tab
// emits it for every ping it receives.
type SessionPingMsg struct{ Ping live.Ping }

// Tab is one top-level screen of forge.
type Tab interface {
	Init() tea.Cmd
	Update(msg tea.Msg) (Tab, tea.Cmd)
	// View renders the tab at exactly the size given to SetSize.
	View() string
	Title() string
	// ShortHelp returns the tab's most useful bindings for the status bar,
	// most important last (at most four; the app adds help).
	ShortHelp() []key.Binding
	// FullHelp returns every tab binding for the help overlay, one column per
	// group.
	FullHelp() [][]key.Binding
	// Refresh reloads the tab's data.
	Refresh() tea.Cmd
	SetSize(width, height int)
	// SetProject filters the tab to a project; nil means all projects. Tabs
	// filter by Repo (PRs, Pipelines, Sessions), PlaneProjectID (Plane) or
	// DocsPath (Docs). The root model calls Refresh right after, so the tab
	// only needs to remember the project.
	SetProject(p *config.Project)
}

// Updater is implemented by tabs that know when their data was last loaded,
// so the status bar can show "updated Ns ago".
type Updater interface {
	LastUpdated() time.Time
}

// StatusDetailer is implemented by tabs with their own status bar text,
// such as the PRs tab's "@login · PR 3/12 (fetched 12)".
type StatusDetailer interface {
	StatusDetail() string
}

// AutoRefresher is implemented by tabs that refresh in the background. The
// root model calls Refresh on every tick of one of Sources while the tab is
// visible; hidden tabs are paused unless RefreshWhenHidden (Home, Sessions)
// and catch up when shown again.
type AutoRefresher interface {
	Sources() []refresh.Source
	RefreshWhenHidden() bool
}

// Fetcher is implemented by tabs that know whether a refresh is running, so
// the status bar can show a spinner.
type Fetcher interface {
	Fetching() bool
}

// Busier is implemented by tabs that run user actions in the background
// (merging a PR). While Busy is non-empty the status bar shows a spinner
// with it, e.g. "merging #12".
type Busier interface {
	Busy() string
}

// Animator is implemented by tabs that animate (a springing divider, a
// working agent's flame). While the tab is visible and Animating is true the
// root model keeps the shared animation clock running; tabs read the frame
// from the *anim.Engine in their deps.
type Animator interface {
	Animating() bool
}

// VisibilityAware is implemented by tabs that do work only while on screen
// (the Sessions output preview). The root model calls SetVisible(true) on
// the tab it shows and SetVisible(false) on the one it hides.
type VisibilityAware interface {
	SetVisible(visible bool) tea.Cmd
}

// GraphicsAware is implemented by tabs that draw terminal graphics (the
// Artifacts preview, P4.7).
type GraphicsAware interface {
	// SetCaps says what the terminal can show. The root model calls it
	// when detection finishes and again when the cell size changes.
	SetCaps(c termcaps.Caps) tea.Cmd
	// SetOccluded says whether something is drawn over the active tab (the
	// palette, help, quick capture, a toast). Sixel images aren't part of
	// the cell grid, so they must be cleared and redrawn around overlays.
	SetOccluded(covered bool) tea.Cmd
	// Resized follows SetSize after a window resize.
	Resized() tea.Cmd
}

// TargetedMsg is a message meant for one tab, such as the result of its
// background fetch. The root model delivers it to the tab whose Title
// matches, whether or not that tab is active.
type TargetedMsg interface {
	TargetTab() string
}

// KVStore is the key/value cache tabs and the root model persist to.
// *store.Cache satisfies it.
type KVStore interface {
	Get(ctx context.Context, key string) (store.CacheEntry, error)
	Put(ctx context.Context, key, kind, project string, value []byte, ttl time.Duration) error
}

// ArtifactIndex is the artifact gallery's index (P4.5). *store.Artifacts
// satisfies it.
type ArtifactIndex interface {
	Sync(ctx context.Context, roots []string, found []store.Artifact) error
	List(ctx context.Context, project string) ([]store.Artifact, error)
	Delete(ctx context.Context, paths []string) error

	// Pins, titles, tags and synced copies (P4.6).
	SetPinned(ctx context.Context, path, project string, on bool) error
	SetTitle(ctx context.Context, path, title string) error
	SetTags(ctx context.Context, path string, tags []string) error
	AllTags(ctx context.Context) ([]string, error)
	MoveMeta(ctx context.Context, from, to string) error
	Link(ctx context.Context, l store.ArtifactLink) error
	ActiveLinks(ctx context.Context) ([]store.ArtifactLink, error)
	MarkSynced(ctx context.Context, copyPath string, at time.Time) error
	Detach(ctx context.Context, copyPath, reason string, at time.Time) error
}

// CacheControl measures and clears what forge stores (P5.3).
// *cachectl.Controller satisfies it.
type CacheControl interface {
	Stats(ctx context.Context) (cachectl.Report, error)
	Plan(ctx context.Context, op cachectl.Op) (cachectl.Plan, error)
	Apply(ctx context.Context, p cachectl.Plan) (cachectl.Result, error)
}

// ReindexDocsMsg asks the Docs tab to rebuild its in-memory index (the
// Cache view's "clear" of the docs index).
type ReindexDocsMsg struct{}

// TargetTab implements TargetedMsg; it is the Docs tab's title.
func (ReindexDocsMsg) TargetTab() string { return "Docs" }

// InputCapturer is implemented by tabs that can take text input (a filter,
// a form). While CapturingInput is true the root model only handles
// force-quit and forwards every other key to the tab.
type InputCapturer interface {
	CapturingInput() bool
}

// Command is a tab-scoped action offered in the command palette while the
// tab is active. Key is the binding that triggers the same action, shown as
// a hint; ID matches the keymap action id where there is one.
type Command struct {
	ID    string
	Title string
	Key   key.Binding
	Run   func() tea.Cmd
}

// Commander is implemented by tabs that contribute palette commands.
type Commander interface {
	Commands() []Command
}

// Entity is an item a tab offers for palette search (a PR, an issue, a
// session, a doc). Kind names the result group, e.g. "PR" or "Issue".
type Entity struct {
	Kind   string
	ID     string
	Title  string
	Detail string
	// Search is extra text the palette matches (linked issues and PRs,
	// branch names) besides the kind and title.
	Search string
}

// EntityProvider is implemented by tabs that contribute searchable items.
// Select jumps to the item; the root model has already made the tab active.
type EntityProvider interface {
	Entities() []Entity
	Select(id string) tea.Cmd
}

// SessionGoer is implemented by the tab that owns agent sessions. The root
// model's "Go to session…" picker lists its Session entities and calls
// AttachSession with the picked id once the tab is active.
type SessionGoer interface {
	EntityProvider
	AttachSession(id string) tea.Cmd
}
