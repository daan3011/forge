// Package ui holds shared widgets, the theme and the keymap.
package ui

import (
	"fmt"
	"image/color"
	"math"
	"strings"

	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"charm.land/huh/v2"
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
	StatusWarn  lipgloss.Style // rate limit running low
	StatusWait  lipgloss.Style // sessions waiting for input

	Toast        lipgloss.Style
	ToastError   lipgloss.Style
	ToastSuccess lipgloss.Style

	HelpTitle   lipgloss.Style
	HelpSection lipgloss.Style
	HelpKey     lipgloss.Style
	HelpDesc    lipgloss.Style

	PreviewRule lipgloss.Style // rule above the session output preview
	PreviewText lipgloss.Style

	FilterPrompt lipgloss.Style
	FilterText   lipgloss.Style

	// Transcript viewer (P1.6).
	TranscriptTitle   lipgloss.Style // session name in the header
	TranscriptStat    lipgloss.Style // header numbers
	TranscriptUser    lipgloss.Style // "you" label
	TranscriptPrompt  lipgloss.Style // prompt text
	TranscriptAgent   lipgloss.Style // "claude" label
	TranscriptTool    lipgloss.Style // collapsed tool call line
	TranscriptToolOK  lipgloss.Style // ✓ on a finished call
	TranscriptToolErr lipgloss.Style // ✗ on a failed call
	TranscriptDetail  lipgloss.Style // expanded tool input / result
	TranscriptNotice  lipgloss.Style // /commands, compactions
	TranscriptCursor  lipgloss.Style // gutter bar at the selected entry

	PaletteBox            lipgloss.Style
	PalettePrompt         lipgloss.Style
	PaletteGroup          lipgloss.Style
	PaletteItem           lipgloss.Style
	PaletteDetail         lipgloss.Style
	PaletteHint           lipgloss.Style
	PaletteSelected       lipgloss.Style
	PaletteSelectedDetail lipgloss.Style
	PaletteSelectedHint   lipgloss.Style
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
		StatusWarn:  lipgloss.NewStyle().Foreground(p.OnAccent).Background(p.Error).Bold(true).Padding(0, 1),
		StatusWait:  lipgloss.NewStyle().Foreground(p.OnAccent).Background(p.Highlight).Bold(true).Padding(0, 1),

		Toast:        lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.Highlight).Foreground(p.Text).Padding(0, 1),
		ToastError:   lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.Error).Foreground(p.Text).Padding(0, 1),
		ToastSuccess: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.Accent).Foreground(p.Highlight).Bold(true).Padding(0, 1),

		HelpTitle:   lipgloss.NewStyle().Foreground(p.Accent).Bold(true),
		HelpSection: lipgloss.NewStyle().Foreground(p.Highlight).Bold(true).Underline(true),
		HelpKey:     lipgloss.NewStyle().Foreground(p.Highlight).Bold(true),
		HelpDesc:    base,

		PreviewRule: lipgloss.NewStyle().Foreground(p.Border),
		PreviewText: lipgloss.NewStyle().Foreground(p.Text),

		FilterPrompt: lipgloss.NewStyle().Foreground(p.Accent).Bold(true),
		FilterText:   base,

		TranscriptTitle:   lipgloss.NewStyle().Foreground(p.Highlight).Bold(true),
		TranscriptStat:    lipgloss.NewStyle().Foreground(p.Text),
		TranscriptUser:    lipgloss.NewStyle().Foreground(p.Link).Bold(true),
		TranscriptPrompt:  base,
		TranscriptAgent:   lipgloss.NewStyle().Foreground(p.Accent).Bold(true),
		TranscriptTool:    lipgloss.NewStyle().Foreground(p.Muted),
		TranscriptToolOK:  lipgloss.NewStyle().Foreground(p.Accent),
		TranscriptToolErr: lipgloss.NewStyle().Foreground(p.Error).Bold(true),
		TranscriptDetail:  lipgloss.NewStyle().Foreground(p.Muted),
		TranscriptNotice:  lipgloss.NewStyle().Foreground(p.Border).Italic(true),
		TranscriptCursor:  lipgloss.NewStyle().Foreground(p.Accent).Bold(true),

		PaletteBox:            lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.Accent).Padding(0, 1),
		PalettePrompt:         lipgloss.NewStyle().Foreground(p.Accent).Bold(true),
		PaletteGroup:          lipgloss.NewStyle().Foreground(p.Accent).Bold(true),
		PaletteItem:           base,
		PaletteDetail:         lipgloss.NewStyle().Foreground(p.Muted),
		PaletteHint:           lipgloss.NewStyle().Foreground(p.Highlight),
		PaletteSelected:       lipgloss.NewStyle().Foreground(p.Highlight).Background(p.Surface).Bold(true),
		PaletteSelectedDetail: lipgloss.NewStyle().Foreground(p.Muted).Background(p.Surface),
		PaletteSelectedHint:   lipgloss.NewStyle().Foreground(p.Highlight).Background(p.Surface).Bold(true),
	}
}

// SessionStatus is the style of an agent session status word ("working",
// "waiting", ...). Unknown statuses render as plain text.
func (t *Theme) SessionStatus(status string) lipgloss.Style {
	s := lipgloss.NewStyle()
	switch status {
	case "starting":
		return s.Foreground(t.Muted).Italic(true)
	case "working":
		return s.Foreground(t.Accent).Bold(true)
	case "waiting":
		return s.Foreground(t.Highlight).Bold(true)
	case "idle":
		return s.Foreground(t.Link)
	case "error":
		return s.Foreground(t.Error).Bold(true)
	case "dead":
		return s.Foreground(t.Border)
	case "archived":
		return s.Foreground(t.Muted).Italic(true)
	}
	return s.Foreground(t.Text)
}

// LinkState is the style of a linked PR's or issue's state ("open",
// "merged", a Plane state name, ...). Unknown states render muted.
func (t *Theme) LinkState(state string) lipgloss.Style {
	s := lipgloss.NewStyle()
	switch strings.ToLower(state) {
	case "open", "started", "in progress":
		return s.Foreground(t.Accent)
	case "draft", "backlog", "todo", "unstarted":
		return s.Foreground(t.Muted).Italic(true)
	case "merged", "done", "completed":
		return s.Foreground(t.Link).Bold(true)
	case "closed", "cancelled", "canceled":
		return s.Foreground(t.Border)
	}
	return s.Foreground(t.Muted)
}

// Huh is the Huh form theme in forge colours. It reads the theme each time a
// form renders, so it follows light/dark detection.
func (t *Theme) Huh() huh.Theme {
	return huh.ThemeFunc(func(isDark bool) *huh.Styles {
		h := huh.ThemeBase(isDark)
		f := &h.Focused
		f.Base = f.Base.BorderForeground(t.Accent)
		f.Card = f.Base
		f.Title = lipgloss.NewStyle().Foreground(t.Accent).Bold(true)
		f.NoteTitle = f.Title.MarginBottom(1)
		f.Description = lipgloss.NewStyle().Foreground(t.Muted)
		f.ErrorIndicator = f.ErrorIndicator.Foreground(t.Error)
		f.ErrorMessage = f.ErrorMessage.Foreground(t.Error)
		f.SelectSelector = f.SelectSelector.Foreground(t.Highlight)
		f.NextIndicator = f.NextIndicator.Foreground(t.Highlight)
		f.PrevIndicator = f.PrevIndicator.Foreground(t.Highlight)
		f.Option = lipgloss.NewStyle().Foreground(t.Text)
		f.SelectedOption = lipgloss.NewStyle().Foreground(t.Highlight)
		f.UnselectedOption = lipgloss.NewStyle().Foreground(t.Text)
		f.FocusedButton = f.FocusedButton.Foreground(t.OnAccent).Background(t.Accent).Bold(true)
		f.Next = f.FocusedButton
		f.BlurredButton = f.BlurredButton.Foreground(t.Text).Background(t.Surface)
		f.TextInput.Cursor = lipgloss.NewStyle().Foreground(t.Highlight)
		f.TextInput.Placeholder = lipgloss.NewStyle().Foreground(t.Muted)
		f.TextInput.Prompt = lipgloss.NewStyle().Foreground(t.Accent)
		f.TextInput.Text = lipgloss.NewStyle().Foreground(t.Text)

		h.Blurred = h.Focused
		h.Blurred.Base = f.Base.BorderStyle(lipgloss.HiddenBorder())
		h.Blurred.Card = h.Blurred.Base
		h.Blurred.Title = lipgloss.NewStyle().Foreground(t.Muted)
		h.Blurred.NextIndicator = lipgloss.NewStyle()
		h.Blurred.PrevIndicator = lipgloss.NewStyle()

		h.Group.Title = f.Title
		h.Group.Description = f.Description
		h.Help.ShortKey = lipgloss.NewStyle().Foreground(t.Highlight)
		h.Help.ShortDesc = lipgloss.NewStyle().Foreground(t.Muted)
		h.Help.ShortSeparator = lipgloss.NewStyle().Foreground(t.Border)
		h.Help.FullKey = h.Help.ShortKey
		h.Help.FullDesc = h.Help.ShortDesc
		h.Help.FullSeparator = h.Help.ShortSeparator
		return h
	})
}

// GlamourStyle is the Glamour markdown style in forge colours, based on
// Glamour's dark or light style, with no document margin (the transcript
// viewer indents itself).
func (t *Theme) GlamourStyle() ansi.StyleConfig {
	c := styles.LightStyleConfig
	if t.IsDark {
		c = styles.DarkStyleConfig
	}
	str := func(s string) *string { return &s }
	col := func(cc color.Color) *string { return str(Hex(cc)) }
	yes := true
	zero := uint(0)

	c.Document.Margin = &zero
	c.Document.BlockPrefix = ""
	c.Document.BlockSuffix = ""
	c.Document.Color = col(t.Text)
	c.Heading.Color = col(t.Accent)
	c.H1.Color = col(t.OnAccent)
	c.H1.BackgroundColor = col(t.Accent)
	c.H1.Bold = &yes
	c.H6.Color = col(t.Muted)
	c.HorizontalRule.Color = col(t.Border)
	c.Link.Color = col(t.Link)
	c.LinkText.Color = col(t.Link)
	c.Code.Color = col(t.Highlight)
	c.Code.BackgroundColor = col(t.Surface)
	c.BlockQuote.Color = col(t.Muted)
	return c
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

// Tint renders s in colour c. Animations use it for colours that change per
// frame; c must come from this file (the palette, Heat or Mix).
func Tint(c color.Color, s string) string {
	return lipgloss.NewStyle().Foreground(c).Render(s)
}

// TintBold is Tint in bold.
func TintBold(c color.Color, s string) string {
	return lipgloss.NewStyle().Foreground(c).Bold(true).Render(s)
}

// Mix blends a into b: 0 is a, 1 is b. Values outside 0..1 are clamped.
func Mix(a, b color.Color, t float64) color.Color {
	if math.IsNaN(t) {
		t = 0
	}
	t = math.Min(math.Max(t, 0), 1)
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	lerp := func(x, y uint32) uint8 {
		return uint8(math.Round((float64(x>>8)*(1-t) + float64(y>>8)*t)))
	}
	return color.RGBA{R: lerp(ar, br), G: lerp(ag, bg), B: lerp(ab, bb), A: 0xff}
}

// TmuxColors are the colours of forge's tmux status line (the dark palette:
// ember accent on charcoal), as #rrggbb strings for tmux's config.
func TmuxColors() (bg, fg, accent, muted string) {
	p := darkPalette()
	return Hex(p.Bg), Hex(p.Text), Hex(p.Accent), Hex(p.Muted)
}

// Hex formats c as #rrggbb.
func Hex(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}
