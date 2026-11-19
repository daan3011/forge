package ui

import (
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// Filter is a one-line filter input ("/ query"). The owner decides when to
// start it and handles Accept/Cancel; Update only edits the text.
type Filter struct {
	theme  *Theme
	keys   *KeyMap
	value  string
	active bool
}

// NewFilter returns an inactive, empty filter.
func NewFilter(theme *Theme, keys *KeyMap) Filter { return Filter{theme: theme, keys: keys} }

// Start focuses the filter for typing.
func (f *Filter) Start() { f.active = true }

// Stop leaves typing mode but keeps the text.
func (f *Filter) Stop() { f.active = false }

// Clear empties the filter and leaves typing mode.
func (f *Filter) Clear() { f.value, f.active = "", false }

// Active reports whether the filter is capturing key presses.
func (f *Filter) Active() bool { return f.active }

// Value returns the filter text.
func (f *Filter) Value() string { return f.value }

// Visible reports whether the filter line should be shown.
func (f *Filter) Visible() bool { return f.active || f.value != "" }

// Update applies a key press to the text and reports whether it changed.
func (f *Filter) Update(msg tea.KeyPressMsg) bool {
	if !f.active {
		return false
	}
	switch {
	case key.Matches(msg, f.keys.Input.DeleteBack):
		if f.value == "" {
			return false
		}
		_, n := utf8.DecodeLastRuneInString(f.value)
		f.value = f.value[:len(f.value)-n]
		return true
	case key.Matches(msg, f.keys.Input.ClearInput):
		changed := f.value != ""
		f.value = ""
		return changed
	}
	if msg.Text == "" || msg.Mod&^tea.ModShift != 0 {
		return false
	}
	f.value += msg.Text
	return true
}

// View renders the filter line at exactly width cells.
func (f *Filter) View(width int) string {
	cursor := ""
	if f.active {
		cursor = "█"
	}
	line := f.theme.FilterPrompt.Render("/ ") + f.theme.FilterText.Render(f.value+cursor)
	return fit(line, width)
}
