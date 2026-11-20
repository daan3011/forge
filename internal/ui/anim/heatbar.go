package anim

import (
	"fmt"
	"math"

	"github.com/daan3011/forge/internal/ui"
)

// partials are left-aligned eighth blocks, 1/8 to 7/8 of a cell.
var partials = [...]rune{'▏', '▎', '▍', '▌', '▋', '▊', '▉'}

// HeatBar renders a progress bar width cells wide. The filled part samples
// the heat gradient by position, so the bar runs dark red → orange → yellow
// → white-hot as it fills. pct is clamped to 0..1. It never animates.
func HeatBar(pct float64, width int, th *ui.Theme) string {
	if width <= 0 {
		return ""
	}
	if math.IsNaN(pct) {
		pct = 0
	}
	pct = math.Min(math.Max(pct, 0), 1)
	eighths := int(math.Round(pct * float64(width*8)))
	full, part := eighths/8, eighths%8

	g := newGrid(width, 1)
	heat := func(i int) float64 {
		if width == 1 {
			return 1
		}
		return float64(i) / float64(width-1)
	}
	for i := range width {
		switch {
		case i < full:
			g.set(i, 0, '█', ui.Heat(heat(i)))
		case i == full && part > 0:
			g.set(i, 0, partials[part-1], ui.Heat(heat(i)))
		default:
			g.set(i, 0, '░', th.Border)
		}
	}
	return g.String()
}

// HeatBarLabel is HeatBar followed by the percentage, e.g. "███░░  60%".
func HeatBarLabel(pct float64, width int, th *ui.Theme) string {
	if math.IsNaN(pct) {
		pct = 0
	}
	label := fmt.Sprintf(" %3d%%", int(math.Round(math.Min(math.Max(pct, 0), 1)*100)))
	return HeatBar(pct, max(width-len(label), 1), th) + th.Hint.Render(label)
}
