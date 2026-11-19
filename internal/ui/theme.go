// Package ui holds shared widgets, the theme and the keymap.
package ui

import (
	"image/color"
	"math"

	"charm.land/lipgloss/v2"
)

// Forge palette (see docs/SPEC.md "Look & feel"). These are the dark-theme
// base colours; light-theme variants live in lightPalette.
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

// Palette is the set of named colours a Theme is built from. Names match the
// keys accepted by the ui.theme config section.
type Palette struct {
	Bg        color.Color // "charcoal": app background
	Surface   color.Color // "surface": raised areas (status bar middle, selected row)
	Border    color.Color // "steel": pane borders
	Accent    color.Color // "ember": focus, active tab
	Highlight color.Color // "molten": selection, emphasis
	Link      color.Color // "coldblue": links
	Muted     color.Color // "smoke": secondary text
	Text      color.Color // "text": primary text
	OnAccent  color.Color // "onaccent": text drawn on the accent colour
	Error     color.Color // "error": errors
}

func darkPalette() Palette {
	return Palette{
		Bg:        Charcoal,
		Surface:   lipgloss.Color("#2a2826"),
		Border:    Steel,
		Accent:    Ember,
		Highlight: Molten,
		Link:      ColdBlue,
		Muted:     MutedSmoke,
		Text:      lipgloss.Color("#e8e2d9"),
		OnAccent:  Charcoal,
		Error:     lipgloss.Color("#e5484d"),
	}
}

func lightPalette() Palette {
	return Palette{
		Bg:        lipgloss.Color("#f7f3ec"),
		Surface:   lipgloss.Color("#ebe4d8"),
		Border:    lipgloss.Color("#8d929a"),
		Accent:    lipgloss.Color("#d35400"),
		Highlight: lipgloss.Color("#a66f00"),
		Link:      lipgloss.Color("#1f6fa3"),
		Muted:     lipgloss.Color("#7a736b"),
		Text:      lipgloss.Color("#2b2724"),
		OnAccent:  lipgloss.Color("#fff6e0"),
		Error:     lipgloss.Color("#c62828"),
	}
}

// apply sets palette entries from config overrides ("#rgb" / "#rrggbb",
// already validated by config). Unknown names are ignored.
func (p *Palette) apply(overrides map[string]string) {
	slots := map[string]*color.Color{
		"charcoal": &p.Bg,
		"surface":  &p.Surface,
		"steel":    &p.Border,
		"ember":    &p.Accent,
		"molten":   &p.Highlight,
		"coldblue": &p.Link,
		"smoke":    &p.Muted,
		"text":     &p.Text,
		"onaccent": &p.OnAccent,
		"error":    &p.Error,
	}
	for name, hex := range overrides {
		if slot, ok := slots[name]; ok && hex != "" {
			*slot = lipgloss.Color(hex)
		}
	}
}

// Theme is the palette plus every Lip Gloss style used by forge. Widgets and
// tabs hold a *Theme; the root model swaps its contents in place when the
// terminal background is detected, so everyone sees the change.
type Theme struct {
	Palette
	IsDark bool

	Wordmark    lipgloss.Style
	TabActive   lipgloss.Style
	TabInactive lipgloss.Style
	TabRule     lipgloss.Style

	Pane        lipgloss.Style
	PaneFocused lipgloss.Style

	TableHeader   lipgloss.Style
	TableRow      lipgloss.Style
	TableSelected lipgloss.Style
	TableEmpty    lipgloss.Style

	DetailTitle lipgloss.Style
	DetailLabel lipgloss.Style
	Body        lipgloss.Style
	LinkText    lipgloss.Style
	Hint        lipgloss.Style

	StatusLeft  lipgloss.Style
	StatusMid   lipgloss.Style
	StatusRight lipgloss.Style
	StatusKey   lipgloss.Style

	Toast      lipgloss.Style
	ToastError lipgloss.Style
}

// NewTheme builds the theme for a dark or light terminal, applying colour
// overrides from config.
func NewTheme(isDark bool, overrides map[string]string) *Theme {
	p := lightPalette()
	if isDark {
		p = darkPalette()
	}
	p.apply(overrides)

	base := lipgloss.NewStyle().Foreground(p.Text)
	pane := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.Border)

	return &Theme{
		Palette: p,
		IsDark:  isDark,

		Wordmark:    lipgloss.NewStyle().Foreground(p.Accent).Bold(true).Padding(0, 1),
		TabActive:   lipgloss.NewStyle().Foreground(p.OnAccent).Background(p.Accent).Bold(true).Padding(0, 1),
		TabInactive: lipgloss.NewStyle().Foreground(p.Muted).Padding(0, 1),
		TabRule:     lipgloss.NewStyle().Foreground(p.Border),

		Pane:        pane,
		PaneFocused: pane.BorderForeground(p.Accent),

		TableHeader:   lipgloss.NewStyle().Foreground(p.Accent).Bold(true),
		TableRow:      base,
		TableSelected: lipgloss.NewStyle().Foreground(p.Highlight).Background(p.Surface).Bold(true),
		TableEmpty:    lipgloss.NewStyle().Foreground(p.Muted).Italic(true),

		DetailTitle: lipgloss.NewStyle().Foreground(p.Highlight).Bold(true),
		DetailLabel: lipgloss.NewStyle().Foreground(p.Muted),
		Body:        base,
		LinkText:    lipgloss.NewStyle().Foreground(p.Link).Underline(true),
		Hint:        lipgloss.NewStyle().Foreground(p.Muted),

		StatusLeft:  lipgloss.NewStyle().Foreground(p.OnAccent).Background(p.Accent).Bold(true).Padding(0, 1),
		StatusMid:   lipgloss.NewStyle().Foreground(p.Text).Background(p.Surface).Padding(0, 1),
		StatusRight: lipgloss.NewStyle().Foreground(p.Muted).Background(p.Surface),
		StatusKey:   lipgloss.NewStyle().Foreground(p.Highlight).Background(p.Surface).Bold(true),

		Toast:      lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.Highlight).Foreground(p.Text).Padding(0, 1),
		ToastError: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.Error).Foreground(p.Text).Padding(0, 1),
	}
}

// heatSteps is the resolution of the precomputed heat gradient.
const heatSteps = 64

// heatGradient runs dark red -> ember orange -> molten yellow -> white-hot.
var heatGradient = lipgloss.Blend1D(heatSteps, DarkRedHeat, Ember, Molten, WhiteHot)

// Heat samples the heat gradient: 0 is dark red, 1 is white-hot. Values
// outside 0..1 are clamped.
func Heat(t float64) color.Color {
	if math.IsNaN(t) || t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return heatGradient[int(math.Round(t*float64(heatSteps-1)))]
}

// HeatRamp returns n evenly spaced samples of the heat gradient, from dark red
// to white-hot.
func HeatRamp(n int) []color.Color {
	if n <= 0 {
		return nil
	}
	if n == 1 {
		return []color.Color{Heat(0)}
	}
	out := make([]color.Color, n)
	for i := range out {
		out[i] = Heat(float64(i) / float64(n-1))
	}
	return out
}
