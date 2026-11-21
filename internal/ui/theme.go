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
	Success   color.Color // "patina": passing checks, approvals, added lines
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
		Success:   lipgloss.Color("#7fb069"),
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
		Success:   lipgloss.Color("#3f7d3a"),
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
		"patina":   &p.Success,
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

	// PRs tab (P2.1).
	SectionActive   lipgloss.Style // active section in the section bar
	SectionInactive lipgloss.Style
	SectionSep      lipgloss.Style // "│" and the search glyph
	SearchBox       lipgloss.Style // rounded box around the section query
	SearchIcon      lipgloss.Style
	SearchQuery     lipgloss.Style
	PRRepo          lipgloss.Style // line 1 of a PR row: owner/repo · #n
	PRTitle         lipgloss.Style // line 2: the title
	DiffAdd         lipgloss.Style // +additions
	DiffDel         lipgloss.Style // -deletions
	PRTime          lipgloss.Style // updated / created ages
	PRStale         lipgloss.Style // updated age past github.stale_after_days (amber)
	PRVeryStale     lipgloss.Style // updated age past twice that (red)
	PRStaleIcon     lipgloss.Style // the dim clock under a stale PR's age

	// PR diff viewer (P2.4), when delta isn't installed.
	DiffHunk       lipgloss.Style // @@ -1,4 +1,5 @@
	DiffMeta       lipgloss.Style // index, mode, ---/+++ and "\ No newline" lines
	DiffFileHeader lipgloss.Style // the band naming each file
	DiffFile       lipgloss.Style // a file in the file list
	DiffFileActive lipgloss.Style // the file in view

	// PR detail pane (P2.2).
	DetailBand      lipgloss.Style // the title on a raised band
	DetailTab       lipgloss.Style // Overview / Checks / Activity
	DetailTabActive lipgloss.Style
	DetailRule      lipgloss.Style // rule under the tab strip
	StatusCard      lipgloss.Style // bordered box around the status lines
	CardTitle       lipgloss.Style // first line of a status entry

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

		SectionActive:   lipgloss.NewStyle().Foreground(p.OnAccent).Background(p.Accent).Bold(true).Padding(0, 1),
		SectionInactive: lipgloss.NewStyle().Foreground(p.Muted).Padding(0, 1),
		SectionSep:      lipgloss.NewStyle().Foreground(p.Border),
		SearchBox:       lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.Border).Padding(0, 1),
		SearchIcon:      lipgloss.NewStyle().Foreground(p.Accent).Bold(true),
		SearchQuery:     lipgloss.NewStyle().Foreground(p.Muted),
		PRRepo:          lipgloss.NewStyle().Foreground(Mix(p.Accent, p.Muted, 0.45)),
		PRTitle:         base,
		DiffAdd:         lipgloss.NewStyle().Foreground(p.Success),
		DiffDel:         lipgloss.NewStyle().Foreground(p.Error),
		PRTime:          base,
		PRStale:         lipgloss.NewStyle().Foreground(Mix(p.Accent, p.Highlight, 0.5)),
		PRVeryStale:     lipgloss.NewStyle().Foreground(p.Error),
		PRStaleIcon:     lipgloss.NewStyle().Foreground(p.Muted),

		DiffHunk:       lipgloss.NewStyle().Foreground(p.Accent),
		DiffMeta:       lipgloss.NewStyle().Foreground(p.Muted),
		DiffFileHeader: lipgloss.NewStyle().Foreground(p.Highlight).Background(p.Surface).Bold(true),
		DiffFile:       base,
		DiffFileActive: lipgloss.NewStyle().Foreground(p.OnAccent).Background(p.Accent).Bold(true),

		DetailBand:      lipgloss.NewStyle().Foreground(p.Highlight).Background(p.Surface).Bold(true),
		DetailTab:       lipgloss.NewStyle().Foreground(p.Muted).Padding(0, 1),
		DetailTabActive: lipgloss.NewStyle().Foreground(p.Accent).Bold(true).Underline(true).Padding(0, 1),
		DetailRule:      lipgloss.NewStyle().Foreground(p.Border),
		StatusCard:      lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.Border).Padding(0, 1),
		CardTitle:       lipgloss.NewStyle().Foreground(p.Text).Bold(true),

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

// PRIcon is a PR's state icon and its style: open, draft, merged or closed
// (the github.State* values).
func (t *Theme) PRIcon(state string) (string, lipgloss.Style) {
	s := lipgloss.NewStyle()
	switch state {
	case "draft":
		return "◌", s.Foreground(t.Muted)
	case "merged":
		return "⊕", s.Foreground(t.Link).Bold(true)
	case "closed":
		return "⊘", s.Foreground(t.Border)
	}
	return "⑂", s.Foreground(t.Accent).Bold(true)
}

// ReviewIcon is the icon and style of a PR's review decision
// (APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED, "" for none).
func (t *Theme) ReviewIcon(decision string) (string, lipgloss.Style) {
	s := lipgloss.NewStyle()
	switch decision {
	case "APPROVED":
		return "✓", s.Foreground(t.Success).Bold(true)
	case "CHANGES_REQUESTED":
		return "✗", s.Foreground(t.Error).Bold(true)
	case "REVIEW_REQUIRED":
		return "●", s.Foreground(t.Highlight)
	}
	return "·", s.Foreground(t.Muted)
}

// CIIcon is the icon and style of a PR's check rollup (SUCCESS, FAILURE,
// ERROR, PENDING, EXPECTED, "" for no checks).
func (t *Theme) CIIcon(state string) (string, lipgloss.Style) {
	s := lipgloss.NewStyle()
	switch state {
	case "SUCCESS":
		return "✓", s.Foreground(t.Success).Bold(true)
	case "FAILURE", "ERROR":
		return "✗", s.Foreground(t.Error).Bold(true)
	case "PENDING", "EXPECTED":
		return "●", s.Foreground(t.Highlight)
	}
	return "·", s.Foreground(t.Muted)
}

// StatePill is the filled pill behind a PR's state word in the detail
// header: open, draft, merged or closed (the github.State* values).
func (t *Theme) StatePill(state string) lipgloss.Style {
	s := lipgloss.NewStyle().Foreground(t.OnAccent).Bold(true).Padding(0, 1)
	switch state {
	case "draft":
		return s.Background(t.Muted)
	case "merged":
		return s.Background(t.Link)
	case "closed":
		return s.Background(t.Border)
	}
	return s.Background(t.Accent)
}

// CheckPill is the state pill of a workflow run or job (the github.Check*
// values), as StatePill is a PR's.
func (t *Theme) CheckPill(state string) lipgloss.Style {
	s := lipgloss.NewStyle().Foreground(t.OnAccent).Bold(true).Padding(0, 1)
	switch state {
	case "fail":
		return s.Background(t.Error)
	case "running", "pending":
		return s.Background(t.Highlight)
	case "pass":
		return s.Background(t.Success)
	}
	return s.Background(t.Muted)
}

// LabelChip is the style of a GitHub label: its own colour ("d73a4a", with
// or without "#") as background, with dark or light text for contrast. A
// missing or bad colour gives a surface-coloured chip.
func (t *Theme) LabelChip(hex string) lipgloss.Style {
	s := lipgloss.NewStyle().Padding(0, 1)
	hex = strings.TrimPrefix(hex, "#")
	var r, g, b uint8
	if len(hex) != 6 {
		return s.Foreground(t.Text).Background(t.Surface)
	}
	if _, err := fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b); err != nil {
		return s.Foreground(t.Text).Background(t.Surface)
	}
	fg := lipgloss.Color("#ffffff")
	if 0.299*float64(r)+0.587*float64(g)+0.114*float64(b) > 150 {
		fg = lipgloss.Color("#000000")
	}
	return s.Foreground(fg).Background(lipgloss.Color("#" + hex))
}

// CheckIcon is the icon and style of one check's normalised state (the
// github.Check* values: fail, running, pending, pass, skipped).
func (t *Theme) CheckIcon(state string) (string, lipgloss.Style) {
	s := lipgloss.NewStyle()
	switch state {
	case "fail":
		return "✗", s.Foreground(t.Error).Bold(true)
	case "running":
		return "◐", s.Foreground(t.Highlight).Bold(true)
	case "pending":
		return "○", s.Foreground(t.Highlight)
	case "pass":
		return "✓", s.Foreground(t.Success).Bold(true)
	}
	return "⊘", s.Foreground(t.Muted)
}

// ReviewStateIcon is the icon and style of one reviewer's state (APPROVED,
// CHANGES_REQUESTED, COMMENTED, DISMISSED, PENDING).
func (t *Theme) ReviewStateIcon(state string) (string, lipgloss.Style) {
	s := lipgloss.NewStyle()
	switch state {
	case "APPROVED":
		return "✓", s.Foreground(t.Success).Bold(true)
	case "CHANGES_REQUESTED":
		return "✗", s.Foreground(t.Error).Bold(true)
	case "COMMENTED":
		return "◇", s.Foreground(t.Link)
	case "PENDING":
		return "○", s.Foreground(t.Highlight)
	}
	return "⊘", s.Foreground(t.Muted)
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
