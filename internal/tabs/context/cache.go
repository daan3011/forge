package context

import (
	stdctx "context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/cachectl"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

// The Cache view (P5.3): what forge stores, per type and per project, with
// actions to reclaim it. Every action plans first (in a command) and asks
// for confirmation with the exact counts; nothing is removed without a yes.

// section is which part of the tab is showing.
type section int

const (
	viewMemory section = iota
	viewPacks
	viewCache
)

// cacheTimeout bounds measuring (it walks worktrees) and each action.
const cacheTimeout = 2 * time.Minute

// defaultPruneDays is what the prune form starts with.
const defaultPruneDays = 30

// cacheState is the Cache view's state.
type cacheState struct {
	gen     int // bumped per load; older reports are dropped
	loading bool
	loaded  bool
	report  cachectl.Report
	err     error
	cursor  int // type row
	top     int
	// drill is the type broken down by project ("" = the type list).
	drill   cachectl.Type
	pcursor int
	ptop    int
	busy    bool // a plan or apply is running
}

// --- messages --------------------------------------------------------------------

type cacheLoadedMsg struct {
	gen    int
	report cachectl.Report
	err    error
}

type cachePlanMsg struct {
	plan cachectl.Plan
	err  error
	days int // Prune only, for the confirmation
}

type cacheDoneMsg struct {
	res cachectl.Result
	err error
}

// TargetTab implements tabs.TargetedMsg.
func (cacheLoadedMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (cachePlanMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (cacheDoneMsg) TargetTab() string { return Title }

// --- loading ---------------------------------------------------------------------

// loadCache measures every type in a command.
func (t *Tab) loadCache() tea.Cmd {
	if t.caches == nil {
		return nil
	}
	t.cs.gen++
	t.cs.loading = true
	gen, c := t.cs.gen, t.caches
	return func() tea.Msg {
		ctx, cancel := stdctx.WithTimeout(stdctx.Background(), cacheTimeout)
		defer cancel()
		r, err := c.Stats(ctx)
		return cacheLoadedMsg{gen: gen, report: r, err: err}
	}
}

func (t *Tab) cacheLoaded(msg cacheLoadedMsg) {
	if msg.gen != t.cs.gen {
		return
	}
	t.cs.loading, t.cs.loaded = false, true
	t.cs.report, t.cs.err = msg.report, msg.err
	t.updated = msg.report.At
	t.setCacheCursor(t.cs.cursor)
	t.setProjectCursor(t.cs.pcursor)
}

// toggleCache switches between memory files and the cache view (from the
// packs view it goes to the cache view).
func (t *Tab) toggleCache() tea.Cmd {
	if t.view == viewCache {
		t.view = viewMemory
		return nil
	}
	t.view = viewCache
	t.focusDetail, t.matrix = false, false
	return t.loadCache()
}

// inCache wraps a cache action for the palette: it shows the view first.
func (t *Tab) inCache(f func() tea.Cmd) func() tea.Cmd {
	return func() tea.Cmd {
		if t.view != viewCache {
			load := t.toggleCache()
			if !t.cs.loaded {
				return load // nothing to act on yet; the view shows the numbers
			}
			return tea.Batch(load, f())
		}
		return f()
	}
}

// --- selection -------------------------------------------------------------------

func (t *Tab) cacheTypes() []cachectl.TypeStat { return t.cs.report.Types }

// selectedType is the type under the cursor, or the one drilled into.
func (t *Tab) selectedType() (cachectl.TypeStat, bool) {
	if t.cs.drill != "" {
		return t.cs.report.Type(t.cs.drill), true
	}
	ts := t.cacheTypes()
	if t.cs.cursor < 0 || t.cs.cursor >= len(ts) {
		return cachectl.TypeStat{}, false
	}
	return ts[t.cs.cursor], true
}

// selectedProject is the project row under the cursor while drilled in.
func (t *Tab) selectedProject() (cachectl.ProjectStat, bool) {
	if t.cs.drill == "" {
		return cachectl.ProjectStat{}, false
	}
	ps := t.cs.report.Type(t.cs.drill).Projects
	if t.cs.pcursor < 0 || t.cs.pcursor >= len(ps) {
		return cachectl.ProjectStat{}, false
	}
	return ps[t.cs.pcursor], true
}

func (t *Tab) cacheListHeight() int {
	_, h := t.split.LeftSize()
	return max(h-footerLines-1, 1) // the header row
}

func scrollTo(cursor, top, n, h int) (int, int) {
	if n == 0 {
		return 0, 0
	}
	cursor = min(max(cursor, 0), n-1)
	if cursor < top {
		top = cursor
	}
	if cursor >= top+h {
		top = cursor - h + 1
	}
	return cursor, max(min(top, n-h), 0)
}

func (t *Tab) setCacheCursor(i int) {
	t.cs.cursor, t.cs.top = scrollTo(i, t.cs.top, len(t.cacheTypes()), t.cacheListHeight())
}

func (t *Tab) setProjectCursor(i int) {
	n := len(t.cs.report.Type(t.cs.drill).Projects)
	t.cs.pcursor, t.cs.ptop = scrollTo(i, t.cs.ptop, n, t.cacheListHeight())
}

// --- update ----------------------------------------------------------------------

func (t *Tab) cacheKey(kp tea.KeyPressMsg) tea.Cmd {
	ck, l := t.keys.Context, t.keys.List
	move := func(d int) {
		if t.cs.drill != "" {
			t.setProjectCursor(t.cs.pcursor + d)
		} else {
			t.setCacheCursor(t.cs.cursor + d)
		}
	}
	half := max(t.cacheListHeight()/2, 1)
	switch {
	case key.Matches(kp, ck.ClearCache):
		return t.clearCache()
	case key.Matches(kp, ck.PruneCache):
		return t.pruneCache()
	case key.Matches(kp, ck.RemoveOrphans):
		return t.removeOrphans()
	case key.Matches(kp, ck.Vacuum):
		return t.vacuum()
	case key.Matches(kp, ck.Open):
		t.drillIn()
	case key.Matches(kp, ck.Back), key.Matches(kp, t.keys.Input.Cancel):
		t.drillOut()
	case key.Matches(kp, l.GrowPane):
		t.split.Grow()
	case key.Matches(kp, l.ShrinkPane):
		t.split.Shrink()
	case key.Matches(kp, l.Up):
		move(-1)
	case key.Matches(kp, l.Down):
		move(1)
	case key.Matches(kp, l.Top):
		move(-1 << 20)
	case key.Matches(kp, l.Bottom):
		move(1 << 20)
	case key.Matches(kp, l.PageDown):
		move(half)
	case key.Matches(kp, l.PageUp):
		move(-half)
	}
	return nil
}

// drillIn breaks the selected type down by project.
func (t *Tab) drillIn() {
	s, ok := t.selectedType()
	if !ok || t.cs.drill != "" || len(s.Projects) == 0 {
		return
	}
	t.cs.drill, t.cs.pcursor, t.cs.ptop = s.Type, 0, 0
}

func (t *Tab) drillOut() { t.cs.drill = "" }

// --- actions ---------------------------------------------------------------------

// ready reports whether an action may start, or a toast explaining why not.
func (t *Tab) ready() (tea.Cmd, bool) {
	switch {
	case t.caches == nil:
		return info("cache controls need forge's database"), false
	case t.cs.busy:
		return info("still working on the last cache action"), false
	case !t.cs.loaded:
		return nil, false
	}
	return nil, true
}

// scope is the type and project an action applies to: the selected type,
// and the selected project while drilled in.
func (t *Tab) scope() (cachectl.Type, string, bool) {
	s, ok := t.selectedType()
	if !ok {
		return "", "", false
	}
	p, _ := t.selectedProject()
	return s.Type, p.Project, true
}

// clearCache plans clearing the selected type (or project); the docs index
// is only rebuilt, which needs no confirmation.
func (t *Tab) clearCache() tea.Cmd {
	if cmd, ok := t.ready(); !ok {
		return cmd
	}
	ty, project, ok := t.scope()
	if !ok {
		return nil
	}
	if ty == cachectl.Docs {
		return tea.Batch(
			func() tea.Msg { return tabs.ReindexDocsMsg{} },
			info("rebuilding the docs index"),
			t.loadCache(),
		)
	}
	return t.plan(cachectl.Op{Kind: cachectl.Clear, Type: ty, Project: project}, 0)
}

// pruneCache asks for N days, then plans removing older entries.
func (t *Tab) pruneCache() tea.Cmd {
	if cmd, ok := t.ready(); !ok {
		return cmd
	}
	ty, project, ok := t.scope()
	if !ok {
		return nil
	}
	if !ty.Prunable() {
		return info(ty.Label() + " has no age to prune by; " + t.keys.Context.ClearCache.Help().Key + " rebuilds it")
	}
	days := strconv.Itoa(defaultPruneDays)
	what := ty.Label()
	if project != "" {
		what += " for " + project
	}
	f := t.styled(huh.NewGroup(
		huh.NewInput().Key("days").Title("Prune " + what).
			Description("Remove entries older than this many days. You'll see what goes before anything is removed.").
			Value(&days).
			Validate(func(s string) error {
				if n, err := strconv.Atoi(strings.TrimSpace(s)); err != nil || n < 1 {
					return errors.New("a whole number of days, 1 or more")
				}
				return nil
			}),
	))
	return t.openForm(f, func() tea.Cmd {
		n, _ := strconv.Atoi(strings.TrimSpace(days))
		cut := t.now().Add(-time.Duration(n) * 24 * time.Hour)
		return t.plan(cachectl.Op{Kind: cachectl.Prune, Type: ty, Project: project, OlderThan: cut}, n)
	})
}

func (t *Tab) removeOrphans() tea.Cmd {
	if cmd, ok := t.ready(); !ok {
		return cmd
	}
	return t.plan(cachectl.Op{Kind: cachectl.Orphans}, 0)
}

func (t *Tab) vacuum() tea.Cmd {
	if cmd, ok := t.ready(); !ok {
		return cmd
	}
	return t.plan(cachectl.Op{Kind: cachectl.Vacuum}, 0)
}

// plan works out what op would remove, in a command.
func (t *Tab) plan(op cachectl.Op, days int) tea.Cmd {
	t.cs.busy = true
	c := t.caches
	return func() tea.Msg {
		ctx, cancel := stdctx.WithTimeout(stdctx.Background(), cacheTimeout)
		defer cancel()
		p, err := c.Plan(ctx, op)
		return cachePlanMsg{plan: p, err: err, days: days}
	}
}

// planned asks for confirmation, or says there is nothing to do.
func (t *Tab) planned(msg cachePlanMsg) tea.Cmd {
	t.cs.busy = false
	switch {
	case errors.Is(msg.err, cachectl.ErrNotPlannable):
		return info(msg.plan.Op.Type.Label() + ": nothing to remove")
	case msg.err != nil:
		return errCmd(fmt.Errorf("cache: %w", msg.err))
	case msg.plan.Empty():
		text := "nothing to remove"
		if kept := cachectl.SkipSummary(msg.plan.Skipped); kept != "" {
			text += " (kept: " + kept + ")"
		}
		return info(text)
	}
	title, desc, affirm := confirmText(msg.plan, msg.days)
	yes := false
	f := t.styled(huh.NewGroup(
		huh.NewConfirm().Key("confirm").Title(title).Description(desc).
			Affirmative(affirm).Negative("Keep").Value(&yes),
	))
	p := msg.plan
	return t.openForm(f, func() tea.Cmd {
		if !yes {
			return nil
		}
		return t.apply(p)
	})
}

func (t *Tab) apply(p cachectl.Plan) tea.Cmd {
	t.cs.busy = true
	c := t.caches
	return func() tea.Msg {
		ctx, cancel := stdctx.WithTimeout(stdctx.Background(), cacheTimeout)
		defer cancel()
		res, err := c.Apply(ctx, p)
		return cacheDoneMsg{res: res, err: err}
	}
}

func (t *Tab) applied(msg cacheDoneMsg) tea.Cmd {
	t.cs.busy = false
	reload := t.loadCache()
	if msg.err != nil {
		return tea.Batch(errCmd(fmt.Errorf("cache: %w", msg.err)), reload)
	}
	text, ok := resultText(msg.res)
	if !ok {
		return tea.Batch(info(text), reload)
	}
	return tea.Batch(success(text), reload)
}

// noun names n items of a type: "3 entries", "1 worktree".
func noun(t cachectl.Type, n int) string {
	one, many := "entry", "entries"
	switch t {
	case cachectl.Artifacts:
		one, many = "copy", "copies"
	case cachectl.Worktrees:
		one, many = "worktree", "worktrees"
	case cachectl.Archived:
		one, many = "session", "sessions"
	}
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// typeNote says what removing items of t means, beyond the numbers.
func typeNote(t cachectl.Type) string {
	switch t {
	case cachectl.GitHub, cachectl.Plane:
		return "Cached API responses; tabs fetch them again on the next refresh."
	case cachectl.Transcripts:
		return "Parsed transcript snapshots; transcripts are reparsed from Claude Code's files when opened."
	case cachectl.Artifacts:
		return "Deletes the copied files from .forge/artifacts. Only detached copies go; pinned and linked ones stay."
	case cachectl.Worktrees:
		return "Only orphaned worktrees under the worktree root, with no uncommitted changes. Branches are kept."
	case cachectl.Archived:
		return "Deletes the sessions' rows, events and data folders, and removes clean worktrees still on disk. Branches are kept."
	}
	return ""
}

// confirmText is the confirmation's title, description and yes button.
func confirmText(p cachectl.Plan, days int) (title, desc, affirm string) {
	op := p.Op
	if op.Kind == cachectl.Vacuum {
		return "Vacuum " + dbName(p.DB.Path) + "?",
			fmt.Sprintf("The file is %s (WAL %s); about %s is free pages that a rebuild gives back.\nWrites wait while it runs.",
				ui.HumanBytes(p.DB.Bytes), ui.HumanBytes(p.DB.WAL), ui.HumanBytes(p.DB.Free)),
			"Vacuum"
	}
	what := op.Type.Label()
	if op.Project != "" {
		what += " for " + op.Project
	}
	n := noun(op.Type, len(p.Items))
	switch op.Kind {
	case cachectl.Prune:
		title = fmt.Sprintf("Remove %s from %s older than %d days?", n, what, days)
	case cachectl.Orphans:
		title = fmt.Sprintf("Remove %s?", strings.Replace(n, "worktree", "orphaned worktree", 1))
	default:
		title = fmt.Sprintf("Clear %s: %s?", what, n)
	}
	lines := []string{ui.HumanBytes(p.Bytes()) + " will be freed."}
	if note := typeNote(op.Type); note != "" {
		lines = append(lines, note)
	}
	if op.Type == cachectl.Worktrees || op.Type == cachectl.Archived || op.Type == cachectl.Artifacts {
		const shown = 6
		for i, it := range p.Items {
			if i == shown {
				lines = append(lines, fmt.Sprintf("  … and %d more", len(p.Items)-shown))
				break
			}
			lines = append(lines, "  "+it.Label+" ("+ui.HumanBytes(it.Bytes)+")")
		}
	}
	if kept := cachectl.SkipSummary(p.Skipped); kept != "" {
		lines = append(lines, "Kept: "+kept+".")
	}
	affirm = "Remove"
	if op.Kind == cachectl.Clear {
		affirm = "Clear"
	}
	return title, strings.Join(lines, "\n"), affirm
}

// resultText describes what Apply did; ok is false when nothing was
// removed.
func resultText(r cachectl.Result) (string, bool) {
	if r.Op.Kind == cachectl.Vacuum {
		return fmt.Sprintf("vacuumed: %s → %s", ui.HumanBytes(r.DBBefore), ui.HumanBytes(r.DBAfter)), true
	}
	ty := r.Op.Type
	if r.Op.Kind == cachectl.Orphans {
		ty = cachectl.Worktrees
	}
	text := fmt.Sprintf("removed %s, freed %s", noun(ty, r.Removed), ui.HumanBytes(r.Bytes))
	if kept := cachectl.SkipSummary(r.Skipped); kept != "" {
		text += "; kept " + kept
	}
	return text, r.Removed > 0
}

func dbName(path string) string {
	if path == "" {
		return "the database"
	}
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// --- view ------------------------------------------------------------------------

// Column widths of the cache table.
const (
	colEntries = 8
	colSize    = 9
	colAge     = 7
)

func (t *Tab) age(at time.Time) string {
	if at.IsZero() {
		return "—"
	}
	return ui.ShortAge(t.now().Sub(at))
}

// cacheRow lays out label | entries | size | oldest in w columns.
func cacheRow(label, entries, size, age string, w int) string {
	right := fmt.Sprintf("%*s%*s%*s", colEntries, entries, colSize, size, colAge, age)
	space := w - ansi.StringWidth(right)
	if space < 6 {
		return ansi.Truncate(label, w, "…")
	}
	label = ansi.Truncate(label, space, "…")
	return label + strings.Repeat(" ", max(space-ansi.StringWidth(label), 0)) + right
}

func (t *Tab) cacheListView(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	th := t.theme
	switch {
	case t.caches == nil:
		return th.Hint.Render(ansi.Wrap("Cache controls need forge's database; it isn't open in this run.", w, ""))
	case !t.cs.loaded:
		return th.Hint.Render("Measuring what forge stores…")
	}
	var header string
	var lines []string
	if t.cs.drill == "" {
		header = cacheRow("  Type", "Entries", "Size", "Oldest", w)
		end := min(t.cs.top+t.cacheListHeight(), len(t.cacheTypes()))
		for i := t.cs.top; i < end; i++ {
			lines = append(lines, t.typeRow(t.cacheTypes()[i], i == t.cs.cursor, w))
		}
	} else {
		s := t.cs.report.Type(t.cs.drill)
		header = cacheRow("‹ "+s.Type.Label(), "Entries", "Size", "Oldest", w)
		end := min(t.cs.ptop+t.cacheListHeight(), len(s.Projects))
		for i := t.cs.ptop; i < end; i++ {
			lines = append(lines, t.projectRow(s, s.Projects[i], i == t.cs.pcursor, w))
		}
	}
	body := t.cacheListHeight()
	for len(lines) < body {
		lines = append(lines, "")
	}
	return th.DetailLabel.Render(header) + "\n" + strings.Join(lines[:body], "\n") + "\n\n" + t.dbFooter(w)
}

// orphans is the orphaned worktrees marker of a row: " · 2 orphans".
func orphans(n int) string {
	switch n {
	case 0:
		return ""
	case 1:
		return " · 1 orphan"
	}
	return fmt.Sprintf(" · %d orphans", n)
}

func sizeText(s cachectl.TypeStat) string {
	if s.InMemory {
		return "mem"
	}
	return ui.HumanBytes(s.Bytes)
}

func (t *Tab) typeRow(s cachectl.TypeStat, selected bool, w int) string {
	th := t.theme
	label := "  " + s.Type.Label() + orphans(s.Orphans)
	if s.Err != nil {
		label += " ⚠"
	}
	line := cacheRow(label, strconv.Itoa(s.Entries), sizeText(s), t.age(s.Oldest), w)
	switch {
	case selected:
		return th.TableSelected.Render(line)
	case s.Entries == 0:
		return th.Hint.Render(line)
	}
	return th.TableRow.Render(line)
}

func (t *Tab) projectRow(s cachectl.TypeStat, p cachectl.ProjectStat, selected bool, w int) string {
	th := t.theme
	label := "  " + p.Project + orphans(p.Orphans)
	size := ui.HumanBytes(p.Bytes)
	if s.InMemory {
		size = "mem"
	}
	line := cacheRow(label, strconv.Itoa(p.Entries), size, t.age(p.Oldest), w)
	if selected {
		return th.TableSelected.Render(line)
	}
	return th.TableRow.Render(line)
}

// dbFooter is the database file's size under the list.
func (t *Tab) dbFooter(w int) string {
	db := t.cs.report.DB
	total := dbName(db.Path) + " " + ui.HumanBytes(db.Bytes)
	rest := fmt.Sprintf(" · WAL %s · %s free", ui.HumanBytes(db.WAL), ui.HumanBytes(db.Free))
	if t.cs.loading {
		rest += " · measuring…"
	}
	return t.theme.DetailTitle.Render(ansi.Truncate(total, w, "…")) +
		t.theme.Hint.Render(ansi.Truncate(rest, max(w-ansi.StringWidth(total), 0), ""))
}

// storedIn says where each type lives.
func storedIn(ty cachectl.Type) string {
	switch ty {
	case cachectl.GitHub:
		return "forge.db (kv_cache): PR lists, PR details, workflow runs and CI error summaries."
	case cachectl.Plane:
		return "forge.db (kv_cache): Plane work items, states, labels, cycles and members."
	case cachectl.Transcripts:
		return "forge.db (kv_cache): parsed transcripts, so reopening a session only reads new lines."
	case cachectl.Docs:
		return "Memory only: the Docs tab's search index over each project's docs folder. Size is what it reads."
	case cachectl.Artifacts:
		return "<checkout>/.forge/artifacts: copies kept from session worktrees, plus their index rows."
	case cachectl.Worktrees:
		return "The sessions' worktree root, one git worktree per agent session."
	case cachectl.Archived:
		return "forge.db (sessions, events, links) and the per-session data folder; sometimes a worktree."
	}
	return ""
}

func (t *Tab) cacheDetailLines(s cachectl.TypeStat, w int) []string {
	th, ck := t.theme, t.keys.Context
	var out []string
	section := func(title string) { out = append(out, "", th.DetailLabel.Render(title)) }
	para := func(s string) { out = append(out, strings.Split(ansi.Wrap(s, w, ""), "\n")...) }
	hint := func(s string) { out = append(out, th.Hint.Render(ansi.Wrap(s, w, ""))) }

	para(storedIn(s.Type))
	if s.Err != nil {
		section("Couldn't measure everything")
		out = append(out, th.TableEmpty.Render(ansi.Wrap(s.Err.Error(), w, "/")))
	}
	section("Actions")
	switch s.Type {
	case cachectl.Docs:
		hint(ck.ClearCache.Help().Key + "  rebuild the index (nothing on disk is touched)")
	default:
		hint(ck.ClearCache.Help().Key + "  clear " + t.clearWhat())
		hint(ck.PruneCache.Help().Key + "  prune entries older than N days")
		if note := typeNote(s.Type); note != "" {
			out = append(out, "")
			hint(note)
		}
	}
	if s.Type == cachectl.Worktrees {
		hint(ck.RemoveOrphans.Help().Key + "  remove orphaned worktrees (every project)")
	}
	if t.cs.drill == "" && len(s.Projects) > 0 {
		hint(ck.Open.Help().Key + "  break down by project")
	}
	section("Database")
	hint(ck.Vacuum.Help().Key + "  vacuum " + dbName(t.cs.report.DB.Path) + " to give back free pages")
	return out
}

// clearWhat is what the clear key clears right now.
func (t *Tab) clearWhat() string {
	if p, ok := t.selectedProject(); ok {
		return "this type for " + p.Project
	}
	return "this type for every project"
}

func (t *Tab) cacheDetailView(w, h int) string {
	if w <= 0 || h <= 0 || !t.cs.loaded {
		return ""
	}
	th := t.theme
	s, ok := t.selectedType()
	if !ok {
		return ""
	}
	title := s.Type.Label()
	entries, size, oldest, orph := s.Entries, sizeText(s), s.Oldest, s.Orphans
	if p, ok := t.selectedProject(); ok {
		title += " · " + p.Project
		entries, oldest, orph = p.Entries, p.Oldest, p.Orphans
		if !s.InMemory {
			size = ui.HumanBytes(p.Bytes)
		}
	}
	info := fmt.Sprintf("%s · %s", noun(s.Type, entries), size)
	if orph > 0 {
		info += fmt.Sprintf(" · %d orphaned", orph)
	}
	if !oldest.IsZero() {
		info += " · oldest " + t.age(oldest)
	}
	header := th.DetailTitle.Render(ansi.Truncate(title, w, "…")) + "\n" +
		th.Hint.Render(ansi.Truncate(info, w, "…")) + "\n"
	all := t.cacheDetailLines(s, w)
	body := max(h-detailHeader, 1)
	return header + strings.Join(all[:min(body, len(all))], "\n")
}

// cacheStatus is the status bar detail in the cache view.
func (t *Tab) cacheStatus() string {
	if !t.cs.loaded {
		return ""
	}
	var total int64
	for _, s := range t.cacheTypes() {
		if !s.InMemory {
			total += s.Bytes
		}
	}
	return fmt.Sprintf("forge stores %s · db %s", ui.HumanBytes(total), ui.HumanBytes(t.cs.report.DB.Bytes))
}
