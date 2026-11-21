package pipelines

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/cifix"
	"github.com/daan3011/forge/internal/cilog"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

// The log viewer (P2.7) takes over the tab to show a job's log, collapsed
// to the lines around errors. `e` shows the full log, `/` searches, `[` and
// `]` move between the run's failed jobs. Each loaded failed job's error
// context is saved in kv_cache (cilog.SummaryKey) for "fix this" (P2.8).

const (
	logTimeout = 2 * time.Minute
	// logMemCap is how many jobs' logs stay in memory.
	logMemCap = 8
	// logHeaderLines are the title line and the rule under it.
	logHeaderLines = 2
)

// jobLog is a downloaded, cleaned log.
type jobLog struct {
	lines []string
	view  cilog.View // the collapsed view
	hunks int        // error hunks in view
}

// logRow is one line on screen; orig is its line in the full log, -1 for
// a "lines hidden" separator.
type logRow struct {
	text string
	orig int
}

// logView is the open viewer.
type logView struct {
	id   int
	run  github.Run
	jobs []github.Job // the jobs it moves between
	// failed is set when jobs are the run's failed jobs (else they are
	// its finished ones).
	failed bool
	job    int // index into jobs

	waitJobs bool  // the run's jobs are being fetched
	loading  bool  // the current job's log is being fetched
	err      error // the current job's log failed to load

	full    bool
	rows    []logRow
	offset  int
	search  ui.Filter
	matches []int // indexes into rows
	cur     int   // current match
	note    string
}

func (v *logView) current() (github.Job, bool) {
	if v.job < 0 || v.job >= len(v.jobs) {
		return github.Job{}, false
	}
	return v.jobs[v.job], true
}

// logCache keeps recently loaded logs by job id.
type logCache struct {
	order []int64
	logs  map[int64]*jobLog
}

func (c *logCache) get(id int64) *jobLog { return c.logs[id] }

func (c *logCache) put(id int64, l *jobLog) {
	if c.logs == nil {
		c.logs = map[int64]*jobLog{}
	}
	if _, ok := c.logs[id]; !ok {
		c.order = append(c.order, id)
	}
	c.logs[id] = l
	for len(c.order) > logMemCap {
		delete(c.logs, c.order[0])
		c.order = c.order[1:]
	}
}

type logLoadedMsg struct {
	id    int
	jobID int64
	log   *jobLog
	err   error
}

type summarySavedMsg struct{ err error }

func (logLoadedMsg) TargetTab() string    { return Title }
func (summarySavedMsg) TargetTab() string { return Title }

// updateLogMsg handles the viewer's messages.
func (t *Tab) updateLogMsg(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case logLoadedMsg:
		return t.logLoaded(msg), true
	case summarySavedMsg:
		if msg.err != nil {
			return errCmd(fmt.Errorf("save error summary: %w", msg.err)), true
		}
		return nil, true
	}
	return nil, false
}

// openLogs opens the viewer on the selected run's failed jobs, fetching
// the jobs first when they aren't loaded.
func (t *Tab) openLogs() tea.Cmd {
	r, ok := t.selected()
	if !ok {
		return nil
	}
	if t.gh == nil {
		return errCmd(errors.New("GitHub isn't available"))
	}
	t.logID++
	v := &logView{id: t.logID, run: r, search: ui.NewFilter(t.theme, t.keys)}
	if e := t.det.jobs[r.Key()]; e != nil && e.jobs != nil {
		return t.startLogs(v, e.jobs)
	}
	v.waitJobs = true
	t.log = v
	return t.fetchJobs(r)
}

// logJobsLoaded continues opening the viewer once its run's jobs land.
func (t *Tab) logJobsLoaded(msg jobsLoadedMsg) tea.Cmd {
	v := t.log
	if v == nil || !v.waitJobs || v.run.Key() != msg.key {
		return nil
	}
	if msg.err != nil {
		t.log = nil
		return nil // the jobs pane already reported it
	}
	return t.startLogs(v, msg.jobs)
}

// startLogs picks the jobs to show (the failed ones, else the finished
// ones) and loads the first.
func (t *Tab) startLogs(v *logView, jobs []github.Job) tea.Cmd {
	v.waitJobs = false
	var failed, done []github.Job
	for _, j := range jobs {
		switch j.State {
		case github.CheckFail:
			failed = append(failed, j)
			done = append(done, j)
		case github.CheckRunning, github.CheckPending:
		default:
			done = append(done, j)
		}
	}
	switch {
	case len(failed) > 0:
		v.jobs, v.failed = failed, true
	case len(done) > 0:
		v.jobs = done
		v.note = "no failed jobs; showing finished ones"
	default:
		t.log = nil
		return info(fmt.Sprintf("%s #%d has no finished jobs yet", v.run.Workflow, v.run.RunNumber))
	}
	t.log = v
	return t.loadJob(0)
}

// loadJob shows jobs[i], from memory or by downloading its log.
func (t *Tab) loadJob(i int) tea.Cmd {
	v := t.log
	if v == nil || i < 0 || i >= len(v.jobs) {
		return nil
	}
	v.job, v.err, v.offset, v.full = i, nil, 0, false
	v.matches, v.cur = nil, 0
	j := v.jobs[i]
	if l := t.logs.get(j.ID); l != nil {
		v.loading = false
		t.layoutLog(true)
		return nil
	}
	v.loading = true
	v.rows = nil
	gh, repo, id, jobID := t.gh, v.run.Repo, v.id, j.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), logTimeout)
		defer cancel()
		raw, err := gh.JobLogs(ctx, repo, jobID)
		if err != nil {
			return logLoadedMsg{id: id, jobID: jobID, err: err}
		}
		lines := cilog.Clean(raw)
		hunks := cilog.Extract(lines, cilog.Context)
		return logLoadedMsg{id: id, jobID: jobID, log: &jobLog{
			lines: lines, view: cilog.Collapse(lines, hunks), hunks: len(hunks),
		}}
	}
}

func (t *Tab) logLoaded(msg logLoadedMsg) tea.Cmd {
	if msg.err == nil {
		t.logs.put(msg.jobID, msg.log)
	}
	v := t.log
	if v == nil || msg.id != v.id {
		return nil
	}
	j, ok := v.current()
	if !ok || j.ID != msg.jobID {
		return nil // moved on to another job meanwhile
	}
	v.loading = false
	if msg.err != nil {
		v.err = msg.err
		return nil
	}
	t.layoutLog(true)
	if !v.failed {
		return nil
	}
	return t.saveSummary(v.run, j, msg.log)
}

// saveSummary stores the job's error context for "fix this" (P2.8).
func (t *Tab) saveSummary(r github.Run, j github.Job, l *jobLog) tea.Cmd {
	if t.store == nil {
		return nil
	}
	s := cifix.NewSummary(cifix.RunOf(r), j.ID, j.Name, j.URL, l.view, t.now())
	project := ""
	if t.project != nil {
		project = t.project.Name
	}
	st := t.store
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return summarySavedMsg{err: cifix.Save(ctx, st, project, s)}
	}
}

func (t *Tab) closeLogs() tea.Cmd {
	t.log = nil
	return nil
}

// --- layout & navigation -----------------------------------------------------------

// logSize is the viewer's inner size, inside the pane border.
func (t *Tab) logSize() (w, h int) { return max(t.width-2, 10), max(t.height-2, 3) }

// logBodyHeight is how many log lines fit.
func (t *Tab) logBodyHeight() int {
	_, h := t.logSize()
	h -= logHeaderLines
	if t.log != nil && t.log.search.Visible() {
		h--
	}
	return max(h, 1)
}

// layoutLog fills rows for the current job and mode. With fresh, the view
// starts at the first error.
func (t *Tab) layoutLog(fresh bool) {
	v := t.log
	j, ok := v.current()
	if !ok {
		return
	}
	l := t.logs.get(j.ID)
	if l == nil {
		v.rows = nil
		return
	}
	v.rows = v.rows[:0]
	if v.full {
		for i, s := range l.lines {
			v.rows = append(v.rows, logRow{text: s, orig: i})
		}
	} else {
		for _, x := range l.view.Lines {
			v.rows = append(v.rows, logRow{text: x.Text, orig: x.Orig})
		}
	}
	if fresh {
		v.offset = 0
		if !v.full && l.view.Tail {
			v.offset = len(v.rows) // the end; clamped below
		}
	}
	t.findMatches()
	t.clampLog()
}

func (t *Tab) clampLog() {
	v := t.log
	v.offset = min(max(v.offset, 0), max(len(v.rows)-t.logBodyHeight(), 0))
}

func (t *Tab) scrollLog(n int) {
	t.log.offset += n
	t.clampLog()
}

// toggleFull switches between error context and the full log, keeping the
// top line in view.
func (t *Tab) toggleFull() tea.Cmd {
	v := t.log
	if v == nil || len(v.rows) == 0 {
		return nil
	}
	top := -1
	for i := v.offset; i < len(v.rows); i++ {
		if v.rows[i].orig >= 0 {
			top = v.rows[i].orig
			break
		}
	}
	v.full = !v.full
	v.note = ""
	t.layoutLog(false)
	t.showOrig(top)
	return nil
}

// showOrig scrolls to the first row at or after full-log line orig.
func (t *Tab) showOrig(orig int) {
	v := t.log
	if orig < 0 {
		return
	}
	v.offset = len(v.rows) - 1
	for i, r := range v.rows {
		if r.orig >= orig {
			v.offset = i
			break
		}
	}
	t.clampLog()
}

// switchJob moves to the next (+1) or previous (-1) job.
func (t *Tab) switchJob(dir int) tea.Cmd {
	v := t.log
	if v == nil || len(v.jobs) < 2 {
		return nil
	}
	return t.loadJob((v.job + dir + len(v.jobs)) % len(v.jobs))
}

// --- search ------------------------------------------------------------------------

// findMatches finds the search query in the rows (case-insensitive).
func (t *Tab) findMatches() {
	v := t.log
	v.matches, v.cur = v.matches[:0], 0
	q := strings.ToLower(v.search.Value())
	if q == "" {
		return
	}
	for i, r := range v.rows {
		if r.orig >= 0 && strings.Contains(strings.ToLower(r.text), q) {
			v.matches = append(v.matches, i)
		}
	}
	// Start at the first match at or below the top of the view.
	for k, i := range v.matches {
		if i >= v.offset {
			v.cur = k
			return
		}
	}
}

// fullHasMatch reports whether the full log contains the query.
func (t *Tab) fullHasMatch() bool {
	v := t.log
	j, ok := v.current()
	l := t.logs.get(j.ID)
	q := strings.ToLower(v.search.Value())
	if !ok || l == nil || q == "" {
		return false
	}
	for _, s := range l.lines {
		if strings.Contains(strings.ToLower(s), q) {
			return true
		}
	}
	return false
}

// showMatch scrolls the current match into view, a third from the top.
func (t *Tab) showMatch() {
	v := t.log
	if len(v.matches) == 0 {
		return
	}
	row := v.matches[v.cur]
	h := t.logBodyHeight()
	if row < v.offset || row >= v.offset+h {
		v.offset = row - h/3
		t.clampLog()
	}
}

func (t *Tab) stepMatch(dir int) tea.Cmd {
	v := t.log
	if v == nil || len(v.matches) == 0 {
		return nil
	}
	v.cur = (v.cur + dir + len(v.matches)) % len(v.matches)
	t.showMatch()
	return nil
}

func (t *Tab) startLogSearch() tea.Cmd {
	if t.log == nil || len(t.log.rows) == 0 {
		return nil
	}
	t.log.search.Start()
	t.clampLog()
	return nil
}

// updateLogSearch edits the query; matches update as it is typed. On
// accept, a query found only in the hidden lines switches to the full log.
func (t *Tab) updateLogSearch(kp tea.KeyPressMsg) tea.Cmd {
	v := t.log
	in := t.keys.Input
	switch {
	case key.Matches(kp, in.Accept):
		v.search.Stop()
		if v.search.Value() == "" {
			t.clearLogSearch()
			return nil
		}
		if len(v.matches) == 0 && !v.full && t.fullHasMatch() {
			v.full = true
			t.layoutLog(false)
			v.cur = 0
			v.note = "matches only in the full log"
		}
		t.showMatch()
	case key.Matches(kp, in.Cancel):
		t.clearLogSearch()
	default:
		if v.search.Update(kp) {
			t.findMatches()
			t.showMatch()
		}
	}
	return nil
}

func (t *Tab) clearLogSearch() {
	v := t.log
	v.search.Clear()
	v.matches, v.cur = nil, 0
	t.clampLog()
}

// updateLog handles a key press while the viewer is open.
func (t *Tab) updateLog(kp tea.KeyPressMsg) tea.Cmd {
	v := t.log
	if v.search.Active() {
		return t.updateLogSearch(kp)
	}
	k := t.keys.Log
	half := max(t.logBodyHeight()/2, 1)
	switch {
	case key.Matches(kp, k.Close):
		if v.search.Value() != "" {
			t.clearLogSearch() // esc drops the search first
			return nil
		}
		return t.closeLogs()
	case key.Matches(kp, k.RerunFailed):
		return t.rerunFailed()
	case key.Matches(kp, k.RerunAll):
		return t.rerunAll()
	case key.Matches(kp, k.CancelRun):
		return t.cancelRun()
	case key.Matches(kp, k.Fix):
		return t.fix(false)
	case key.Matches(kp, k.FixEdit):
		return t.fix(true)
	case key.Matches(kp, k.Open):
		return t.openJob()
	case key.Matches(kp, k.NextJob):
		return t.switchJob(1)
	case key.Matches(kp, k.PrevJob):
		return t.switchJob(-1)
	}
	if len(v.rows) == 0 {
		return nil
	}
	switch {
	case key.Matches(kp, k.Up):
		t.scrollLog(-1)
	case key.Matches(kp, k.Down):
		t.scrollLog(1)
	case key.Matches(kp, k.PageUp):
		t.scrollLog(-half)
	case key.Matches(kp, k.PageDown):
		t.scrollLog(half)
	case key.Matches(kp, k.Top):
		v.offset = 0
	case key.Matches(kp, k.Bottom):
		v.offset = len(v.rows)
		t.clampLog()
	case key.Matches(kp, k.ToggleFull):
		return t.toggleFull()
	case key.Matches(kp, k.Search):
		return t.startLogSearch()
	case key.Matches(kp, k.NextMatch):
		return t.stepMatch(1)
	case key.Matches(kp, k.PrevMatch):
		return t.stepMatch(-1)
	}
	return nil
}

// openJob opens the viewer's job on GitHub.
func (t *Tab) openJob() tea.Cmd {
	v := t.log
	if v == nil {
		return nil
	}
	url := v.run.URL
	if j, ok := v.current(); ok && j.URL != "" {
		url = j.URL
	}
	return t.openURL(url, "open job")
}

// --- view --------------------------------------------------------------------------

func (t *Tab) logViewString() string {
	th := t.theme
	v := t.log
	w, h := t.logSize()
	lines := []string{t.logTitle(w), th.DetailRule.Render(t.logRule(w))}
	body := t.logBody(w)
	lines = append(lines, body...)
	if v.search.Visible() {
		for len(lines) < h-1 {
			lines = append(lines, "")
		}
		lines = append(lines, v.search.View(w))
	}
	return th.PaneFocused.Render(ui.Box(strings.Join(lines, "\n"), w, h))
}

// logTitle is "repo · workflow #n › job (k/N failed)".
func (t *Tab) logTitle(w int) string {
	th := t.theme
	v := t.log
	head := fmt.Sprintf("%s · %s #%d", v.run.Repo, v.run.Workflow, v.run.RunNumber)
	job := ""
	if j, ok := v.current(); ok {
		job = j.Name
		if len(v.jobs) > 1 {
			word := "finished"
			if v.failed {
				word = "failed"
			}
			job += fmt.Sprintf("  (%d/%d %s)", v.job+1, len(v.jobs), word)
		}
	}
	s := th.PRRepo.Render(head)
	if job != "" {
		s += th.Hint.Render(" › ") + th.DetailTitle.Render(job)
	}
	return ansi.Truncate(s, w, "…")
}

// logRule names the mode, the search state and the position.
func (t *Tab) logRule(w int) string {
	v := t.log
	var parts []string
	if j, ok := v.current(); ok {
		if l := t.logs.get(j.ID); l != nil {
			switch {
			case v.full:
				parts = append(parts, "full log · "+plural(len(l.lines), "line"))
			case l.view.Tail:
				parts = append(parts, "no error lines found · showing the tail")
			default:
				parts = append(parts, "errors · "+plural(l.hunks, "hunk"))
			}
		}
	}
	if q := v.search.Value(); q != "" {
		if len(v.matches) == 0 {
			parts = append(parts, fmt.Sprintf("%q: no matches", q))
		} else {
			parts = append(parts, fmt.Sprintf("%q: match %d/%d", q, v.cur+1, len(v.matches)))
		}
	}
	if v.note != "" {
		parts = append(parts, v.note)
	}
	if n := len(v.rows); n > t.logBodyHeight() {
		parts = append(parts, fmt.Sprintf("%d/%d", min(v.offset+t.logBodyHeight(), n), n))
	}
	label := ""
	if len(parts) > 0 {
		label = " " + strings.Join(parts, " · ") + " "
	}
	label = ansi.Truncate(label, max(w-2, 0), "…")
	return "──" + label + strings.Repeat("─", max(w-2-ansi.StringWidth(label), 0))
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// logBody is the visible log lines with a line-number gutter.
func (t *Tab) logBody(w int) []string {
	th := t.theme
	v := t.log
	switch {
	case v.waitJobs || v.loading:
		return []string{th.TableEmpty.Render("heating up…")}
	case v.err != nil:
		msg := "couldn't load the log: " + github.Reason(v.err)
		out := strings.Split(ansi.Wrap(th.DiffDel.Render(msg), max(w, 1), ""), "\n")
		return append(out, th.Hint.Render(t.keys.Log.Open.Help().Key+" opens the job in the browser"))
	case len(v.rows) == 0:
		return []string{th.Hint.Render("the log is empty")}
	}
	total := len(v.rows)
	if j, ok := v.current(); ok {
		if l := t.logs.get(j.ID); l != nil {
			total = len(l.lines)
		}
	}
	gw := len(strconv.Itoa(total))
	room := max(w-gw-1, 1)
	cur := -1
	if len(v.matches) > 0 {
		cur = v.matches[v.cur]
	}
	q := v.search.Value()
	end := min(v.offset+t.logBodyHeight(), len(v.rows))
	out := make([]string, 0, end-v.offset)
	for i := v.offset; i < end; i++ {
		r := v.rows[i]
		if r.orig < 0 {
			out = append(out, strings.Repeat(" ", gw+1)+th.LogHidden.Render(ansi.Truncate(r.text, room, "…")))
			continue
		}
		gutter := th.LogGutter.Render(fmt.Sprintf("%*d ", gw, r.orig+1))
		text := ansi.Truncate(r.text, room, "…")
		base := th.Body
		switch {
		case cilog.IsErrorLine(r.text):
			base = th.LogError
		case strings.HasPrefix(r.text, "▸ "):
			base = th.LogGroup
		}
		hl := th.LogMatch
		if i == cur {
			hl = th.LogMatchCur
		}
		out = append(out, gutter+highlight(text, q, base, hl))
	}
	return out
}

// highlight renders s in base with each case-insensitive occurrence of q
// in hl.
func highlight(s, q string, base, hl interface{ Render(...string) string }) string {
	if q == "" {
		return base.Render(s)
	}
	low, lq := strings.ToLower(s), strings.ToLower(q)
	if len(low) != len(s) {
		if strings.Contains(low, lq) {
			return hl.Render(s) // lower-casing moved bytes: mark the line
		}
		return base.Render(s)
	}
	var b strings.Builder
	for {
		i := strings.Index(low, lq)
		if i < 0 {
			break
		}
		if i > 0 {
			b.WriteString(base.Render(s[:i]))
		}
		b.WriteString(hl.Render(s[i : i+len(lq)]))
		s, low = s[i+len(lq):], low[i+len(lq):]
	}
	if s != "" {
		b.WriteString(base.Render(s))
	}
	return b.String()
}

// --- help & commands ---------------------------------------------------------------

func (t *Tab) logHelp() []key.Binding {
	if t.log.search.Active() {
		return []key.Binding{t.keys.Input.Cancel, t.keys.Input.Accept}
	}
	k := t.keys.Log
	return []key.Binding{k.ToggleFull, k.Search, k.NextMatch, k.NextJob, k.RerunFailed, k.Open, k.Close}
}

func (t *Tab) logCommands() []tabs.Command {
	k := t.keys.Log
	return []tabs.Command{
		{ID: "log_toggle_full", Title: "Log: full log / errors only", Key: k.ToggleFull, Run: t.toggleFull},
		{ID: "log_search", Title: "Log: search", Key: k.Search, Run: t.startLogSearch},
		{ID: "log_next_match", Title: "Log: next match", Key: k.NextMatch, Run: func() tea.Cmd { return t.stepMatch(1) }},
		{ID: "log_prev_match", Title: "Log: previous match", Key: k.PrevMatch, Run: func() tea.Cmd { return t.stepMatch(-1) }},
		{ID: "log_next_job", Title: "Log: next failed job", Key: k.NextJob, Run: func() tea.Cmd { return t.switchJob(1) }},
		{ID: "log_prev_job", Title: "Log: previous failed job", Key: k.PrevJob, Run: func() tea.Cmd { return t.switchJob(-1) }},
		{ID: "log_open", Title: "Log: open job in browser", Key: k.Open, Run: t.openJob},
		{ID: "log_close", Title: "Log: close", Key: k.Close, Run: t.closeLogs},
	}
}
