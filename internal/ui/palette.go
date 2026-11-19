package ui

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// PaletteRow is one result in the command palette. Rows with the same Group
// must be adjacent; each group gets a header with its icon.
type PaletteRow struct {
	Group  string
	Title  string
	Detail string // muted text after the title
	Hint   string // key hint, right-aligned
}

// paletteIcons maps result groups to their header icon.
var paletteIcons = map[string]string{
	"Recent":   "↺",
	"Commands": "⚒",
	"Tabs":     "▤",
	"Projects": "◆",
	"PRs":      "⎇",
	"Issues":   "◉",
	"Sessions": "❯",
	"Docs":     "¶",
}

// PaletteIcon returns the header icon for a result group.
func PaletteIcon(group string) string {
	if i, ok := paletteIcons[group]; ok {
		return i
	}
	return "•"
}

// CommandPalette is the command palette overlay: a query line over grouped results.
// It only edits the query and moves the selection; the owner ranks the rows
// and runs the selected one.
type CommandPalette struct {
	theme  *Theme
	keys   *KeyMap
	input  Filter
	rows   []PaletteRow
	cursor int
	offset int // first visible body line
}

// paletteChrome is the rows taken by the border, prompt and rule.
const paletteChrome = 4

// NewCommandPalette returns an empty palette.
func NewCommandPalette(theme *Theme, keys *KeyMap) CommandPalette {
	return CommandPalette{theme: theme, keys: keys, input: NewFilter(theme, keys)}
}

// Reset clears the query and results, ready to open.
func (p *CommandPalette) Reset() {
	p.input.Clear()
	p.input.Start()
	p.rows, p.cursor, p.offset = nil, 0, 0
}

// Query returns the query text.
func (p *CommandPalette) Query() string { return p.input.Value() }

// SetRows replaces the results and selects the first one.
func (p *CommandPalette) SetRows(rows []PaletteRow) {
	p.rows, p.cursor, p.offset = rows, 0, 0
}

// SetCursor selects row i, clamped to the rows.
func (p *CommandPalette) SetCursor(i int) {
	p.cursor = min(max(i, 0), max(len(p.rows)-1, 0))
	p.offset = 0
}

// Selected returns the index of the selected row, or -1 if there are none.
func (p *CommandPalette) Selected() int {
	if len(p.rows) == 0 {
		return -1
	}
	return p.cursor
}

// Update moves the selection on Next/Prev (wrapping) and otherwise edits the
// query. It reports whether the query changed, so the owner can re-rank.
func (p *CommandPalette) Update(msg tea.KeyPressMsg) bool {
	in := p.keys.Input
	switch {
	case key.Matches(msg, in.Next):
		if n := len(p.rows); n > 0 {
			p.cursor = (p.cursor + 1) % n
		}
		return false
	case key.Matches(msg, in.Prev):
		if n := len(p.rows); n > 0 {
			p.cursor = (p.cursor - 1 + n) % n
		}
		return false
	}
	return p.input.Update(msg)
}

// View renders the palette box at exactly width x height.
func (p *CommandPalette) View(width, height int) string {
	if width < 8 || height < paletteChrome+1 {
		return ""
	}
	t := p.theme
	inner := width - 4 // border + one cell of padding each side
	bodyH := height - paletteChrome

	prompt := t.PalettePrompt.Render("› ") + t.FilterText.Render(p.input.Value()+"█")
	lines := []string{fit(prompt, inner), t.TabRule.Render(strings.Repeat("─", inner))}

	body, cursorLine := p.body(inner)
	p.scroll(cursorLine, len(body), bodyH)
	for i := range bodyH {
		line := strings.Repeat(" ", inner)
		if j := p.offset + i; j < len(body) {
			line = body[j]
		}
		lines = append(lines, line)
	}
	return t.PaletteBox.Width(width).Height(height).Render(strings.Join(lines, "\n"))
}

// body lays out group headers and rows, returning the lines and the line
// index of the selected row.
func (p *CommandPalette) body(width int) (lines []string, cursorLine int) {
	t := p.theme
	if len(p.rows) == 0 {
		empty := EmptyText
		if q := p.input.Value(); q != "" {
			empty = "no matches for " + strconv.Quote(q)
		}
		return []string{t.TableEmpty.Render(fit(empty, width))}, 0
	}
	group := ""
	for i, r := range p.rows {
		if i == 0 || r.Group != group {
			group = r.Group
			lines = append(lines, fit(t.PaletteGroup.Render(PaletteIcon(group)+" "+group), width))
		}
		if i == p.cursor {
			cursorLine = len(lines)
		}
		lines = append(lines, p.row(r, width, i == p.cursor))
	}
	return lines, cursorLine
}

// row renders one result: "  title  detail ... hint".
func (p *CommandPalette) row(r PaletteRow, width int, selected bool) string {
	t := p.theme
	item, detail, hint := t.PaletteItem, t.PaletteDetail, t.PaletteHint
	if selected {
		item, detail, hint = t.PaletteSelected, t.PaletteSelectedDetail, t.PaletteSelectedHint
	}
	hintW := lipgloss.Width(r.Hint)
	if hintW > 0 {
		hintW++ // gap before the hint
	}
	leftW := max(width-hintW, 0)
	left := "  " + r.Title
	if r.Detail != "" {
		left = item.Render(left+"  ") + detail.Render(r.Detail)
	} else {
		left = item.Render(left)
	}
	left = fit(left, leftW)
	if hintW == 0 {
		return item.Render(left)
	}
	return left + item.Render(" ") + hint.Render(r.Hint)
}

// scroll keeps the selected line visible, showing the first group header
// when the first row is selected.
func (p *CommandPalette) scroll(cursorLine, total, height int) {
	if p.cursor == 0 {
		p.offset = 0
	}
	if cursorLine < p.offset {
		p.offset = cursorLine
		// Show the group header right above the selection if it is one.
		if p.offset > 0 && p.cursor > 0 && p.rows[p.cursor-1].Group != p.rows[p.cursor].Group {
			p.offset--
		}
	}
	if cursorLine >= p.offset+height {
		p.offset = cursorLine - height + 1
	}
	p.offset = min(max(p.offset, 0), max(total-height, 0))
}
