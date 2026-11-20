package prs

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/ui"
)

// detail is the right pane: header, tab strip and the active tab, scrolled.
func (t *Tab) detail() string {
	th := t.theme
	w, h := t.split.RightSize()
	if w <= 0 || h <= 0 {
		return ""
	}
	p, ok := t.selected()
	if !ok {
		return th.Hint.Render("no PR selected")
	}
	e, fresh := t.det.entry(p)
	head := t.detailHeader(p, w)
	var content []string
	switch t.det.tab {
	case tabChecks:
		content = t.checksView(p, e, w)
	case tabActivity:
		content = t.activityView(p, e, w)
	default:
		content = t.overviewView(p, e, w)
	}
	if e != nil && !fresh {
		content = append([]string{th.Hint.Render("updating…"), ""}, content...)
	}

	avail := max(h-len(head)-2, 1)
	maxOff := max(len(content)-avail, 0)
	t.det.offset = min(t.det.offset, maxOff)
	pos := ""
	if maxOff > 0 {
		pos = fmt.Sprintf("%d/%d", min(t.det.offset+avail, len(content)), len(content))
	}
	head = append(head, t.detailStrip(e, w, pos), th.DetailRule.Render(strings.Repeat("─", w)))
	end := min(t.det.offset+avail, len(content))
	head = append(head, content[t.det.offset:end]...)
	return strings.Join(head, "\n")
}

// detailHeader is repo · #n, the title on a band, the state pill with
// base ← head, and author and ages. It needs only the list row.
func (t *Tab) detailHeader(p github.PR, w int) []string {
	th := t.theme
	lines := []string{th.PRRepo.Render(fmt.Sprintf("%s · #%d", p.Repo, p.Number))}
	for _, l := range strings.Split(ansi.Wrap(p.Title, max(w-2, 1), ""), "\n") {
		lines = append(lines, th.DetailBand.Render(" "+l+strings.Repeat(" ", max(w-2-ansi.StringWidth(l), 0))+" "))
	}
	icon, _ := th.PRIcon(p.State)
	pill := th.StatePill(p.State).Render(icon + " " + stateWord(p.State))
	now := t.now()
	author := "ghost"
	if p.Author != "" {
		author = "@" + p.Author
	}
	by := th.Hint.Render("by ") + th.Body.Bold(true).Render(author)
	if a := age(now, p.CreatedAt); a != "" {
		by += th.Hint.Render(" · " + a + " ago")
	}
	if a := age(now, p.UpdatedAt); a != "" {
		by += th.Hint.Render(" · updated " + a + " ago")
	}
	lines = append(lines, "")
	lines = append(lines, strings.Split(ansi.Wrap(pill+" "+th.Hint.Render(p.BaseRef+" ← "+p.HeadRef), max(w, 1), ""), "\n")...)
	by += "  " + th.DiffAdd.Render("+"+compact(p.Additions)) + " " + th.DiffDel.Render("-"+compact(p.Deletions))
	lines = append(lines, strings.Split(ansi.Wrap(by, max(w, 1), ""), "\n")...)
	return append(lines, "")
}

func stateWord(s string) string {
	if s == "" {
		return "Open"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// detailStrip is "Overview  Checks 2✗  Activity 5", with pos at the right.
func (t *Tab) detailStrip(e *detailEntry, w int, pos string) string {
	th := t.theme
	var parts []string
	for tab := range numDetailTabs {
		label := tab.String()
		if e != nil {
			switch tab {
			case tabChecks:
				if n := countChecks(e.d.Checks, github.CheckFail); n > 0 {
					label += fmt.Sprintf(" %d✗", n)
				} else if len(e.d.Checks) > 0 {
					label += fmt.Sprintf(" %d", len(e.d.Checks))
				}
			case tabActivity:
				if len(e.d.Activity) > 0 {
					label += fmt.Sprintf(" %d", len(e.d.Activity))
				}
			}
		}
		st := th.DetailTab
		if tab == t.det.tab {
			st = th.DetailTabActive
		}
		parts = append(parts, st.Render(label))
	}
	strip := strings.Join(parts, " ")
	if pos != "" {
		gap := w - lipgloss.Width(strip) - len(pos)
		if gap >= 1 {
			strip += strings.Repeat(" ", gap) + th.Hint.Render(pos)
		}
	}
	return strip
}

// pending is the line shown where details aren't there yet.
func (t *Tab) pending(p github.PR) string {
	if err := t.failedFor(p); err != nil {
		msg := "couldn't load details: " + err.Error()
		if github.IsAuthError(err) {
			msg = "not logged in to GitHub: run `gh auth login`"
		}
		return t.theme.DiffDel.Render(msg)
	}
	if t.gh == nil {
		return t.theme.Hint.Render("GitHub isn't available")
	}
	return t.theme.TableEmpty.Render("heating up…")
}

// --- overview ------------------------------------------------------------------

// card is one entry of the status card: an icon line and a hint line.
type card struct {
	icon  string
	style lipgloss.Style
	title string
	sub   string
}

func (t *Tab) overviewView(p github.PR, e *detailEntry, w int) []string {
	th := t.theme
	lines := t.statusCards(p, e, w)

	section := func(title string, body ...string) {
		lines = append(lines, "", th.DetailLabel.Bold(true).Render(title))
		lines = append(lines, body...)
	}
	if e == nil {
		lines = append(lines, "", t.pending(p))
	} else {
		if len(e.d.Labels) > 0 {
			chips := make([]string, len(e.d.Labels))
			for i, l := range e.d.Labels {
				chips[i] = th.LabelChip(l.Color).Render(l.Name)
			}
			section("Labels", strings.Split(ansi.Wrap(strings.Join(chips, " "), w, ""), "\n")...)
		}
		section("Reviewers", t.reviewerLines(e.d.Reviewers)...)
	}
	if t.sessions != nil {
		section("Sessions", t.sessionLines(p)...)
	}
	if e != nil {
		section("Description", t.markdown(e.body, e.d.Body, e.mdWidth == w, w, "no description")...)
	}
	return lines
}

// statusCards is the bordered box with review, checks and merge state. It
// starts from the list row, so it shows before the details load; the
// details, when there, are fresher.
func (t *Tab) statusCards(p github.PR, e *detailEntry, w int) []string {
	th := t.theme
	decision, mergeable, mergeState := p.ReviewDecision, p.Mergeable, ""
	var d *github.PRDetail
	if e != nil {
		d = &e.d
		decision, mergeState = d.ReviewDecision, d.MergeState
		if d.Mergeable != "" {
			mergeable = d.Mergeable
		}
	}
	cards := []card{reviewCard(th, decision, d), checksCard(th, p.CI, d), mergeCard(th, p, mergeable, mergeState)}

	inner := max(w-4, 1) // border and padding
	var body []string
	for i, c := range cards {
		if i > 0 {
			body = append(body, th.DetailRule.Render(strings.Repeat("─", inner)))
		}
		body = append(body, wrapBox(c.style.Render(c.icon)+" "+th.CardTitle.Render(c.title), inner, "  ")...)
		body = append(body, wrapBox("  "+th.Hint.Render(c.sub), inner, "  ")...)
	}
	return strings.Split(th.StatusCard.Render(strings.Join(body, "\n")), "\n")
}

// wrapBox wraps a styled line to width, indenting continuation lines,
// and pads every line to exactly width.
func wrapBox(line string, width int, indent string) []string {
	wrapped := strings.Split(ansi.Wrap(line, width, ""), "\n")
	for i := 1; i < len(wrapped); i++ {
		wrapped[i] = ansi.Wrap(indent+wrapped[i], width, "")
	}
	out := strings.Split(strings.Join(wrapped, "\n"), "\n")
	for i, l := range out {
		out[i] = ui.Box(l, width, 1)
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func reviewCard(th *ui.Theme, decision string, d *github.PRDetail) card {
	icon, st := th.ReviewIcon(decision)
	c := card{icon: icon, style: st}
	approvals, pending, blockers := -1, 0, []string(nil)
	if d != nil {
		approvals = d.Approvals
		for _, r := range d.Reviewers {
			switch r.State {
			case github.ReviewStatePending:
				pending++
			case github.ReviewStateChangesRequested:
				blockers = append(blockers, "@"+r.Login)
			}
		}
	}
	switch decision {
	case github.ReviewApproved:
		c.title, c.sub = "Changes approved", "approved by a reviewer"
		if approvals >= 0 {
			c.sub = plural(approvals, "approving review", "approving reviews")
		}
	case github.ReviewChangesRequested:
		c.title, c.sub = "Changes requested", "a reviewer asked for changes"
		if len(blockers) > 0 {
			c.sub = "by " + strings.Join(blockers, ", ")
		}
	case github.ReviewRequired:
		c.title, c.sub = "Review required", "an approving review is needed to merge"
		if pending > 0 {
			c.sub = plural(pending, "reviewer", "reviewers") + " requested"
		}
	default:
		c.title, c.sub = "No review required", "reviews are optional here"
		if approvals > 0 {
			c.sub = plural(approvals, "approving review", "approving reviews")
		}
	}
	return c
}

func checksCard(th *ui.Theme, rollup string, d *github.PRDetail) card {
	icon, st := th.CIIcon(rollup)
	c := card{icon: icon, style: st, title: ciTitle(rollup), sub: "no checks reported"}
	if rollup != "" {
		c.sub = "details load with the PR"
	}
	if d == nil {
		return c
	}
	if len(d.Checks) == 0 {
		if rollup == "" {
			c.sub = "no checks reported"
		} else {
			c.sub = "no check details reported"
		}
		return c
	}
	var parts []string
	for _, s := range []struct{ state, word string }{
		{github.CheckFail, "failing"},
		{github.CheckRunning, "running"},
		{github.CheckPending, "queued"},
		{github.CheckPass, "successful"},
		{github.CheckSkipped, "skipped"},
	} {
		if n := countChecks(d.Checks, s.state); n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s.word))
		}
	}
	c.sub = strings.Join(parts, ", ")
	return c
}

func ciTitle(s string) string {
	switch s {
	case github.CISuccess:
		return "All checks have passed"
	case github.CIFailure, github.CIError:
		return "Some checks failed"
	case github.CIPending, github.CIExpected:
		return "Checks are running"
	}
	return "No checks"
}

func mergeCard(th *ui.Theme, p github.PR, mergeable, mergeState string) card {
	switch p.State {
	case github.StateMerged:
		icon, st := th.PRIcon(p.State)
		return card{icon, st, "Merged", "into " + p.BaseRef}
	case github.StateClosed:
		icon, st := th.PRIcon(p.State)
		return card{icon, st, "Closed", "closed without merging"}
	}
	switch mergeable {
	case github.Conflicting:
		icon, st := th.CIIcon(github.CIFailure)
		return card{icon, st, "This branch has conflicts", "resolve them with " + p.BaseRef + " before merging"}
	case github.Mergeable:
		icon, st := th.CIIcon(github.CISuccess)
		sub := "changes can be cleanly merged"
		switch mergeState {
		case github.MergeStateBehind:
			sub = "but the branch is behind " + p.BaseRef + ": update it"
		case github.MergeStateBlocked:
			sub = "but merging is blocked by branch protection"
		case github.MergeStateUnstable:
			sub = "but some checks are failing"
		}
		return card{icon, st, "No conflicts with base branch", sub}
	}
	icon, st := th.CIIcon("")
	return card{icon, st, "Mergeability unknown", "GitHub is still checking"}
}

func countChecks(cs []github.Check, state string) int {
	n := 0
	for _, c := range cs {
		if c.State == state {
			n++
		}
	}
	return n
}

func (t *Tab) reviewerLines(rs []github.Reviewer) []string {
	th := t.theme
	if len(rs) == 0 {
		return []string{th.Hint.Render("none")}
	}
	out := make([]string, len(rs))
	for i, r := range rs {
		icon, st := th.ReviewStateIcon(r.State)
		name := "ghost"
		if r.Login != "" {
			name = "@" + r.Login
		}
		out[i] = st.Render(icon) + " " + th.Body.Render(name) + " " + th.Hint.Render(reviewWord(r.State))
	}
	return out
}

func reviewWord(state string) string {
	switch state {
	case github.ReviewStateApproved:
		return "approved"
	case github.ReviewStateChangesRequested:
		return "requested changes"
	case github.ReviewStateCommented:
		return "commented"
	case github.ReviewStateDismissed:
		return "dismissed"
	case github.ReviewStatePending:
		return "review requested"
	}
	return strings.ToLower(state)
}

// sessionLines lists the forge sessions linked to the PR (P1.9).
func (t *Tab) sessionLines(p github.PR) []string {
	th := t.theme
	ss, loaded, err := t.linkedFor(p.ID())
	switch {
	case err != nil:
		return []string{th.DiffDel.Render("couldn't load linked sessions: " + err.Error())}
	case !loaded:
		return []string{th.Hint.Render("…")}
	case len(ss) == 0:
		return []string{th.Hint.Render("none")}
	}
	now := t.now()
	out := make([]string, 0, len(ss))
	for _, s := range sortedSessions(ss) {
		status := string(s.Status)
		if s.Archived() {
			status = string(store.StatusArchived)
		}
		line := th.SessionStatus(status).Render("▸ ") + th.Body.Render(s.Name) + " " + th.SessionStatus(status).Render(status)
		if a := age(now, s.CreatedAt); a != "" {
			line += th.Hint.Render(" · " + a + " ago")
		}
		out = append(out, line)
	}
	return out
}

// markdown is rendered Markdown when ready, else the plain text wrapped.
func (t *Tab) markdown(rendered, plain string, ready bool, w int, empty string) []string {
	if strings.TrimSpace(plain) == "" {
		return []string{t.theme.Hint.Render(empty)}
	}
	if ready && rendered != "" {
		return strings.Split(rendered, "\n")
	}
	return strings.Split(t.theme.Body.Render(ansi.Wrap(strings.ReplaceAll(plain, "\r\n", "\n"), max(w, 1), "")), "\n")
}

// --- checks ----------------------------------------------------------------------

func (t *Tab) checksView(p github.PR, e *detailEntry, w int) []string {
	th := t.theme
	if e == nil {
		return []string{t.pending(p)}
	}
	if len(e.d.Checks) == 0 {
		return []string{th.Hint.Render("no checks reported for the head commit")}
	}
	now := t.now()
	out := make([]string, 0, len(e.d.Checks))
	for _, c := range e.d.Checks {
		icon, st := th.CheckIcon(c.State)
		left := st.Render(icon) + " " + th.Body.Render(c.Name)
		if c.Workflow != "" {
			left += th.Hint.Render(" · " + c.Workflow)
		}
		right := th.Hint.Render(checkDuration(c, now))
		gap := w - lipgloss.Width(left) - lipgloss.Width(right)
		if gap < 1 {
			left = ansi.Truncate(left, max(w-lipgloss.Width(right)-2, 1), "…")
			gap = max(w-lipgloss.Width(left)-lipgloss.Width(right), 1)
		}
		out = append(out, left+strings.Repeat(" ", gap)+right)
	}
	return out
}

// checkDuration is "2m14s", "running 1m3s", "queued", "skipped" or "—".
func checkDuration(c github.Check, now time.Time) string {
	d := c.Duration(now)
	switch {
	case c.State == github.CheckRunning:
		return "running " + shortDuration(d)
	case c.State == github.CheckPending:
		return "queued"
	case c.State == github.CheckSkipped && d == 0:
		return "skipped"
	case d == 0:
		return "—"
	}
	return shortDuration(d)
}

func shortDuration(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

// --- activity ----------------------------------------------------------------------

func (t *Tab) activityView(p github.PR, e *detailEntry, w int) []string {
	th := t.theme
	if e == nil {
		return []string{t.pending(p)}
	}
	if len(e.d.Activity) == 0 {
		return []string{th.Hint.Render("no comments or reviews yet")}
	}
	now := t.now()
	ready := e.mdWidth == w
	var out []string
	for i, ev := range e.d.Activity {
		if i > 0 {
			out = append(out, "")
		}
		name := "ghost"
		if ev.Author != "" {
			name = "@" + ev.Author
		}
		head := th.Body.Bold(true).Render(name) + " "
		if ev.Kind == github.EventReview {
			icon, st := th.ReviewStateIcon(ev.State)
			head += st.Render(icon + " " + reviewWord(ev.State))
		} else {
			head += th.Hint.Render("commented")
		}
		if a := age(now, ev.At); a != "" {
			head += th.Hint.Render(" · " + a + " ago")
		}
		out = append(out, head)
		if strings.TrimSpace(ev.Body) == "" {
			continue
		}
		rendered := ""
		if ready && i < len(e.events) {
			rendered = e.events[i]
		}
		out = append(out, t.markdown(rendered, ev.Body, ready, w, "")...)
	}
	return out
}
