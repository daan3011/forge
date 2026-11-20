package prs

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/ui"
)

// Table cells of a PR row. colID and colSearch are hidden: the id finds the
// PR again, the search text lets `/` match author and branches.
const (
	colState = iota
	colTitle
	colReview
	colCI
	colDiff
	colUpdated
	colCreated
	colID
	colSearch
)

// rowHeight is the PR rows' height: repo on line 1, title on line 2.
const rowHeight = 2

func (t *Tab) columns() []ui.Column {
	icon := func(f func(string) (string, lipgloss.Style)) func(string, int, lipgloss.Style) string {
		return func(cell string, width int, row lipgloss.Style) string {
			glyph, st := f(cell)
			return st.Inherit(row).Render(ansi.Truncate(glyph, width, "")) + row.Render(strings.Repeat(" ", max(width-1, 0)))
		}
	}
	return []ui.Column{
		{Title: "⑂", Width: 2, Render: icon(t.stateIcon)},
		{Title: "Title", Width: 16, Flex: true, Render: t.renderTitle},
		{Title: "✎", Width: 2, Render: icon(t.theme.ReviewIcon)},
		{Title: "⚙", Width: 2, Render: icon(t.theme.CIIcon)},
		{Title: "±", Width: 12, Render: t.renderDiff},
		{Title: "◷", Width: 4, Render: t.renderTime},
		{Title: "✚", Width: 4, Render: t.renderTime},
		{Title: "id", Hidden: true},
		{Title: "search", Hidden: true},
	}
}

func (t *Tab) stateIcon(state string) (string, lipgloss.Style) { return t.theme.PRIcon(state) }

// renderTitle draws "owner/repo · #12" over the title.
func (t *Tab) renderTitle(cell string, width int, row lipgloss.Style) string {
	repo, title, _ := strings.Cut(cell, "\n")
	return t.theme.PRRepo.Inherit(row).Render(ansi.Truncate(repo, width, "…")) + "\n" +
		t.theme.PRTitle.Inherit(row).Render(ansi.Truncate(title, width, "…"))
}

// renderDiff colours "+391 -4".
func (t *Tab) renderDiff(cell string, width int, row lipgloss.Style) string {
	add, del, _ := strings.Cut(cell, " ")
	s := t.theme.DiffAdd.Inherit(row).Render(add) + row.Render(" ") + t.theme.DiffDel.Inherit(row).Render(del)
	return ansi.Truncate(s, width, "")
}

func (t *Tab) renderTime(cell string, width int, row lipgloss.Style) string {
	return t.theme.PRTime.Inherit(row).Render(ansi.Truncate(cell, width, ""))
}

// prRow is the table row of a PR, with ages relative to now.
func prRow(p github.PR, now time.Time) []string {
	row := make([]string, colSearch+1)
	row[colState] = p.State
	row[colTitle] = fmt.Sprintf("%s · #%d\n%s", p.Repo, p.Number, p.Title)
	row[colReview] = p.ReviewDecision
	row[colCI] = p.CI
	row[colDiff] = "+" + compact(p.Additions) + " -" + compact(p.Deletions)
	row[colUpdated] = age(now, p.UpdatedAt)
	row[colCreated] = age(now, p.CreatedAt)
	row[colID] = p.ID()
	row[colSearch] = strings.TrimSpace("@" + p.Author + " " + p.HeadRef + " " + p.BaseRef)
	return row
}

func age(now, at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return ui.ShortAge(now.Sub(at))
}

// compact writes line counts like gh-dash: 391, 1.2k, 12k, 1.5m.
func compact(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 10_000:
		return trimZero(fmt.Sprintf("%.1f", float64(n)/1000)) + "k"
	case n < 1_000_000:
		return fmt.Sprintf("%dk", n/1000)
	default:
		return trimZero(fmt.Sprintf("%.1f", float64(n)/1_000_000)) + "m"
	}
}

func trimZero(s string) string { return strings.TrimSuffix(s, ".0") }
