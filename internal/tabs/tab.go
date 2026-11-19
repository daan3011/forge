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
	// ShortHelp returns the tab's most useful bindings for the status bar.
	ShortHelp() []key.Binding
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
