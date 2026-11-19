// Package tabs defines the Tab interface every forge tab implements. Each tab
// lives in its own subpackage.
package tabs

import (
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/config"
)

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
	// SetProject filters the tab to a project; nil means all projects.
	SetProject(p *config.Project)
}

// Updater is implemented by tabs that know when their data was last loaded,
// so the status bar can show "updated Ns ago".
type Updater interface {
	LastUpdated() time.Time
}

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
}

// EntityProvider is implemented by tabs that contribute searchable items.
// Select jumps to the item; the root model has already made the tab active.
type EntityProvider interface {
	Entities() []Entity
	Select(id string) tea.Cmd
}
