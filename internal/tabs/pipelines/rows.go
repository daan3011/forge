package pipelines

import (
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/ui"
)

// cellSep splits the parts of an encoded cell. It can't be typed, so `/`
// never matches across it.
const cellSep = "\x1f"

// columns are the run table's columns. The last one, the run key, is
// hidden and finds the run again. Duration and started are stored as
// timestamps and drawn against now, so a running run's time keeps moving
// between fetches.
func (t *Tab) columns() []ui.Column {
	cols := []ui.Column{{Title: "⚙", Width: 2, Render: t.renderState}}
	if t.multi {
		cols = append(cols, ui.Column{Title: "Repo", Width: 14})
	}
	return append(cols,
		ui.Column{Title: "Workflow", Width: 12},
		ui.Column{Title: "Branch", Width: 14},
		ui.Column{Title: "Commit", Width: 12, Flex: true},
		ui.Column{Title: "Event", Width: 10},
		ui.Column{Title: "Actor", Width: 10},
		ui.Column{Title: "Duration", Width: 13, Render: t.renderDuration},
		ui.Column{Title: "Started", Width: 7, Render: t.renderStarted},
		ui.Column{Title: "key", Hidden: true},
	)
}

// runRow is a run's cells, in column order.
func (t *Tab) runRow(r github.Run) []string {
	row := []string{r.State}
	if t.multi {
		row = append(row, repoName(r.Repo))
	}
	return append(row,
		r.Workflow, r.Branch, r.Title, r.Event, r.Actor,
		durationCell(r), unix(r.CreatedAt),
		r.Key(),
	)
}

// repoName drops the owner: "acme/app" → "app".
func repoName(repo string) string {
	if _, name, ok := strings.Cut(repo, "/"); ok {
		return name
	}
	return repo
}

func unix(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return strconv.FormatInt(at.Unix(), 10)
}

func fromUnix(s string) time.Time {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n == 0 {
		return time.Time{}
	}
	return time.Unix(n, 0)
}

// durationCell encodes what Run.Duration needs: state, start, end.
func durationCell(r github.Run) string {
	start := r.StartedAt
	if start.IsZero() {
		start = r.CreatedAt
	}
	return r.State + cellSep + unix(start) + cellSep + unix(r.UpdatedAt)
}

// cellDuration decodes a duration cell into "2m14s" / "running 1m03s" / ….
func cellDuration(cell string, now time.Time) (state, text string) {
	parts := strings.Split(cell, cellSep)
	for len(parts) < 3 {
		parts = append(parts, "")
	}
	r := github.Run{State: parts[0], StartedAt: fromUnix(parts[1]), UpdatedAt: fromUnix(parts[2])}
	return r.State, ui.StateDuration(r.State, r.Duration(now))
}

func (t *Tab) renderState(cell string, width int, row lipgloss.Style) string {
	glyph, st := t.theme.CheckIcon(cell)
	return st.Inherit(row).Render(ansi.Truncate(glyph, width, "")) + row.Render(strings.Repeat(" ", max(width-1, 0)))
}

func (t *Tab) renderDuration(cell string, width int, row lipgloss.Style) string {
	state, text := cellDuration(cell, t.now())
	st := t.theme.PRTime
	if state == github.CheckRunning {
		_, st = t.theme.CheckIcon(state)
	}
	return pad(st.Inherit(row).Render(ansi.Truncate(text, width, "…")), width, row)
}

func (t *Tab) renderStarted(cell string, width int, row lipgloss.Style) string {
	text := ""
	if at := fromUnix(cell); !at.IsZero() {
		text = ui.ShortAge(t.now().Sub(at))
	}
	return pad(t.theme.PRTime.Inherit(row).Render(ansi.Truncate(text, width, "")), width, row)
}

// pad fills s out to width cells in the row's style.
func pad(s string, width int, row lipgloss.Style) string {
	return s + row.Render(strings.Repeat(" ", max(width-ansi.StringWidth(s), 0)))
}

// stateWord is a run state for people: "running", "failed", ….
func stateWord(state string) string {
	switch state {
	case github.CheckFail:
		return "failed"
	case github.CheckRunning:
		return "running"
	case github.CheckPending:
		return "queued"
	case github.CheckPass:
		return "succeeded"
	}
	return "skipped"
}
