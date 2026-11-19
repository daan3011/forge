package ui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// EmptyText is shown by widgets that have nothing to display.
const EmptyText = "nothing on the anvil"

// Column describes one table column. Fixed columns keep Width; Flex columns
// share the remaining width equally (Width is then a minimum).
type Column struct {
	Title string
	Width int
	Flex  bool
}

// Table is a selectable list of rows with fixed and flexible columns. Cells
// that don't fit are truncated with an ellipsis.
type Table struct {
	theme   *Theme
	cols    []Column
	rows    [][]string
	cursor  int
	offset  int
	width   int
	height  int
	widths  []int
	focused bool
}

// colGap is the space between columns.
const colGap = 1

// NewTable returns a table with the given columns.
func NewTable(theme *Theme, cols []Column) Table {
	return Table{theme: theme, cols: cols, focused: true}
}

// SetRows replaces the rows, keeping the cursor in range.
func (t *Table) SetRows(rows [][]string) {
	t.rows = rows
	t.clamp()
}

// Rows returns the table rows.
func (t *Table) Rows() [][]string { return t.rows }

// SetSize sets the outer size of the table, header included.
func (t *Table) SetSize(width, height int) {
	t.width, t.height = max(width, 0), max(height, 0)
	t.widths = columnWidths(t.cols, t.width)
	t.clamp()
}

// SetFocused controls whether the selected row is highlighted.
func (t *Table) SetFocused(f bool) { t.focused = f }

// Cursor returns the index of the selected row.
func (t *Table) Cursor() int { return t.cursor }

// SetCursor moves the selection, clamped to the rows.
func (t *Table) SetCursor(i int) {
	t.cursor = i
	t.clamp()
}

// SelectedRow returns the selected row, or nil if the table is empty.
func (t *Table) SelectedRow() []string {
	if len(t.rows) == 0 {
		return nil
	}
	return t.rows[t.cursor]
}

// Update moves the cursor on Up/Down key presses.
func (t *Table) Update(msg tea.Msg) tea.Cmd {
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(msg, Keys.Up):
			t.SetCursor(t.cursor - 1)
		case key.Matches(msg, Keys.Down):
			t.SetCursor(t.cursor + 1)
		}
	}
	return nil
}

func (t *Table) bodyHeight() int { return max(t.height-1, 0) }

func (t *Table) clamp() {
	t.cursor = min(max(t.cursor, 0), max(len(t.rows)-1, 0))
	h := t.bodyHeight()
	if h == 0 {
		t.offset = t.cursor
		return
	}
	if t.cursor < t.offset {
		t.offset = t.cursor
	}
	if t.cursor >= t.offset+h {
		t.offset = t.cursor - h + 1
	}
	t.offset = min(max(t.offset, 0), max(len(t.rows)-h, 0))
}

// View renders the table at exactly its size.
func (t *Table) View() string {
	if t.width == 0 || t.height == 0 {
		return ""
	}
	lines := make([]string, 0, t.height)
	titles := make([]string, len(t.cols))
	for i, c := range t.cols {
		titles[i] = c.Title
	}
	lines = append(lines, t.theme.TableHeader.Render(t.renderCells(titles)))

	if len(t.rows) == 0 {
		lines = append(lines, t.theme.TableEmpty.Render(fit(EmptyText, t.width)))
	}
	end := min(t.offset+t.bodyHeight(), len(t.rows))
	for i := t.offset; i < end; i++ {
		style := t.theme.TableRow
		if i == t.cursor && t.focused {
			style = t.theme.TableSelected
		}
		lines = append(lines, style.Render(t.renderCells(t.rows[i])))
	}
	for len(lines) < t.height {
		lines = append(lines, strings.Repeat(" ", t.width))
	}
	return strings.Join(lines[:t.height], "\n")
}

// renderCells lays cells out at the computed widths, padded to the full width.
func (t *Table) renderCells(cells []string) string {
	var b strings.Builder
	for i, w := range t.widths {
		if w == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteString(strings.Repeat(" ", colGap))
		}
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		b.WriteString(fit(cell, w))
	}
	return fit(b.String(), t.width)
}

// fit truncates s with an ellipsis to width cells and pads it to exactly width.
func fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	s = ansi.Truncate(s, width, "…")
	if pad := width - lipgloss.Width(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// columnWidths distributes total across columns. Fixed columns get their width
// first; flex columns share the rest. Columns that don't fit are dropped
// (width 0) from the right.
func columnWidths(cols []Column, total int) []int {
	widths := make([]int, len(cols))
	if len(cols) == 0 || total <= 0 {
		return widths
	}
	remaining := total - colGap*(len(cols)-1)
	flex := 0
	for i, c := range cols {
		if c.Flex {
			flex++
			continue
		}
		widths[i] = c.Width
		remaining -= c.Width
	}
	if flex > 0 {
		share, extra := 0, 0
		if remaining > 0 {
			share, extra = remaining/flex, remaining%flex
		}
		for i, c := range cols {
			if !c.Flex {
				continue
			}
			widths[i] = max(share, c.Width)
			if extra > 0 {
				widths[i]++
				extra--
			}
			remaining -= widths[i]
		}
	} else if remaining > 0 {
		// No flex columns: give the slack to the last column.
		widths[len(widths)-1] += remaining
		remaining = 0
	}
	// Too narrow: shrink from the right until everything fits. A column shrunk
	// to zero is skipped when rendering, which also frees its gap.
	for i := len(widths) - 1; remaining < 0 && i >= 0; i-- {
		take := min(widths[i], -remaining)
		widths[i] -= take
		remaining += take
		if widths[i] == 0 && i > 0 {
			remaining += colGap
		}
	}
	return widths
}
