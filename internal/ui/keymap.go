package ui

import "charm.land/bubbles/v2/key"

// KeyMap holds every key binding in forge. Define bindings only here so the
// help overlay stays accurate.
type KeyMap struct {
	Quit key.Binding
}

// Keys is the active keymap.
var Keys = KeyMap{
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
}
