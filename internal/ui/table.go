package ui

import (
	"strconv"
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
	keys    *KeyMap
	cols    []Column
	rows    [][]string
	visible [][]string // rows matching the filter
	filter  string
	cursor  int
	offset  int
	width   int
	height  int
	widths  []int
	focused bool
	empty   func(width, height int) string // see SetEmptyView
}

// SetEmptyView draws the body of a table that has no rows at all (not one
// whose filter matches nothing), such as anim.EmptyForge. nil restores the
// one-line EmptyText.
func (t *Table) SetEmptyView(f func(width, height int) string) { t.empty = f }

// colGap is the space between columns.
const colGap = 1

// NewTable returns a table with the given columns.
func NewTable(theme *Theme, keys *KeyMap, cols []Column) Table {
	return Table{theme: theme, keys: keys, cols: cols, focused: true}
}

// SetRows replaces the rows, keeping the cursor in range.
func (t *Table) SetRows(rows [][]string) {
	t.rows = rows
	t.applyFilter()
}

// Rows returns all table rows, ignoring the filter.
func (t *Table) Rows() [][]string { return t.rows }

// VisibleRows returns the rows that match the filter.
func (t *Table) VisibleRows() [][]string { return t.visible }

// SetFilter shows only rows with a cell containing q (case-insensitive).
// An empty q shows every row.
func (t *Table) SetFilter(q string) {
	t.filter = q
	t.applyFilter()
}

// Filter returns the current filter text.
func (t *Table) Filter() string { return t.filter }

func (t *Table) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(t.filter))
	if q == "" {
		t.visible = t.rows
	} else {
		t.visible = nil
		for _, row := range t.rows {
			for _, cell := range row {
				if strings.Contains(strings.ToLower(cell), q) {
					t.visible = append(t.visible, row)
					break
				}
			}
		}
	}
	t.clamp()
}

// SetSize sets the outer size of the table, header included.
func (t *Table) SetSize(width, height int) {
	t.width, t.height = max(width, 0), max(height, 0)
	t.widths = columnWidths(t.cols, t.width)
	t.clamp()
}

// SetFocused controls whether the selected row is highlighted.
func (t *Table) SetFocused(f bool) { t.focused = f }

// Cursor returns the index of the selected row among the visible rows.
func (t *Table) Cursor() int { return t.cursor }

// SetCursor moves the selection, clamped to the rows.
func (t *Table) SetCursor(i int) {
	t.cursor = i
	t.clamp()
}

// SelectedRow returns the selected row, or nil if the table is empty.
func (t *Table) SelectedRow() []string {
	if len(t.visible) == 0 {
		return nil
	}
	return t.visible[t.cursor]
}

// Update moves the cursor on list movement keys.
func (t *Table) Update(msg tea.Msg) tea.Cmd {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	l := t.keys.List
	half := max(t.bodyHeight()/2, 1)
	switch {
	case key.Matches(kp, l.Up):
		t.SetCursor(t.cursor - 1)
	case key.Matches(kp, l.Down):
		t.SetCursor(t.cursor + 1)
	case key.Matches(kp, l.Top):
		t.SetCursor(0)
	case key.Matches(kp, l.Bottom):
		t.SetCursor(len(t.visible) - 1)
	case key.Matches(kp, l.PageDown):
		t.SetCursor(t.cursor + half)
	case key.Matches(kp, l.PageUp):
		t.SetCursor(t.cursor - half)
	}
	return nil
}

func (t *Table) bodyHeight() int { return max(t.height-1, 0) }

func (t *Table) clamp() {
	t.cursor = min(max(t.cursor, 0), max(len(t.visible)-1, 0))
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
	t.offset = min(max(t.offset, 0), max(len(t.visible)-h, 0))
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

	if len(t.rows) == 0 && t.empty != nil && t.height > 1 {
		lines = append(lines, strings.Split(t.empty(t.width, t.height-1), "\n")...)
	} else if len(t.visible) == 0 {
		empty := EmptyText
		if len(t.rows) > 0 {
			empty = "no matches for " + strconv.Quote(t.filter)
		}
		lines = append(lines, t.theme.TableEmpty.Render(fit(empty, t.width)))
	}
	end := min(t.offset+t.bodyHeight(), len(t.visible))
	for i := t.offset; i < end; i++ {
		style := t.theme.TableRow
		if i == t.cursor && t.focused {
			style = t.theme.TableSelected
		}
		lines = append(lines, style.Render(t.renderCells(t.visible[i])))
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
