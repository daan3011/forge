package plane

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
)

// The detail pane shows the selected work item. Its header comes from the
// list row, so it follows the cursor at once; the description, comments
// and linked sessions and PRs are fetched once the cursor has rested for
// detailDebounce. Details are kept in memory by item + updated_at.

const (
	detailDebounce = 150 * time.Millisecond
	// detailMemCap is how many items' details stay in memory.
	detailMemCap = 64
)

// detailKey identifies one version of an item: "<id>@<updated_at>".
func detailKey(w plane.WorkItem) string {
	return w.ID + "@" + w.UpdatedAt.UTC().Format(time.RFC3339Nano)
}

// detailEntry is one fetched version of an item's description and
// comments, as Markdown, and their rendering for mdWidth (0 = not yet).
type detailEntry struct {
	id, key     string
	description string // Markdown
	comments    []plane.Comment
	commentMD   []string // Markdown per comment
	commentsErr error

	mdWidth   int
	descR     string
	commentsR []string
}

// linkEntry is what forge links to an item (P1.9).
type linkEntry struct {
	sessions []store.Session
	prs      []store.Link
	err      error
}

type detailState struct {
	offset int
	seq    int    // debounce generation
	want   string // detailKey of the selection last scheduled
	wantID string

	mem      map[string]*detailEntry // by key
	order    []string                // mem keys, oldest first
	latest   map[string]*detailEntry // newest entry per item id, also stale ones
	inflight map[string]bool
	failed   map[string]error

	rendering string // "key@width" being rendered

	links map[string]linkEntry // by item key (PROJ-12)
}

func newDetailState() detailState {
	return detailState{
		mem: map[string]*detailEntry{}, latest: map[string]*detailEntry{},
		inflight: map[string]bool{}, failed: map[string]error{},
		links: map[string]linkEntry{},
	}
}

// remember stores e, evicting the oldest entries past detailMemCap.
func (d *detailState) remember(e *detailEntry) {
	if _, ok := d.mem[e.key]; !ok {
		d.order = append(d.order, e.key)
	}
	d.mem[e.key] = e
	d.latest[e.id] = e
	for len(d.order) > detailMemCap {
		old := d.mem[d.order[0]]
		delete(d.mem, d.order[0])
		d.order = d.order[1:]
		if old != nil && d.latest[old.id] == old {
			delete(d.latest, old.id)
		}
	}
}

// entry is the details to show for w: its exact version, else the newest
// one fetched (shown while the new one loads).
func (d *detailState) entry(w plane.WorkItem) *detailEntry {
	if e := d.mem[detailKey(w)]; e != nil {
		return e
	}
	return d.latest[w.ID]
}

// --- messages ------------------------------------------------------------------------

type detailDueMsg struct{ seq int }

type detailLoadedMsg struct {
	id, key     string
	item        plane.WorkItem
	comments    []plane.Comment
	err         error
	commentsErr error
}

type detailRenderedMsg struct {
	key      string
	width    int
	desc     string
	comments []string
}

type linksLoadedMsg struct {
	key  string
	link linkEntry
}

// TargetTab implements tabs.TargetedMsg.
func (detailDueMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (detailLoadedMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (detailRenderedMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (linksLoadedMsg) TargetTab() string { return Title }

func (t *Tab) updateDetail(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case detailDueMsg:
		return t.detailDue(msg), true
	case detailLoadedMsg:
		return t.detailLoaded(msg), true
	case detailRenderedMsg:
		t.detailRendered(msg)
		return nil, true
	case linksLoadedMsg:
		t.det.links[msg.key] = msg.link
		return nil, true
	}
	return nil, false
}

// --- scheduling ------------------------------------------------------------------------

// syncDetail notices a new selection (or a new version of it) and arms the
// debounce.
func (t *Tab) syncDetail() tea.Cmd {
	d := &t.det
	w, ok := t.selected()
	if dw, _ := t.detailSize(); dw <= 0 {
		ok = false // nothing shows it (the board without its overlay)
	}
	if !ok {
		d.want, d.wantID = "", ""
		return nil
	}
	k := detailKey(w)
	if k == d.want {
		return nil
	}
	if w.ID != d.wantID {
		d.offset = 0
	}
	d.want, d.wantID = k, w.ID
	d.seq++
	seq := d.seq
	return t.tick(detailDebounce, func(time.Time) tea.Msg { return detailDueMsg{seq: seq} })
}

// detailDue runs once the cursor rested: linked sessions always (they
// change without the item changing), details unless known or on their way.
func (t *Tab) detailDue(msg detailDueMsg) tea.Cmd {
	d := &t.det
	if msg.seq != d.seq {
		return nil // the cursor moved on
	}
	w, ok := t.selected()
	if !ok || detailKey(w) != d.want {
		return nil
	}
	cmds := []tea.Cmd{t.loadLinks(w.Key())}
	k := d.want
	if d.mem[k] == nil && !d.inflight[k] && t.api != nil {
		delete(d.failed, k)
		d.inflight[k] = true
		cmds = append(cmds, t.loadDetail(w))
	}
	return tea.Batch(cmds...)
}

// loadDetail fetches the item (for its description) and its comments
// together, off the update loop.
func (t *Tab) loadDetail(w plane.WorkItem) tea.Cmd {
	api := t.api
	id, key, project := w.ID, detailKey(w), w.ProjectID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), shortTimeout)
		defer cancel()
		msg := detailLoadedMsg{id: id, key: key}
		var wg sync.WaitGroup
		wg.Go(func() { msg.item, msg.err = api.WorkItem(ctx, project, id) })
		wg.Go(func() { msg.comments, msg.commentsErr = api.Comments(ctx, project, id) })
		wg.Wait()
		return msg
	}
}

func (t *Tab) detailLoaded(msg detailLoadedMsg) tea.Cmd {
	d := &t.det
	delete(d.inflight, msg.key)
	if msg.err != nil {
		d.failed[msg.key] = msg.err
		if msg.key != d.want {
			return nil // nobody is looking at it any more
		}
		name := msg.id
		if w, ok := t.itemByID(msg.id); ok {
			name = w.Key()
		}
		return errCmd(fmt.Errorf("plane: %s: %s", name, reason(msg.err)))
	}
	e := &detailEntry{
		id: msg.id, key: msg.key,
		description: plane.HTMLToMarkdown(msg.item.DescriptionHTML),
		commentsErr: msg.commentsErr,
	}
	cs := slices.Clone(msg.comments)
	slices.SortStableFunc(cs, func(a, b plane.Comment) int { return a.CreatedAt.Compare(b.CreatedAt.Time) })
	e.comments = cs
	for _, c := range cs {
		md := plane.HTMLToMarkdown(c.CommentHTML)
		if md == "" {
			md = c.CommentStripped
		}
		e.commentMD = append(e.commentMD, md)
	}
	d.remember(e)
	return nil
}

// loadLinks reads the sessions and PRs forge links to the item.
func (t *Tab) loadLinks(key string) tea.Cmd {
	api := t.sessions
	if api == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		ss, prs, err := api.IssueLinks(ctx, key)
		return linksLoadedMsg{key: key, link: linkEntry{sessions: ss, prs: prs, err: err}}
	}
}

// --- Markdown ----------------------------------------------------------------------------

// ensureRendered renders the shown entry's Markdown for the pane's width
// in the background; until then the view wraps the plain Markdown.
func (t *Tab) ensureRendered() tea.Cmd {
	d := &t.det
	w, ok := t.selected()
	width, _ := t.detailSize()
	if !ok || width <= 0 {
		return nil
	}
	e := d.entry(w)
	if e == nil || e.mdWidth == width {
		return nil
	}
	tag := fmt.Sprintf("%s@%d", e.key, width)
	if d.rendering == tag {
		return nil
	}
	d.rendering = tag
	key, style := e.key, t.theme.GlamourStyle()
	texts := append([]string{e.description}, e.commentMD...)
	return func() tea.Msg {
		out := ui.RenderMarkdown(style, width, texts)
		return detailRenderedMsg{key: key, width: width, desc: out[0], comments: out[1:]}
	}
}

func (t *Tab) detailRendered(msg detailRenderedMsg) {
	d := &t.det
	if d.rendering == fmt.Sprintf("%s@%d", msg.key, msg.width) {
		d.rendering = ""
	}
	e := d.mem[msg.key]
	if e == nil {
		return
	}
	e.mdWidth, e.descR, e.commentsR = msg.width, msg.desc, msg.comments
}

// scrollDetail moves the pane by half its height; the view clamps it.
func (t *Tab) scrollDetail(dir int) {
	_, h := t.detailSize()
	t.det.offset = max(t.det.offset+dir*max(h/2, 1), 0)
}

// --- view --------------------------------------------------------------------------------

// detailSize is the inner size of wherever the detail shows: the right
// pane, the board's overlay, or nowhere (0, 0) on the board without it.
func (t *Tab) detailSize() (w, h int) {
	if t.mode == modeBoard {
		if !t.overlay {
			return 0, 0
		}
		return t.overlaySize()
	}
	return t.split.RightSize()
}

// detailView is the detail at w x h: the header, then the description,
// linked sessions and PRs, and comments, scrolling under the header.
func (t *Tab) detailView(w, h int) string {
	th := t.theme
	if w <= 0 || h <= 0 {
		return ""
	}
	item, ok := t.selected()
	if !ok {
		return th.Hint.Render("no issue selected")
	}
	head := t.header(item, w)
	content := t.body(item, w)
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

// header is "KEY · project", the title on a band, the state, priority and
// assignees, labels, and cycle and ages.
func (t *Tab) header(item plane.WorkItem, w int) []string {
	th := t.theme
	now := t.now()
	top := item.Key()
	if p := t.projectLabel(item.ProjectID); p != "" {
		top += " · " + p
	}
	lines := []string{th.PRRepo.Render(ansi.Truncate(top, w, "…"))}
	for _, l := range strings.Split(ansi.Wrap(item.Name, max(w-2, 1), ""), "\n") {
		lines = append(lines, th.DetailBand.Render(" "+l+strings.Repeat(" ", max(w-2-ansi.StringWidth(l), 0))+" "))
	}
	lines = append(lines, "")

	name, group := t.stateOf(item)
	if name == "" {
		name = "unknown state"
	}
	glyph, pst := th.PriorityIcon(string(item.Priority))
	prio := string(item.Priority)
	if prio == "" {
		prio = string(plane.PriorityNone)
	}
	meta := th.StateGroupStyle(string(group)).Render("● "+name) + "  " + pst.Render(glyph+" "+prio)
	if who := t.assigneeNames(item); len(who) > 0 {
		meta += th.Hint.Render("  → ") + th.Body.Render(strings.Join(who, ", "))
	} else {
		meta += th.Hint.Render("  → unassigned")
	}
	lines = append(lines, strings.Split(ansi.Wrap(meta, max(w, 1), ""), "\n")...)

	if chips := t.labelChips(item); len(chips) > 0 {
		lines = append(lines, strings.Split(ansi.Wrap(strings.Join(chips, " "), max(w, 1), ""), "\n")...)
	}
	var facts []string
	if c := t.cycleName(item); c != "" {
		facts = append(facts, "cycle "+c)
	}
	if !item.TargetDate.IsZero() {
		facts = append(facts, "due "+item.TargetDate.Format("2 Jan"))
	}
	if !item.UpdatedAt.IsZero() {
		facts = append(facts, "updated "+ui.ShortAge(now.Sub(item.UpdatedAt.Time))+" ago")
	}
	if !item.CreatedAt.IsZero() {
		facts = append(facts, "created "+ui.ShortAge(now.Sub(item.CreatedAt.Time))+" ago")
	}
	if len(facts) > 0 {
		lines = append(lines, strings.Split(ansi.Wrap(th.Hint.Render(strings.Join(facts, " · ")), max(w, 1), ""), "\n")...)
	}
	return append(lines, "")
}

// labelChips are the item's labels in their own colours.
func (t *Tab) labelChips(item plane.WorkItem) []string {
	labels := item.Labels
	if len(labels) == 0 {
		if m := t.meta[item.ProjectID]; m != nil {
			for _, id := range item.LabelIDs {
				for _, l := range m.labels {
					if l.ID == id {
						labels = append(labels, l)
					}
				}
			}
		}
	}
	out := make([]string, len(labels))
	for i, l := range labels {
		out[i] = t.theme.LabelChip(l.Color).Render(l.Name)
	}
	return out
}

func (t *Tab) cycleName(item plane.WorkItem) string {
	if item.Cycle != nil {
		return item.Cycle.Name
	}
	if item.CycleID == "" {
		return ""
	}
	if m := t.meta[item.ProjectID]; m != nil {
		for _, c := range m.cycles {
			if c.ID == item.CycleID {
				return c.Name
			}
		}
	}
	return ""
}

// body is the scrolling part: description, linked, comments.
func (t *Tab) body(item plane.WorkItem, w int) []string {
	th := t.theme
	var lines []string
	section := func(title string, body ...string) {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, th.DetailLabel.Bold(true).Render(title))
		lines = append(lines, body...)
	}
	e := t.det.entry(item)
	switch {
	case e != nil:
		section("Description", t.markdown(e.descR, e.description, e.mdWidth == w, w, "no description")...)
	default:
		section("Description", t.pending(item, w)...)
	}
	if t.sessions != nil {
		section("Linked", t.linkLines(item, w)...)
	}
	if e != nil {
		section(commentsTitle(e), t.commentLines(item, e, w)...)
	}
	return lines
}

func commentsTitle(e *detailEntry) string {
	if n := len(e.comments); n > 0 {
		return fmt.Sprintf("Comments (%d)", n)
	}
	return "Comments"
}

// pending is what's shown where details aren't there yet: the error, or
// the loading hammer (P6.4).
func (t *Tab) pending(item plane.WorkItem, w int) []string {
	if err := t.det.failed[detailKey(item)]; err != nil {
		return []string{t.theme.DiffDel.Render("couldn't load details: " + reason(err))}
	}
	if t.api == nil {
		return []string{t.theme.Hint.Render("Plane isn't set up")}
	}
	t.detailLoading = true
	return anim.LoadingLines("heating up…", t.anim.Frame(), t.anim.Settings(), t.theme, w)
}

// markdown is rendered Markdown when ready, else the Markdown wrapped.
func (t *Tab) markdown(rendered, plain string, ready bool, w int, empty string) []string {
	if strings.TrimSpace(plain) == "" {
		return []string{t.theme.Hint.Render(empty)}
	}
	if ready && rendered != "" {
		return strings.Split(rendered, "\n")
	}
	return strings.Split(t.theme.Body.Render(ansi.Wrap(plain, max(w, 1), "")), "\n")
}

// linkLines lists the sessions and PRs forge links to the item.
func (t *Tab) linkLines(item plane.WorkItem, w int) []string {
	th := t.theme
	l, ok := t.det.links[item.Key()]
	switch {
	case !ok:
		return []string{th.Hint.Render("…")}
	case l.err != nil:
		return []string{th.DiffDel.Render("couldn't load links: " + l.err.Error())}
	case len(l.sessions) == 0 && len(l.prs) == 0:
		return []string{th.Hint.Render("no sessions or PRs")}
	}
	now := t.now()
	var out []string
	ss := slices.Clone(l.sessions)
	slices.SortStableFunc(ss, func(a, b store.Session) int { return b.CreatedAt.Compare(a.CreatedAt) })
	for _, s := range ss {
		status := string(s.Status)
		if s.Archived() {
			status = string(store.StatusArchived)
		}
		line := th.SessionStatus(status).Render("▸ ") + th.Body.Render(s.Name) + " " + th.SessionStatus(status).Render(status)
		if !s.CreatedAt.IsZero() {
			line += th.Hint.Render(" · " + ui.ShortAge(now.Sub(s.CreatedAt)) + " ago")
		}
		out = append(out, ansi.Truncate(line, w, "…"))
	}
	for _, p := range l.prs {
		id := p.Other.ID
		if _, n, ok := strings.Cut(id, "#"); ok {
			id = "#" + n
		}
		line := th.PRRepo.Render("⇄ "+id) + " " + th.Body.Render(p.Title)
		if p.State != "" {
			line += " " + th.Hint.Render("["+p.State+"]")
		}
		out = append(out, ansi.Truncate(line, w, "…"))
	}
	return out
}

// commentLines lists comments oldest first: author and age, then the body.
func (t *Tab) commentLines(item plane.WorkItem, e *detailEntry, w int) []string {
	th := t.theme
	var out []string
	if e.commentsErr != nil {
		out = append(out, th.DiffDel.Render("couldn't load comments: "+reason(e.commentsErr)))
	}
	if len(e.comments) == 0 {
		if e.commentsErr == nil {
			out = append(out, th.Hint.Render("no comments"))
		}
		return out
	}
	now := t.now()
	ready := e.mdWidth == w
	for i, c := range e.comments {
		if i > 0 {
			out = append(out, "")
		}
		who := th.Body.Bold(true).Render(t.memberName(item.ProjectID, c.ActorID))
		if !c.CreatedAt.IsZero() {
			who += th.Hint.Render(" · " + ui.ShortAge(now.Sub(c.CreatedAt.Time)) + " ago")
		}
		if !c.EditedAt.IsZero() {
			who += th.Hint.Render(" (edited)")
		}
		out = append(out, who)
		rendered := ""
		if i < len(e.commentsR) {
			rendered = e.commentsR[i]
		}
		out = append(out, t.markdown(rendered, e.commentMD[i], ready, w, "(empty)")...)
	}
	return out
}
