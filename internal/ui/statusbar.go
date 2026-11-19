package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
)

// StatusBar is the one-line bar at the bottom of the screen.
type StatusBar struct {
	theme *Theme
}

// StatusInfo is what the status bar shows.
type StatusInfo struct {
	Tab     string
	Project string    // empty means all projects
	Message string    // latest info message
	Updated time.Time // zero hides "updated Ns ago"
	Now     time.Time
}

// NewStatusBar returns a status bar.
func NewStatusBar(theme *Theme) StatusBar { return StatusBar{theme: theme} }

// View renders the bar at exactly width cells. The message shrinks first,
// then the right side, then the left.
func (s StatusBar) View(width int, info StatusInfo) string {
	if width <= 0 {
		return ""
	}
	t := s.theme
	project := info.Project
	if project == "" {
		project = "all projects"
	}

	left := t.StatusLeft.Render(info.Tab + " · " + project)

	updated := ""
	if !info.Updated.IsZero() {
		updated = "updated " + FormatAgo(info.Now.Sub(info.Updated)) + " · "
	}
	help := Keys.Help.Help()
	rightView := t.StatusRight.Render(" "+updated) + t.StatusKey.Render(help.Key) + t.StatusRight.Render(" "+help.Desc+" ")

	lw, rw := lipgloss.Width(left), lipgloss.Width(rightView)
	if lw+rw > width {
		rightView = ""
		rw = 0
	}
	if lw > width {
		return t.StatusLeft.Render(fit(info.Tab, max(width-2, 0)))
	}
	midW := width - lw - rw
	mid := ""
	if midW > 0 {
		inner := max(midW-2, 0)
		mid = t.StatusMid.Render(fit(info.Message, inner))
		if lipgloss.Width(mid) != midW { // too narrow for padding
			mid = t.StatusMid.UnsetPadding().Render(strings.Repeat(" ", midW))
		}
	}
	return left + mid + rightView
}

// FormatAgo formats a duration as a short "time ago" string.
func FormatAgo(d time.Duration) string {
	switch {
	case d < 5*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// HelpLine renders bindings as "key desc · key desc" in hint style.
func HelpLine(theme *Theme, bindings []key.Binding) string {
	parts := make([]string, 0, len(bindings))
	for _, b := range bindings {
		h := b.Help()
		if h.Key == "" {
			continue
		}
		parts = append(parts, theme.StatusKey.UnsetBackground().Render(h.Key)+" "+theme.Hint.Render(h.Desc))
	}
	return strings.Join(parts, theme.Hint.Render(" · "))
}
