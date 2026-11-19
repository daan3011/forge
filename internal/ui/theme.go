// Package ui holds shared widgets, the theme and the keymap.
package ui

import "charm.land/lipgloss/v2"

// Forge palette (see docs/SPEC.md "Look & feel").
var (
	Charcoal    = lipgloss.Color("#1c1b1a")
	Steel       = lipgloss.Color("#6b7078")
	Ember       = lipgloss.Color("#ff7a1a")
	Molten      = lipgloss.Color("#ffd23f")
	ColdBlue    = lipgloss.Color("#5fa8d3")
	MutedSmoke  = lipgloss.Color("#8a8580")
	WhiteHot    = lipgloss.Color("#fff6e0")
	DarkRedHeat = lipgloss.Color("#7a1f12")
)

// Styles used across the app.
var (
	Wordmark = lipgloss.NewStyle().Foreground(Ember).Bold(true)
	Hint     = lipgloss.NewStyle().Foreground(MutedSmoke)
)
