package ui

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// SectionBar renders a tab's sub-tabs with counts at exactly width cells:
// "⌕ │ My PRs (3) │ Review requested (0) │ Involved (2)", the active one
// highlighted. A negative count shows no number (not loaded yet). When it
// doesn't fit, inactive sections show only their count, then the active
// one does too.
func SectionBar(theme *Theme, titles []string, counts []int, active, width int) string {
	if width <= 0 {
		return ""
	}
	sep := theme.SectionSep.Render("│")
	label := func(i int, short bool) string {
		n := ""
		if i < len(counts) && counts[i] >= 0 {
			n = "(" + strconv.Itoa(counts[i]) + ")"
		}
		if short && n != "" {
			return n
		}
		if short {
			return strconv.Itoa(i + 1)
		}
		return strings.TrimSpace(titles[i] + " " + n)
	}
	render := func(compactInactive, compactActive bool) string {
		parts := []string{theme.SectionSep.Render(" ⌕ ")}
		for i := range titles {
			if i > 0 {
				parts = append(parts, sep)
			}
			if i == active {
				parts = append(parts, theme.SectionActive.Render(label(i, compactActive)))
			} else {
				parts = append(parts, theme.SectionInactive.Render(label(i, compactInactive)))
			}
		}
		return strings.Join(parts, "")
	}
	bar := render(false, false)
	if lipgloss.Width(bar) > width {
		bar = render(true, false)
	}
	if lipgloss.Width(bar) > width {
		bar = render(true, true)
	}
	return fit(bar, width)
}
