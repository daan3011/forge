package ui

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// TabBarHeight is the number of rows the tab bar takes (tabs + rule).
const TabBarHeight = 2

// TabBar renders the "forge" wordmark and numbered tabs, followed by a rule,
// at exactly width cells. When the tabs don't fit, inactive tabs show only
// their number, then the wordmark is dropped.
func TabBar(theme *Theme, titles []string, active, width int) string {
	if width <= 0 {
		return ""
	}
	render := func(compact, wordmark bool) string {
		var b strings.Builder
		if wordmark {
			b.WriteString(theme.Wordmark.Render("forge"))
		}
		for i, title := range titles {
			label := strconv.Itoa(i+1) + " " + title
			if i == active {
				b.WriteString(theme.TabActive.Render(label))
				continue
			}
			if compact {
				label = strconv.Itoa(i + 1)
			}
			b.WriteString(theme.TabInactive.Render(label))
		}
		return b.String()
	}

	bar := render(false, true)
	if lipgloss.Width(bar) > width {
		bar = render(true, true)
	}
	if lipgloss.Width(bar) > width {
		bar = render(true, false)
	}
	bar = fit(bar, width)
	rule := theme.TabRule.Render(strings.Repeat("─", width))
	return bar + "\n" + rule
}
