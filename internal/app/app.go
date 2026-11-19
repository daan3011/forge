// Package app holds the root Bubble Tea model, tab routing, global keys and
// the command palette.
package app

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/ui"
)

// Model is the root model.
type Model struct{}

// New returns the root model.
func New() Model { return Model{} }

// Init implements tea.Model.
func (m Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.KeyPressMsg); ok && key.Matches(msg, ui.Keys.Quit) {
		return m, tea.Quit
	}
	return m, nil
}

// View implements tea.Model.
func (m Model) View() tea.View {
	return tea.NewView(ui.Wordmark.Render("forge") + "\n\n" + ui.Hint.Render("q quit") + "\n")
}
