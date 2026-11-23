package notes

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
)

// Heading starts a standup section in a note.
const Heading = "## Standup"

// Inputs is everything a standup is built from.
type Inputs struct {
	// Day is the standup's day (local midnight).
	Day time.Time
	// Merged are my merged PRs, Reviewed the PRs I reviewed (not mine),
	// Open my open PRs.
	Merged, Reviewed, Open []github.PR
	// Done are my completed Plane items, Active my open ones (state
	// expanded).
	Done, Active []plane.WorkItem
	// Archived are archived sessions, Live the others.
	Archived, Live []sessions.Row
	// Blocked reports whether an open item counts as blocked; nil = never.
	Blocked func(plane.WorkItem) bool
	// Errs are sources that failed; each is noted under the standup.
	Errs []error
}

// Window is the "done yesterday" span of day: from the previous workday's
// midnight up to day's, so weekend work counts on a Monday.
func Window(day time.Time) (from, to time.Time) {
	return PrevWorkday(day), day
}

// Build renders the standup section as Markdown.
func Build(in Inputs) string {
	from, to := Window(in.Day)
	in2 := func(t time.Time) bool { return !t.IsZero() && !t.Before(from) && t.Before(to) }
	blocked := func(w plane.WorkItem) bool { return in.Blocked != nil && in.Blocked(w) }

	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s\n\n", Heading, in.Day.Format("Mon 2 Jan"))

	// Done yesterday.
	var done []string
	merged := map[string]bool{}
	for _, p := range sortedPRs(in.Merged, func(p github.PR) time.Time { return p.MergedAt }) {
		if in2(p.MergedAt) {
			merged[p.ID()] = true
			done = append(done, fmt.Sprintf("Merged %s — %s", p.ID(), p.Title))
		}
	}
	for _, p := range sortedPRs(in.Reviewed, func(p github.PR) time.Time { return p.UpdatedAt }) {
		if in2(p.UpdatedAt) && !merged[p.ID()] {
			done = append(done, fmt.Sprintf("Reviewed %s — %s", p.ID(), p.Title))
		}
	}
	items := slices.Clone(in.Done)
	slices.SortStableFunc(items, func(a, b plane.WorkItem) int { return completed(a).Compare(completed(b)) })
	for _, w := range items {
		if in2(completed(w)) {
			done = append(done, fmt.Sprintf("Closed %s — %s", w.Key(), w.Name))
		}
	}
	archived := slices.Clone(in.Archived)
	slices.SortStableFunc(archived, func(a, b sessions.Row) int { return a.ArchivedAt.Compare(b.ArchivedAt) })
	for _, r := range archived {
		if in2(r.ArchivedAt) {
			done = append(done, "Agent "+r.Name+sessionSummary(r.Summary))
		}
	}
	title := fmt.Sprintf("Done yesterday (%s)", from.Format("Mon 2 Jan"))
	if !AddDays(from, 1).Equal(to) { // after a weekend
		title = "Done since " + from.Format("Mon 2 Jan")
	}
	writeSection(&b, title, done, true)

	// Today and blockers.
	var today, blockers []string
	for _, w := range in.Active {
		switch {
		case blocked(w):
			blockers = append(blockers, fmt.Sprintf("%s — %s (%s)", w.Key(), w.Name, stateName(w)))
		case w.State != nil && w.State.Group == plane.GroupStarted:
			today = append(today, fmt.Sprintf("In progress %s — %s", w.Key(), w.Name))
		}
	}
	for _, p := range in.Open {
		if !p.Open() {
			continue
		}
		if why := prBlocker(p); why != "" {
			blockers = append(blockers, fmt.Sprintf("%s — %s (%s)", p.ID(), p.Title, why))
			continue
		}
		line := fmt.Sprintf("Open PR %s — %s", p.ID(), p.Title)
		if p.Draft {
			line += " (draft)"
		}
		today = append(today, line)
	}
	for _, r := range in.Live {
		switch r.Status {
		case store.StatusError:
			blockers = append(blockers, "Agent "+r.Name+" — error")
		case store.StatusStarting, store.StatusWorking, store.StatusWaiting, store.StatusIdle:
			today = append(today, "Agent "+r.Name+" — "+string(r.Status))
		}
	}
	writeSection(&b, "Today", today, true)
	writeSection(&b, "Blockers", blockers, false)

	for _, err := range in.Errs {
		fmt.Fprintf(&b, "_(%s)_\n", oneLine(err.Error()))
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// writeSection writes a "###" list; an empty one says so, or is left out
// when always is false.
func writeSection(b *strings.Builder, title string, lines []string, always bool) {
	if len(lines) == 0 && !always {
		return
	}
	fmt.Fprintf(b, "### %s\n", title)
	if len(lines) == 0 {
		b.WriteString("- nothing recorded\n")
	}
	for _, l := range lines {
		b.WriteString("- " + l + "\n")
	}
	b.WriteString("\n")
}

func sortedPRs(ps []github.PR, at func(github.PR) time.Time) []github.PR {
	out := slices.Clone(ps)
	slices.SortStableFunc(out, func(a, b github.PR) int {
		return cmp.Or(at(a).Compare(at(b)), cmp.Compare(a.ID(), b.ID()))
	})
	return out
}

// completed is when an item was finished: CompletedAt, or UpdatedAt when
// Plane didn't record it.
func completed(w plane.WorkItem) time.Time {
	if !w.CompletedAt.IsZero() {
		return w.CompletedAt.Time
	}
	return w.UpdatedAt.Time
}

func stateName(w plane.WorkItem) string {
	if w.State != nil && w.State.Name != "" {
		return w.State.Name
	}
	return "blocked"
}

// prBlocker says why an open PR of mine is stuck; "" when it isn't.
func prBlocker(p github.PR) string {
	var why []string
	if p.Failing() {
		why = append(why, "CI failing")
	}
	if p.ReviewDecision == github.ReviewChangesRequested {
		why = append(why, "changes requested")
	}
	return strings.Join(why, ", ")
}

// sessionSummary condenses an archive summary (sessions.Summarize): its
// first link line ("PR owner/repo#12 MERGED — Title") and the agent's
// last reply, as " — link · reply". "" for an empty summary.
func sessionSummary(summary string) string {
	var link, reply string
	for l := range strings.Lines(summary) {
		l = strings.TrimSpace(l)
		switch {
		case link == "" && (strings.HasPrefix(l, "PR ") || strings.HasPrefix(l, "issue ")):
			link = l
		case strings.HasPrefix(l, "- "):
			reply = strings.TrimPrefix(l, "- ")
		}
	}
	parts := slices.DeleteFunc([]string{link, reply}, func(s string) bool { return s == "" })
	if len(parts) == 0 {
		return ""
	}
	return " — " + strings.Join(parts, " · ")
}

func oneLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return strings.TrimSpace(s)
}

// LastStandup is the last standup section of text (from its heading to
// the next "## " heading or the end), or "" when there is none.
func LastStandup(text string) string {
	lines := strings.Split(text, "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, Heading) {
			start = i
		}
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	return strings.TrimRight(strings.Join(lines[start:end], "\n"), "\n") + "\n"
}
