package ui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// KeyMap holds every key binding in forge. Define bindings only here so the
// help overlay stays accurate.
type KeyMap struct {
	Quit    key.Binding
	Help    key.Binding
	NextTab key.Binding
	PrevTab key.Binding
	GotoTab key.Binding

	Up         key.Binding
	Down       key.Binding
	GrowPane   key.Binding
	ShrinkPane key.Binding
}

// Keys is the active keymap.
var Keys = KeyMap{
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "help"),
	),
	NextTab: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "next tab"),
	),
	PrevTab: key.NewBinding(
		key.WithKeys("shift+tab"),
		key.WithHelp("shift+tab", "prev tab"),
	),
	GotoTab: key.NewBinding(
		key.WithKeys("1", "2", "3", "4", "5", "6", "7", "8", "9"),
		key.WithHelp("1-9", "go to tab"),
	),
	Up: key.NewBinding(
		key.WithKeys("k", "up"),
		key.WithHelp("↑/k", "up"),
	),
	Down: key.NewBinding(
		key.WithKeys("j", "down"),
		key.WithHelp("↓/j", "down"),
	),
	GrowPane: key.NewBinding(
		key.WithKeys(">"),
		key.WithHelp(">", "widen list"),
	),
	ShrinkPane: key.NewBinding(
		key.WithKeys("<"),
		key.WithHelp("<", "narrow list"),
	),
}

// Global returns the bindings that work on every tab.
func (k KeyMap) Global() []key.Binding {
	return []key.Binding{k.GotoTab, k.NextTab, k.PrevTab, k.Help, k.Quit}
}

// TabIndex returns the zero-based tab index for a GotoTab key press, or -1 if
// the key is not a GotoTab key.
func (k KeyMap) TabIndex(msg tea.KeyPressMsg) int {
	if !key.Matches(msg, k.GotoTab) {
		return -1
	}
	s := msg.String()
	if len(s) != 1 || s[0] < '1' || s[0] > '9' {
		return -1
	}
	return int(s[0] - '1')
}
