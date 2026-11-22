package ui

import (
	"math"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// eighths are bottom-aligned blocks, 1/8 to 8/8 of a cell.
var eighths = [...]rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// BarChart draws values as vertical bars in block characters, height rows
// tall (eighth blocks give the top cell its fraction), with labels under
// the bars on one more row. Bars share width evenly with a one-cell gap;
// each is coloured by the heat gradient by its share of the largest value.
// Zero values show as a dot on the baseline. Labels that don't fit their
// slot are thinned out. The result is at most width cells wide.
func BarChart(th *Theme, values []float64, labels []string, width, height int) string {
	n := len(values)
	if n == 0 || width <= 0 || height <= 0 {
		return ""
	}
	slot := max(width/n, 1)
	bar := max(slot-1, 1)
	if slot == 1 {
		bar = 1
	}
	peak := 0.0
	for _, v := range values {
		if v > peak && !math.IsInf(v, 0) {
			peak = v
		}
	}
	// cells[i] is bar i's height in eighths of a row.
	cells := make([]int, n)
	for i, v := range values {
		if peak <= 0 || v <= 0 || math.IsNaN(v) {
			continue
		}
		cells[i] = max(int(math.Round(v/peak*float64(height*8))), 1)
	}
	dim := lipgloss.NewStyle().Foreground(th.Border)
	rows := make([]string, 0, height+1)
	for r := height - 1; r >= 0; r-- {
		var b strings.Builder
		for i := range n {
			if i*slot >= width {
				break
			}
			fill := cells[i] - r*8
			var s string
			switch {
			case fill >= 8:
				s = strings.Repeat(string(eighths[7]), bar)
			case fill > 0:
				s = strings.Repeat(string(eighths[fill-1]), bar)
			case r == 0 && cells[i] == 0:
				s = dim.Render("·" + strings.Repeat(" ", bar-1))
			default:
				s = strings.Repeat(" ", bar)
			}
			if fill > 0 {
				s = lipgloss.NewStyle().Foreground(Heat(0.25 + 0.75*float64(cells[i])/float64(height*8))).Render(s)
			}
			b.WriteString(s)
			if slot > bar {
				b.WriteString(strings.Repeat(" ", slot-bar))
			}
		}
		rows = append(rows, strings.TrimRight(b.String(), " "))
	}
	rows = append(rows, th.Hint.Render(chartLabels(labels, n, slot, width)))
	return strings.Join(rows, "\n")
}

// chartLabels lays out one label per slot, skipping every k-th so each
// shown label has room.
func chartLabels(labels []string, n, slot, width int) string {
	widest := 0
	for _, l := range labels {
		widest = max(widest, ansi.StringWidth(l))
	}
	if widest == 0 {
		return ""
	}
	step := 1
	for step*slot < widest+1 && step < n {
		step++
	}
	line := []rune(strings.Repeat(" ", width))
	for i := 0; i < n && i < len(labels); i++ {
		// Thin from the right so the newest (last) label always shows.
		if (n-1-i)%step != 0 {
			continue
		}
		at, rs := i*slot, []rune(labels[i])
		if at+len(rs) > width {
			continue // would be cut off
		}
		copy(line[at:], rs)
	}
	return strings.TrimRight(string(line), " ")
}
