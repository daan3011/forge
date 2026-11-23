package context

import (
	stdctx "context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
	"github.com/daan3011/forge/internal/usage"
)

// The Usage view (P5.4): token usage from every Claude Code transcript, the
// last N days as a bar chart, a table per project and the top sessions. The
// project list on the left filters the chart and sessions on the right.

// usageTimeout bounds a scan; the first one reads every transcript.
const usageTimeout = 2 * time.Minute

// usageState is the Usage view's state.
type usageState struct {
	gen      int // bumped per load; older reports are dropped
	loading  bool
	scanning bool
	loaded   bool
	report   usage.Report
	err      error
	// filter is the selected project; "" is "All projects" (row 0).
	filter string
	cursor int
	top    int
}

type usageLoadedMsg struct {
	gen     int
	project string
	report  usage.Report
	err     error
	at      time.Time
}

// TargetTab implements tabs.TargetedMsg.
func (usageLoadedMsg) TargetTab() string { return Title }

// --- loading ---------------------------------------------------------------------

// loadUsage builds the report for the selected project, after scanning the
// transcripts for new usage when scan is set.
func (t *Tab) loadUsage(scan bool) tea.Cmd {
	if t.usage == nil {
		return nil
	}
	t.us.gen++
	t.us.loading = true
	t.us.scanning = t.us.scanning || scan
	gen, src, project, now := t.us.gen, t.usage, t.us.filter, t.now
	return func() tea.Msg {
		ctx, cancel := stdctx.WithTimeout(stdctx.Background(), usageTimeout)
		defer cancel()
		if scan {
			if _, err := src.Scan(ctx); err != nil {
				return usageLoadedMsg{gen: gen, project: project, err: fmt.Errorf("usage scan: %w", err), at: now()}
			}
		}
		r, err := src.Report(ctx, project)
		return usageLoadedMsg{gen: gen, project: project, report: r, err: err, at: now()}
	}
}

func (t *Tab) usageLoaded(msg usageLoadedMsg) tea.Cmd {
	if msg.gen != t.us.gen {
		return nil
	}
	t.us.loading, t.us.scanning = false, false
	if msg.err != nil {
		t.us.err = msg.err
		return errCmd(msg.err)
	}
	t.us.loaded, t.us.err, t.us.report = true, nil, msg.report
	t.updated = msg.at
	// The project list may have reordered; keep the selected project.
	cursor := 0
	for i, p := range t.us.report.Projects {
		if p.Project == t.us.filter {
			cursor = i + 1
		}
	}
	if cursor == 0 {
		t.us.filter = ""
	}
	t.setUsageCursor(cursor)
	return nil
}

// toggleUsage switches between memory files and the Usage view.
func (t *Tab) toggleUsage() tea.Cmd {
	if t.view == viewUsage {
		t.view = viewMemory
		return nil
	}
	t.view = viewUsage
	t.focusDetail, t.matrix = false, false
	return t.loadUsage(true)
}

// --- selection -------------------------------------------------------------------

// usageRows is the project list's length: "All projects" plus each project.
func (t *Tab) usageRows() int { return len(t.us.report.Projects) + 1 }

func (t *Tab) usageListHeight() int {
	_, h := t.split.LeftSize()
	return max(h-footerLines-1, 1) // the header row
}

func (t *Tab) setUsageCursor(i int) {
	t.us.cursor, t.us.top = scrollTo(i, t.us.top, t.usageRows(), t.usageListHeight())
}

// moveUsage moves the cursor and reloads the report for the new project.
func (t *Tab) moveUsage(d int) tea.Cmd {
	before := t.us.cursor
	t.setUsageCursor(t.us.cursor + d)
	if t.us.cursor == before {
		return nil
	}
	t.us.filter = ""
	if t.us.cursor > 0 {
		t.us.filter = t.us.report.Projects[t.us.cursor-1].Project
	}
	return t.loadUsage(false)
}

func (t *Tab) usageKey(kp tea.KeyPressMsg) tea.Cmd {
	l := t.keys.List
	half := max(t.usageListHeight()/2, 1)
	switch {
	case key.Matches(kp, l.GrowPane):
		t.split.Grow()
	case key.Matches(kp, l.ShrinkPane):
		t.split.Shrink()
	case key.Matches(kp, l.Up):
		return t.moveUsage(-1)
	case key.Matches(kp, l.Down):
		return t.moveUsage(1)
	case key.Matches(kp, l.Top):
		return t.moveUsage(-1 << 20)
	case key.Matches(kp, l.Bottom):
		return t.moveUsage(1 << 20)
	case key.Matches(kp, l.PageDown):
		return t.moveUsage(half)
	case key.Matches(kp, l.PageUp):
		return t.moveUsage(-half)
	}
	return nil
}

// --- view ------------------------------------------------------------------------

// Column widths of the usage tables.
const (
	colTokens = 8
	colCost   = 10
)

// costText is a cost cell: "—" when nothing is priced, a "*" when only
// some tokens are.
func costText(x usage.Totals) string {
	switch {
	case !x.Priced():
		return "—"
	case x.Partial():
		return ui.USD(x.Cost) + "*"
	}
	return ui.USD(x.Cost)
}

func tokens(n int64) string { return ui.CompactCount(int(n)) }

// usageRow lays out label | tokens | cost in w columns.
func usageRow(label, tok, cost string, w int) string {
	right := fmt.Sprintf("%*s%*s", colTokens, tok, colCost, cost)
	space := w - ansi.StringWidth(right)
	if space < 6 {
		return ansi.Truncate(label, w, "…")
	}
	label = ansi.Truncate(label, space, "…")
	return label + strings.Repeat(" ", max(space-ansi.StringWidth(label), 0)) + right
}

func (t *Tab) usageListView(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	th := t.theme
	switch {
	case t.usage == nil:
		return th.Hint.Render(ansi.Wrap("Token usage needs forge's database; it isn't open in this run.", w, ""))
	case !t.us.loaded && t.us.err != nil && !t.us.loading:
		return anim.Failed("couldn't read usage: "+t.us.err.Error(), ui.RetryKey(t.keys), th, w, h)
	case !t.us.loaded:
		return t.loadingBody("reading Claude Code transcripts…", w, h)
	}
	r := t.us.report
	header := usageRow("  Project", "Tokens", "Cost", w)
	var lines []string
	end := min(t.us.top+t.usageListHeight(), t.usageRows())
	for i := t.us.top; i < end; i++ {
		label, x := "  All projects", t.allTotals()
		if i > 0 {
			p := r.Projects[i-1]
			label, x = "  "+p.Project, p.Totals
		}
		line := usageRow(label, tokens(x.Total()), costText(x), w)
		switch {
		case i == t.us.cursor:
			lines = append(lines, th.TableSelected.Render(line))
		case x.Total() == 0:
			lines = append(lines, th.Hint.Render(line))
		default:
			lines = append(lines, th.TableRow.Render(line))
		}
	}
	body := t.usageListHeight()
	for len(lines) < body {
		lines = append(lines, "")
	}
	return th.DetailLabel.Render(header) + "\n" + strings.Join(lines[:body], "\n") + "\n\n" + t.usageFooter(w)
}

// allTotals sums every project.
func (t *Tab) allTotals() usage.Totals {
	var x usage.Totals
	for _, p := range t.us.report.Projects {
		x.Input += p.Input
		x.Output += p.Output
		x.CacheRead += p.CacheRead
		x.CacheWrite += p.CacheWrite
		x.Messages += p.Messages
		x.Cost += p.Cost
		x.Unpriced += p.Unpriced
	}
	return x
}

// usageFooter is the range and scan state under the list.
func (t *Tab) usageFooter(w int) string {
	r := t.us.report
	text := fmt.Sprintf("last %d days since %s", len(r.Days), r.Since.Format("Jan 2"))
	if t.us.scanning {
		text += " · scanning…"
	}
	return t.theme.Hint.Render(ansi.Truncate(text, w, "…"))
}

// chartHeight is the bar chart's height in rows for a pane h rows tall.
func chartHeight(h int) int { return min(max(h/4, 3), 8) }

func (t *Tab) usageDetailView(w, h int) string {
	if w <= 0 || h <= 0 || !t.us.loaded {
		return ""
	}
	th := t.theme
	r := t.us.report
	title := "All projects"
	if r.Project != "" {
		title = r.Project
	}
	x := r.Total
	sum := fmt.Sprintf("%s tokens · %s · %d messages", tokens(x.Total()), costText(x), x.Messages)
	split := fmt.Sprintf("in %s · out %s · cache read %s · cache write %s",
		tokens(x.Input), tokens(x.Output), tokens(x.CacheRead), tokens(x.CacheWrite))
	out := []string{
		th.DetailTitle.Render(ansi.Truncate(title, w, "…")),
		th.Body.Render(ansi.Truncate(sum, w, "…")),
		th.Hint.Render(ansi.Truncate(split, w, "…")),
		"",
	}
	// The chart.
	values := make([]float64, len(r.Days))
	labels := make([]string, len(r.Days))
	var peak usage.Day
	for i, d := range r.Days {
		values[i] = float64(d.Total())
		labels[i] = d.Day.Format("02")
		if d.Total() > peak.Total() {
			peak = d
		}
	}
	chartHead := fmt.Sprintf("Tokens per day, last %d days", len(r.Days))
	if peak.Total() > 0 {
		chartHead += fmt.Sprintf(" · peak %s on %s", tokens(peak.Total()), peak.Day.Format("Mon Jan 2"))
	}
	out = append(out, th.DetailLabel.Render(ansi.Truncate(chartHead, w, "…")))
	out = append(out, strings.Split(ui.BarChart(th, values, labels, w, chartHeight(h)), "\n")...)
	out = append(out, "", th.DetailLabel.Render(usageRow("Top sessions", "Tokens", "Cost", w)))
	if len(r.Sessions) == 0 {
		out = append(out, th.Hint.Render("no sessions in this range"))
	}
	for _, s := range r.Sessions {
		label := s.Label()
		if r.Project == "" && !strings.HasSuffix(label, " · "+s.Project) {
			label += th.Hint.Render(" · " + s.Project)
		}
		out = append(out, th.TableRow.Render(usageRow(label, tokens(s.Total()), costText(s.Totals), w)))
	}
	if r.Unpriced() {
		out = append(out, "", th.Hint.Render(ansi.Wrap(
			"— no price for some models; * partly priced. Fill in usage.prices in the config for cost estimates.", w, "")))
	}
	return strings.Join(out[:min(len(out), h)], "\n")
}

// usageStatus is the status bar detail in the Usage view.
func (t *Tab) usageStatus() string {
	if !t.us.loaded {
		return ""
	}
	x := t.allTotals()
	s := fmt.Sprintf("%dd: %s tokens", len(t.us.report.Days), tokens(x.Total()))
	if x.Priced() {
		s += " · " + costText(x)
	}
	return s
}
