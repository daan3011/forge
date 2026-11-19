package ui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// HelpSection is one titled block of the help overlay. Each group is shown
// as its own column.
type HelpSection struct {
	Title  string
	Groups [][]key.Binding
}

// HelpView is the full-screen help overlay. Its content is generated from
// the bindings, so it always matches the active keymap.
type HelpView struct {
	theme    *Theme
	keys     *KeyMap
	sections []HelpSection
	width    int
	height   int
	offset   int
}

// helpColGap is the space between help columns.
const helpColGap = 4

// NewHelpView returns an empty help overlay.
func NewHelpView(theme *Theme, keys *KeyMap) HelpView {
	return HelpView{theme: theme, keys: keys}
}

// SetContent replaces the sections and scrolls back to the top.
func (h *HelpView) SetContent(sections []HelpSection) {
	h.sections = sections
	h.offset = 0
}

// SetSize sets the overlay size (the whole screen).
func (h *HelpView) SetSize(width, height int) {
	h.width, h.height = max(width, 0), max(height, 0)
	h.clamp()
}

// Update scrolls on list movement keys.
func (h *HelpView) Update(msg tea.KeyPressMsg) {
	l := h.keys.List
	half := max(h.bodyHeight()/2, 1)
	switch {
	case key.Matches(msg, l.Up):
		h.offset--
	case key.Matches(msg, l.Down):
		h.offset++
	case key.Matches(msg, l.PageUp):
		h.offset -= half
	case key.Matches(msg, l.PageDown):
		h.offset += half
	case key.Matches(msg, l.Top):
		h.offset = 0
	case key.Matches(msg, l.Bottom):
		h.offset = len(h.lines())
	}
	h.clamp()
}

func (h *HelpView) bodyHeight() int { return max(h.height-2, 0) } // title + footer

func (h *HelpView) clamp() {
	h.offset = min(max(h.offset, 0), max(len(h.lines())-h.bodyHeight(), 0))
}

// View renders the overlay at exactly its size.
func (h *HelpView) View() string {
	if h.width == 0 || h.height == 0 {
		return ""
	}
	t := h.theme
	out := make([]string, 0, h.height)
	out = append(out, fit(t.Wordmark.Render("forge")+t.HelpTitle.Render("keys"), h.width))

	lines := h.lines()
	body := h.bodyHeight()
	for i := range body {
		line := ""
		if j := h.offset + i; j < len(lines) {
			line = lines[j]
		}
		out = append(out, fit(line, h.width))
	}

	footer := []key.Binding{h.keys.Global.Help, h.keys.Input.Cancel}
	if len(lines) > body {
		footer = append(footer, h.keys.List.Down, h.keys.List.Up)
	}
	out = append(out, fit(" "+HelpLine(t, closeHelp(footer)), h.width))
	return Box(strings.Join(out[:h.height], "\n"), h.width, h.height)
}

// closeHelp relabels the first two footer bindings as "close".
func closeHelp(bs []key.Binding) []key.Binding {
	out := make([]key.Binding, len(bs))
	copy(out, bs)
	for i := range min(2, len(out)) {
		out[i].SetHelp(out[i].Help().Key, "close")
	}
	return out
}

// lines lays the sections out as text, wrapping columns to the width.
func (h *HelpView) lines() []string {
	t := h.theme
	var lines []string
	for si, s := range h.sections {
		if si > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, " "+t.HelpSection.Render(s.Title), "")

		var row []string
		rowW := 0
		flush := func() {
			if len(row) == 0 {
				return
			}
			block := lipgloss.JoinHorizontal(lipgloss.Top, row...)
			lines = append(lines, strings.Split(block, "\n")...)
			row, rowW = nil, 0
		}
		for _, g := range s.Groups {
			col := h.column(g)
			if col == "" {
				continue
			}
			w := lipgloss.Width(col) + helpColGap
			if rowW > 0 && rowW+w > h.width {
				flush()
			}
			row = append(row, lipgloss.NewStyle().PaddingLeft(helpColGap/2).PaddingRight(helpColGap/2).Render(col))
			rowW += w
		}
		flush()
	}
	return lines
}

// column renders one group as aligned "key  desc" rows.
func (h *HelpView) column(group []key.Binding) string {
	t := h.theme
	keyW := 0
	for _, b := range group {
		if b.Enabled() && b.Help().Key != "" {
			keyW = max(keyW, lipgloss.Width(b.Help().Key))
		}
	}
	var rows []string
	for _, b := range group {
		hp := b.Help()
		if !b.Enabled() || hp.Key == "" {
			continue
		}
		pad := strings.Repeat(" ", keyW-lipgloss.Width(hp.Key)+2)
		rows = append(rows, t.HelpKey.Render(hp.Key)+pad+t.HelpDesc.Render(hp.Desc))
	}
	return strings.Join(rows, "\n")
}
