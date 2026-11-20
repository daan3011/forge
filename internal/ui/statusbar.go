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
	// Fetching replaces "updated Ns ago" with Spinner and "refreshing".
	Fetching bool
	Spinner  string
	// Rate is the GitHub rate limit badge, set only when it runs low. It sits
	// next to the tab name and is kept when hints are dropped.
	Rate string
	// Keys are the hints shown on the right, most important last: when space
	// runs out they are dropped from the front.
	Keys []key.Binding
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
	if info.Rate != "" {
		if rate := t.StatusWarn.Render(info.Rate); lipgloss.Width(left)+lipgloss.Width(rate) <= width {
			left += rate
		}
	}

	updated := ""
	switch {
	case info.Fetching:
		updated = strings.TrimSpace(info.Spinner + " refreshing")
	case !info.Updated.IsZero():
		updated = "updated " + FormatAgo(info.Now.Sub(info.Updated))
	}
	lw := lipgloss.Width(left)
	hints := info.Keys
	rightView := s.right(updated, hints)
	for len(hints) > 0 && lw+lipgloss.Width(rightView) > width {
		hints = hints[1:]
		rightView = s.right(updated, hints)
	}
	rw := lipgloss.Width(rightView)
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

// right renders "updated Ns ago · key desc · key desc ".
func (s StatusBar) right(updated string, hints []key.Binding) string {
	t := s.theme
	var parts []string
	if updated != "" {
		parts = append(parts, t.StatusRight.Render(updated))
	}
	for _, h := range hints {
		hp := h.Help()
		if !h.Enabled() || hp.Key == "" {
			continue
		}
		parts = append(parts, t.StatusKey.Render(hp.Key)+t.StatusRight.Render(" "+hp.Desc))
	}
	if len(parts) == 0 {
		return ""
	}
	sp := t.StatusRight.Render(" ")
	return sp + strings.Join(parts, t.StatusRight.Render(" · ")) + sp
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
		if !b.Enabled() || h.Key == "" {
			continue
		}
		parts = append(parts, theme.StatusKey.UnsetBackground().Render(h.Key)+" "+theme.Hint.Render(h.Desc))
	}
	return strings.Join(parts, theme.Hint.Render(" · "))
}
