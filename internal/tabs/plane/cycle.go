package plane

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
)

// cycleBarWidth is the cycle summary's progress bar, in cells.
const cycleBarWidth = 12

// cycleCounts are a cycle's items by bucket. Blocked open items count as
// blocked only; cancelled ones are left out of the total.
type cycleCounts struct {
	done, started, blocked, todo, cancelled int
}

func (c cycleCounts) total() int { return c.done + c.started + c.blocked + c.todo }

// pct is the share of the (not cancelled) items that are done.
func (c cycleCounts) pct() float64 {
	if c.total() == 0 {
		return 0
	}
	return float64(c.done) / float64(c.total())
}

// countCycle buckets items: completed → done, cancelled → cancelled, else
// blocked when isBlocked says so, else started → in progress, else todo
// (triage, backlog, unstarted and unknown states).
func countCycle(
	items []plane.WorkItem,
	stateOf func(plane.WorkItem) (string, plane.StateGroup),
	labelsOf func(plane.WorkItem) []string,
	isBlocked func(labels []string, state string) bool,
) cycleCounts {
	var c cycleCounts
	for _, w := range items {
		name, group := stateOf(w)
		switch {
		case group == plane.GroupCompleted:
			c.done++
		case group == plane.GroupCancelled:
			c.cancelled++
		case isBlocked(labelsOf(w), name):
			c.blocked++
		case group == plane.GroupStarted:
			c.started++
		default:
			c.todo++
		}
	}
	return c
}

// daysLeft is the number of calendar days from now to the cycle's last
// day: 0 on the last day. A date-only end date is that day; a date-time
// one is its day in now's time zone.
func daysLeft(c plane.Cycle, now time.Time) int {
	end := c.EndDate.Time
	if end.Hour() != 0 || end.Minute() != 0 || end.Second() != 0 || end.Nanosecond() != 0 {
		end = end.In(now.Location())
	}
	ey, em, ed := end.Date()
	ny, nm, nd := now.Date()
	diff := time.Date(ey, em, ed, 0, 0, 0, 0, time.UTC).Sub(time.Date(ny, nm, nd, 0, 0, 0, 0, time.UTC))
	return int(diff.Hours() / 24)
}

// daysLeftLabel is "ends today", "1 day left", "4 days left" or "ended".
func daysLeftLabel(n int) string {
	switch {
	case n < 0:
		return "ended"
	case n == 0:
		return "ends today"
	case n == 1:
		return "1 day left"
	}
	return strconv.Itoa(n) + " days left"
}

// --- current cycle & its items ----------------------------------------------------

// currentCycle is the running cycle of the one listed project, once its
// details have loaded.
func (t *Tab) currentCycle() (plane.Cycle, string, bool) {
	if !t.single() {
		return plane.Cycle{}, "", false
	}
	id := t.lists[0].id
	m := t.meta[id]
	if m == nil {
		return plane.Cycle{}, "", false
	}
	n := t.now()
	for _, c := range m.cycles {
		if c.IsCurrent(n) {
			return c, id, true
		}
	}
	return plane.Cycle{}, "", false
}

// cycleFilter asks for every item of cycle id, whatever the tab's filters.
// It has no order, which keeps its cache entry apart from the lists'.
func cycleFilter(id string) plane.WorkItemFilter {
	return plane.WorkItemFilter{CycleID: id, Expand: listExpand, Limit: listLimit}
}

// ensureCycle points t.cyc at the current cycle's items and loads them:
// the cached copy once, then a fetch (always when force, else only until
// one has landed).
func (t *Tab) ensureCycle(force bool) tea.Cmd {
	c, pid, ok := t.currentCycle()
	if !ok || t.api == nil {
		t.cyc = nil
		return nil
	}
	f := cycleFilter(c.ID)
	k := listKey(pid, f) + "|cycle"
	if t.cyc == nil || t.cyc.key != k {
		t.cyc = &projectItems{id: pid, key: k, filter: f}
	}
	l := t.cyc
	var cmds []tea.Cmd
	if !l.cacheAsked {
		l.cacheAsked = true
		cmds = append(cmds, loadCached(t.api, l.key, l.id, f))
	}
	if !l.fetching && (force || !l.fresh) {
		l.fetching = true
		cmds = append(cmds, fetchItems(t.api, l.key, l.id, f, t.now))
	}
	return tea.Batch(cmds...)
}

// cycleLoaded takes the cycle items from the cache or a fetch.
func (t *Tab) cycleLoaded(msg itemsMsg) tea.Cmd {
	l := t.cyc
	if msg.cached {
		if !l.fresh {
			l.items, l.loaded, l.updated = msg.items, true, msg.at
			t.reapplyMoves(l)
		}
		return nil
	}
	l.fetching = false
	if msg.err != nil {
		return t.toastOnce(fmt.Errorf("cycle: %w", msg.err))
	}
	l.items, l.loaded, l.fresh, l.updated = msg.items, true, true, msg.at
	t.reapplyMoves(l)
	return nil
}

// cycleShown reports whether the summary line is shown.
func (t *Tab) cycleShown() bool {
	_, _, ok := t.currentCycle()
	return ok
}

// headerHeight is the filter box plus the cycle line when shown.
func (t *Tab) headerHeight() int {
	if t.cycleShown() {
		return filterBoxHeight + 1
	}
	return filterBoxHeight
}

// toggleCycle filters to the current cycle, or back to every cycle.
func (t *Tab) toggleCycle() tea.Cmd {
	c, _, ok := t.currentCycle()
	if !ok {
		text := "no current cycle"
		if !t.single() {
			text += " (pick one project with " + t.keys.Plane.Filters.Help().Key + ")"
		}
		return func() tea.Msg { return ui.InfoMsg{Text: text} }
	}
	f := t.filt
	if f.cycle == c.ID {
		f.cycle = ""
	} else {
		f.cycle = c.ID
	}
	return t.setFilters(f)
}

// cycleLine is the current cycle's summary: name, days left, progress and
// counts, e.g. "◷ Sprint 12 · 3 days left ████░░ 40%  done 2 · …". When
// width is short it drops, in turn, the key hint, the cancelled count and
// some of the bar, then shortens the count labels.
func (t *Tab) cycleLine(width int) string {
	c, _, ok := t.currentCycle()
	if !ok {
		return ""
	}
	th := t.theme
	sep := th.SectionSep.Render(" · ")
	head := th.CycleName.Render("◷ "+c.Name) + sep + th.Body.Render(daysLeftLabel(daysLeft(c, t.now())))
	hintText := "cycle"
	if t.filt.cycle == c.ID {
		hintText = "all cycles"
	}
	hint := th.Hint.Render(t.keys.Plane.Cycle.Help().Key + " " + hintText)
	line := func(parts ...string) string { return " " + strings.Join(parts, "  ") }

	if t.cyc == nil || !t.cyc.loaded {
		return ui.Box(line(head, th.Hint.Render("counting…"), hint), max(width, 1), 1)
	}
	n := countCycle(t.cyc.items, t.stateOf, t.labelNames, t.cfg.Plane.IsBlocked)
	blockedStyle := th.Hint
	if n.blocked > 0 {
		blockedStyle = th.CycleBlocked
	}
	counts := func(short, cancelled bool) string {
		labels := [4]string{"done", "in progress", "blocked", "todo"}
		if short {
			labels = [4]string{"✓", "▸", "⊘", "○"}
		}
		styles := [4]lipgloss.Style{th.LinkState("done"), th.LinkState("in progress"), blockedStyle, th.LinkState("todo")}
		vals := [4]int{n.done, n.started, n.blocked, n.todo}
		out := make([]string, 0, 5)
		for i := range labels {
			gap := " "
			if short {
				gap = ""
			}
			out = append(out, styles[i].Render(labels[i])+th.Hint.Render(gap+strconv.Itoa(vals[i])))
		}
		if cancelled && n.cancelled > 0 {
			out = append(out, th.Hint.Render(strconv.Itoa(n.cancelled)+" cancelled"))
		}
		if short {
			return strings.Join(out, " ")
		}
		return strings.Join(out, sep)
	}
	bar := func(w int) string { return anim.HeatBarLabel(n.pct(), w+5, th) }
	variants := []string{
		line(head, bar(cycleBarWidth), counts(false, true), hint),
		line(head, bar(cycleBarWidth), counts(false, true)),
		line(head, bar(cycleBarWidth/2), counts(false, false)),
		line(head, bar(cycleBarWidth/2), counts(true, false)),
	}
	for _, v := range variants {
		if lipgloss.Width(v) <= width {
			return v
		}
	}
	return ui.Box(variants[len(variants)-1], max(width, 1), 1)
}
