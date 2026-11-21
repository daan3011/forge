package pipelines

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/ui"
)

// The detail pane shows the selected run's jobs and steps. Its header
// comes from the list row; jobs are fetched once the cursor has rested for
// detailDebounce, again whenever the run's updatedAt moves, and on every
// fast refresh while the run is active.

const (
	detailDebounce = 150 * time.Millisecond
	jobsTimeout    = 30 * time.Second
	// jobsMemCap is how many runs' jobs stay in memory.
	jobsMemCap = 32
)

// jobsEntry is the last fetched jobs of a run.
type jobsEntry struct {
	jobs []github.Job
	// forUpdated is the run's updatedAt the jobs were fetched for.
	forUpdated time.Time
	err        error
}

type detailState struct {
	offset int
	seq    int    // debounce generation
	want   string // run key + updatedAt last scheduled

	jobs     map[string]*jobsEntry // by run key
	order    []string              // jobs keys, oldest first
	inflight map[string]bool       // run keys being fetched
}

func newDetailState() detailState {
	return detailState{jobs: map[string]*jobsEntry{}, inflight: map[string]bool{}}
}

func (d *detailState) remember(key string, e *jobsEntry) {
	if _, ok := d.jobs[key]; !ok {
		d.order = append(d.order, key)
	}
	d.jobs[key] = e
	for len(d.order) > jobsMemCap {
		delete(d.jobs, d.order[0])
		d.order = d.order[1:]
	}
}

func wantKey(r github.Run) string {
	return r.Key() + "@" + r.UpdatedAt.UTC().Format(time.RFC3339Nano)
}

type detailDueMsg struct{ seq int }

// TargetTab implements tabs.TargetedMsg.
func (detailDueMsg) TargetTab() string { return Title }

type jobsLoadedMsg struct {
	key        string
	forUpdated time.Time
	jobs       []github.Job
	err        error
}

// TargetTab implements tabs.TargetedMsg.
func (jobsLoadedMsg) TargetTab() string { return Title }

func (t *Tab) updateDetail(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case detailDueMsg:
		if msg.seq != t.det.seq {
			return nil, true
		}
		r, ok := t.selected()
		if !ok || wantKey(r) != t.det.want {
			return nil, true
		}
		return t.fetchJobs(r), true
	case jobsLoadedMsg:
		delete(t.det.inflight, msg.key)
		prev := t.det.jobs[msg.key]
		if msg.err != nil {
			e := &jobsEntry{forUpdated: msg.forUpdated, err: msg.err}
			if prev != nil {
				e.jobs = prev.jobs // keep showing what we had
			}
			t.det.remember(msg.key, e)
			if r, ok := t.selected(); ok && r.Key() == msg.key {
				return errCmd(fmt.Errorf("jobs of %s #%d: %s", r.Workflow, r.RunNumber, github.Reason(msg.err))), true
			}
			return nil, true
		}
		t.det.remember(msg.key, &jobsEntry{jobs: msg.jobs, forUpdated: msg.forUpdated})
		return nil, true
	}
	return nil, false
}

// syncDetail schedules a jobs fetch when the selected run has none for its
// current updatedAt.
func (t *Tab) syncDetail() tea.Cmd {
	r, ok := t.selected()
	if !ok || t.gh == nil {
		return nil
	}
	want := wantKey(r)
	if want == t.det.want {
		return nil
	}
	if t.det.want == "" || !strings.HasPrefix(t.det.want, r.Key()+"@") {
		t.det.offset = 0 // a new run starts at the top
	}
	t.det.want = want
	if e := t.det.jobs[r.Key()]; e != nil && e.forUpdated.Equal(r.UpdatedAt) && e.err == nil {
		return nil
	}
	t.det.seq++
	seq := t.det.seq
	return t.tick(detailDebounce, func(time.Time) tea.Msg { return detailDueMsg{seq: seq} })
}

// fetchJobs fetches r's jobs unless a fetch for it is running.
func (t *Tab) fetchJobs(r github.Run) tea.Cmd {
	k := r.Key()
	if t.gh == nil || t.det.inflight[k] {
		return nil
	}
	t.det.inflight[k] = true
	gh, repo, id, upd := t.gh, r.Repo, r.ID, r.UpdatedAt
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), jobsTimeout)
		defer cancel()
		jobs, err := gh.RunJobs(ctx, repo, id)
		return jobsLoadedMsg{key: k, forUpdated: upd, jobs: jobs, err: err}
	}
}

// scrollDetail moves the pane by half its height; the view clamps it.
func (t *Tab) scrollDetail(dir int) {
	_, h := t.split.RightSize()
	t.det.offset = max(t.det.offset+dir*max(h/2, 1), 0)
}

// --- view ------------------------------------------------------------------------

// detail is the right pane: the run header, then jobs and their steps.
func (t *Tab) detail() string {
	th := t.theme
	w, h := t.split.RightSize()
	if w <= 0 || h <= 0 {
		return ""
	}
	r, ok := t.selected()
	if !ok {
		return th.Hint.Render("no run selected")
	}
	head := t.header(r, w)
	content := t.jobLines(r, w)
	avail := max(h-len(head)-1, 1)
	maxOff := max(len(content)-avail, 0)
	t.det.offset = min(t.det.offset, maxOff)
	rule := strings.Repeat("─", w)
	if maxOff > 0 {
		pos := fmt.Sprintf(" %d/%d ", min(t.det.offset+avail, len(content)), len(content))
		rule = strings.Repeat("─", max(w-len(pos), 0)) + pos
	}
	head = append(head, th.DetailRule.Render(rule))
	end := min(t.det.offset+avail, len(content))
	return strings.Join(append(head, content[t.det.offset:end]...), "\n")
}

// header is "repo · workflow #n", the title on a band, the state pill with
// branch @ sha, and event, actor, start and duration.
func (t *Tab) header(r github.Run, w int) []string {
	th := t.theme
	now := t.now()
	name := fmt.Sprintf("%s · %s #%d", r.Repo, r.Workflow, r.RunNumber)
	if r.Attempt > 1 {
		name += fmt.Sprintf(" (attempt %d)", r.Attempt)
	}
	lines := []string{th.PRRepo.Render(ansi.Truncate(name, w, "…"))}
	title := r.Title
	if title == "" {
		title = r.Workflow
	}
	for _, l := range strings.Split(ansi.Wrap(title, max(w-2, 1), ""), "\n") {
		lines = append(lines, th.DetailBand.Render(" "+l+strings.Repeat(" ", max(w-2-ansi.StringWidth(l), 0))+" "))
	}
	icon, _ := th.CheckIcon(r.State)
	pill := th.CheckPill(r.State).Render(icon + " " + stateWord(r.State))
	sha := r.HeadSHA
	if len(sha) > 7 {
		sha = sha[:7]
	}
	ref := r.Branch
	if sha != "" {
		ref += " @ " + sha
	}
	lines = append(lines, "")
	lines = append(lines, strings.Split(ansi.Wrap(pill+" "+th.Hint.Render(ref), max(w, 1), ""), "\n")...)
	by := th.Hint.Render(r.Event + " by ")
	actor := r.Actor
	if actor == "" {
		actor = "ghost"
	}
	by += th.Body.Bold(true).Render("@" + actor)
	if !r.CreatedAt.IsZero() {
		by += th.Hint.Render(" · " + ui.ShortAge(now.Sub(r.CreatedAt)) + " ago")
	}
	by += th.Hint.Render(" · " + ui.StateDuration(r.State, r.Duration(now)))
	lines = append(lines, strings.Split(ansi.Wrap(by, max(w, 1), ""), "\n")...)
	return append(lines, "")
}

// jobLines lists each job with its duration, its steps indented below.
func (t *Tab) jobLines(r github.Run, w int) []string {
	th := t.theme
	e := t.det.jobs[r.Key()]
	if e == nil || (e.jobs == nil && e.err != nil) {
		switch {
		case e != nil:
			msg := "couldn't load jobs: " + github.Reason(e.err)
			return strings.Split(ansi.Wrap(th.DiffDel.Render(msg), max(w, 1), ""), "\n")
		case t.gh == nil:
			return []string{th.Hint.Render("GitHub isn't available")}
		}
		return []string{th.TableEmpty.Render("heating up…")}
	}
	if len(e.jobs) == 0 {
		return []string{th.Hint.Render("no jobs yet")}
	}
	now := t.now()
	var out []string
	if !e.forUpdated.Equal(r.UpdatedAt) || t.det.inflight[r.Key()] && r.Active() {
		out = append(out, th.Hint.Render("updating…"))
	}
	for i, j := range e.jobs {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, t.stateLine("", j.State, j.Name, ui.StateDuration(j.State, j.Duration(now)), w, true))
		for _, s := range j.Steps {
			name := fmt.Sprintf("%d. %s", s.Number, s.Name)
			out = append(out, t.stateLine("  ", s.State, name, ui.StateDuration(s.State, s.Duration(now)), w, false))
		}
	}
	return out
}

// stateLine is "indent icon name … duration", the duration right-aligned.
func (t *Tab) stateLine(indent, state, name, dur string, w int, bold bool) string {
	th := t.theme
	glyph, ist := th.CheckIcon(state)
	nst := th.Body
	switch {
	case bold:
		nst = nst.Bold(true)
	case state == github.CheckSkipped || state == github.CheckPending:
		nst = th.Hint
	}
	dst := th.PRTime
	if state == github.CheckRunning {
		dst = ist
	}
	left := indent + ist.Render(glyph) + " "
	room := max(w-ansi.StringWidth(left)-ansi.StringWidth(dur)-1, 1)
	n := ansi.Truncate(name, room, "…")
	gap := max(w-ansi.StringWidth(left)-ansi.StringWidth(n)-ansi.StringWidth(dur), 1)
	return left + nst.Render(n) + strings.Repeat(" ", gap) + dst.Render(dur)
}
