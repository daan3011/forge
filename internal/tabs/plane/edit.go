package plane

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/daan3011/forge/internal/capture"
	"github.com/daan3011/forge/internal/editor"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

// Create & edit (P3.4): `n` opens a form for a new work item, `e` the same
// form for the selected one, `c` a comment form, `s` the Sessions tab's
// new-session form for the item. Descriptions and comments are written as
// Markdown (ctrl+e opens $EDITOR) and sent to Plane as HTML.

// formKind is the form a pendingForm opens.
type formKind int

const (
	formCreate formKind = iota + 1
	formEdit
)

// pendingForm is a create or edit form waiting for the project's
// metadata (its options) and, to edit, the full item (its description).
type pendingForm struct {
	kind    formKind
	project string
	itemID  string
	item    *plane.WorkItem
	draft   *IssueDraft // prefills a create form
}

// IssueDraft prefills the create form: quick capture's shift+enter (P3.5)
// opens it with what the line said.
type IssueDraft struct {
	Title       string
	Priority    plane.Priority
	LabelIDs    []string
	AssigneeIDs []string // the first is used; the form has one assignee
}

// values are the form's starting values for d in a project whose default
// state is state.
func (d *IssueDraft) values(state string) issueValues {
	v := issueValues{state: state, priority: plane.PriorityNone}
	if d == nil {
		return v
	}
	v.title = d.Title
	if d.Priority != "" {
		v.priority = d.Priority
	}
	v.labels = slices.Clone(d.LabelIDs)
	if len(d.AssigneeIDs) > 0 {
		v.assignee = d.AssigneeIDs[0]
	}
	return v
}

// issueValues are the form's fields.
type issueValues struct {
	title    string
	state    string
	priority plane.Priority
	labels   []string
	assignee string // user id, "" = unassigned
	cycle    string // cycle id, "" = none
	desc     string // Markdown
}

// fullItemMsg carries the item to edit, with its description.
type fullItemMsg struct {
	id   string
	item plane.WorkItem
	err  error
}

// createdMsg settles CreateWorkItem.
type createdMsg struct {
	project string
	item    plane.WorkItem
	err     error
}

// updatedMsg settles UpdateWorkItem from the edit form.
type updatedMsg struct {
	key  string
	item plane.WorkItem
	err  error
}

// commentedMsg settles CreateComment.
type commentedMsg struct {
	id, key string
	err     error
}

// TargetTab implements tabs.TargetedMsg.
func (fullItemMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (createdMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (updatedMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (commentedMsg) TargetTab() string { return Title }

func (t *Tab) updateEdit(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case fullItemMsg:
		return t.fullItemLoaded(msg), true
	case createdMsg:
		return t.created(msg), true
	case updatedMsg:
		return t.updated(msg), true
	case commentedMsg:
		return t.commented(msg), true
	}
	return nil, false
}

// itemAction handles the create & edit keys, in the list, on the board
// and in the card overlay alike.
func (t *Tab) itemAction(kp tea.KeyPressMsg) (tea.Cmd, bool) {
	pk := t.keys.Plane
	switch {
	case key.Matches(kp, pk.New):
		return t.newItem(), true
	case key.Matches(kp, pk.Edit):
		return t.editSelected(), true
	case key.Matches(kp, pk.Comment):
		return t.commentSelected(), true
	case key.Matches(kp, pk.Session):
		return t.startSession(), true
	}
	return nil, false
}

// --- opening ---------------------------------------------------------------------

// newItem opens the create form for the listed project, asking which one
// first when several are listed.
func (t *Tab) newItem() tea.Cmd {
	if t.api == nil {
		return errCmd(errors.New("plane isn't set up"))
	}
	ids := t.projectIDs()
	switch len(ids) {
	case 0:
		return errCmd(errors.New("plane: no Plane project linked to this project"))
	case 1:
		return t.prepareForm(formCreate, ids[0], "", nil)
	}
	pick := ids[0]
	opts := make([]huh.Option[string], len(ids))
	for i, id := range ids {
		name := t.projectLabel(id)
		if name == "" {
			name = id
		}
		opts[i] = huh.NewOption(name, id)
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Key("project").Title("New issue in").Options(opts...).Value(&pick),
	))
	return t.openDialog(form, func() tea.Cmd { return t.prepareForm(formCreate, pick, "", nil) })
}

// NewIssue opens the create form for Plane project project, prefilled
// from d (quick capture's shift+enter).
func (t *Tab) NewIssue(project string, d IssueDraft) tea.Cmd {
	if t.api == nil {
		return errCmd(errors.New("plane isn't set up"))
	}
	t.dialog, t.onDone = nil, nil // a capture replaces whatever form was open
	return t.prepareForm(formCreate, project, "", &d)
}

// editSelected opens the edit form for the selected item.
func (t *Tab) editSelected() tea.Cmd {
	w, ok := t.selected()
	if !ok || t.api == nil {
		return nil
	}
	return t.prepareForm(formEdit, w.ProjectID, w.ID, nil)
}

// prepareForm fetches what the form needs and opens it once everything
// has arrived (at once when it is all known).
func (t *Tab) prepareForm(kind formKind, project, itemID string, draft *IssueDraft) tea.Cmd {
	t.queued = &pendingForm{kind: kind, project: project, itemID: itemID, draft: draft}
	cmds := []tea.Cmd{t.loadMeta(project)}
	if kind == formEdit {
		api := t.api
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), shortTimeout)
			defer cancel()
			w, err := api.WorkItem(ctx, project, itemID)
			return fullItemMsg{id: itemID, item: w, err: err}
		})
	}
	if cmd := t.openPending(); cmd != nil {
		return cmd
	}
	what := "project details"
	if kind == formEdit {
		what = "the issue"
		if w, ok := t.itemByID(itemID); ok {
			what = w.Key()
		}
	}
	cmds = append(cmds, func() tea.Msg { return ui.InfoMsg{Text: "loading " + what + "…"} })
	return tea.Batch(cmds...)
}

func (t *Tab) fullItemLoaded(msg fullItemMsg) tea.Cmd {
	p := t.queued
	if p == nil || p.itemID != msg.id {
		return nil
	}
	if msg.err != nil {
		t.queued = nil
		return errCmd(fmt.Errorf("plane: can't edit: %s", reason(msg.err)))
	}
	w := msg.item
	p.item = &w
	return t.openPending()
}

// openPending opens the pending form once its metadata (and item) are in.
func (t *Tab) openPending() tea.Cmd {
	p := t.queued
	if p == nil {
		return nil
	}
	m := t.meta[p.project]
	if m == nil || t.metaBusy[p.project] || p.kind == formEdit && p.item == nil {
		return nil
	}
	t.queued = nil
	if t.dialog != nil {
		return nil // another form was opened meanwhile
	}
	states := t.orderedStates(p.project)
	if len(states) == 0 {
		return errCmd(errors.New("plane: the project's states didn't load"))
	}
	if p.kind == formCreate {
		return t.openCreate(p.project, m, states, p.draft)
	}
	return t.openEdit(*p.item, m, states)
}

func (t *Tab) openCreate(project string, m *projectMeta, states []plane.State, draft *IssueDraft) tea.Cmd {
	v := new(draft.values(defaultState(states).ID))
	form := issueForm(t.formCtx(), "New issue", m, states, v, nil)
	return t.openDialog(form, func() tea.Cmd { return t.submitCreate(project, *v) })
}

func (t *Tab) openEdit(w plane.WorkItem, m *projectMeta, states []plane.State) tea.Cmd {
	md, lossy := plane.HTMLToEditableMarkdown(w.DescriptionHTML)
	orig := issueValues{
		title: w.Name, state: w.StateID, priority: w.Priority,
		labels: slices.Clone(w.LabelIDs), cycle: w.CycleID, desc: md,
	}
	if orig.priority == "" {
		orig.priority = plane.PriorityNone
	}
	if len(w.AssigneeIDs) > 0 {
		orig.assignee = w.AssigneeIDs[0]
	}
	v := orig
	v.labels = slices.Clone(orig.labels)
	form := issueForm(t.formCtx(), "Edit "+w.Key(), m, states, &v, lossy)
	return t.openDialog(form, func() tea.Cmd { return t.submitEdit(w, orig, v) })
}

// defaultState is the project's default state, else its first.
func defaultState(states []plane.State) plane.State {
	for _, s := range states {
		if s.IsDefault {
			return s
		}
	}
	return states[0]
}

// --- the form ----------------------------------------------------------------------

// formCtx is what the issue form needs from its surroundings, so it can
// also run outside the tab (RunCreateForm).
type formCtx struct {
	theme  *ui.Theme
	me     plane.User
	now    time.Time
	height int                             // rows for the form
	nameOf func(project, id string) string // a user's name, for an assignee not in the project
}

func (t *Tab) formCtx() formCtx {
	_, h := t.dialogSize()
	return formCtx{theme: t.theme, me: t.me, now: t.now(), height: h, nameOf: t.memberName}
}

func issueForm(fc formCtx, title string, m *projectMeta, states []plane.State, v *issueValues, lossy []string) *huh.Form {
	stateOpts := make([]huh.Option[string], len(states))
	for i, s := range states {
		stateOpts[i] = huh.NewOption(s.Name, s.ID)
	}
	prioOpts := make([]huh.Option[plane.Priority], len(priorities))
	for i, p := range priorities {
		prioOpts[i] = huh.NewOption(string(p), p)
	}
	labelOpts := make([]huh.Option[string], len(m.labels))
	for i, l := range m.labels {
		labelOpts[i] = huh.NewOption(l.Name, l.ID).Selected(slices.Contains(v.labels, l.ID))
	}

	descHelp := "Markdown · ctrl+e opens $EDITOR"
	var fields []huh.Field
	if len(lossy) > 0 {
		warn := fc.theme.FormWarn.Render(
			"This description has content forge can't round-trip: " + strings.Join(lossy, ", ") +
				". Editing it may lose that content; leave it unchanged to keep it.")
		descHelp += "\n" + warn
		fields = append(fields, huh.NewNote().Description(warn))
	}

	fields = append(fields,
		huh.NewInput().Key("title").Title(title).Placeholder("Title").Value(&v.title).Validate(capture.ValidateTitle),
		huh.NewSelect[string]().Key("state").Title("State").Options(stateOpts...).Value(&v.state),
		huh.NewSelect[plane.Priority]().Key("priority").Title("Priority").Options(prioOpts...).Value(&v.priority),
	)
	if len(labelOpts) > 0 {
		fields = append(fields, huh.NewMultiSelect[string]().Key("labels").Title("Labels").Options(labelOpts...).
			Height(min(len(labelOpts), 8)+1).Value(&v.labels)) // +1: the title
	}
	fields = append(fields,
		huh.NewSelect[string]().Key("assignee").Title("Assignee").Options(memberOptions(fc, m, v.assignee)...).Value(&v.assignee),
		huh.NewSelect[string]().Key("cycle").Title("Cycle").Options(cycleOptions(fc, m, v.cycle)...).Value(&v.cycle),
	)
	// The description gets a page of its own, with room to read it.
	desc := huh.NewText().Key("description").Title(title + " · description").Description(descHelp).
		Editor(editor.Command()...).EditorExtension("md").Lines(max(fc.height-8, 4)).Value(&v.desc)
	return huh.NewForm(huh.NewGroup(fields...), huh.NewGroup(desc))
}

// priorities are Plane's priorities, most urgent first.
var priorities = []plane.Priority{
	plane.PriorityUrgent, plane.PriorityHigh, plane.PriorityMedium, plane.PriorityLow, plane.PriorityNone,
}

func validateBody(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("write something first")
	}
	return nil
}

// memberOptions are unassigned, me, then the project's other members;
// current is kept when it isn't a member.
func memberOptions(fc formCtx, m *projectMeta, current string) []huh.Option[string] {
	opts := []huh.Option[string]{huh.NewOption("Unassigned", "")}
	have := current == ""
	var others []huh.Option[string]
	for _, mem := range m.members {
		if mem.Member == nil {
			continue
		}
		have = have || mem.MemberID == current
		if fc.me.ID != "" && mem.MemberID == fc.me.ID {
			opts = append(opts, huh.NewOption(userName(*mem.Member)+" (me)", mem.MemberID))
			continue
		}
		others = append(others, huh.NewOption(userName(*mem.Member), mem.MemberID))
	}
	opts = append(opts, others...)
	if !have {
		name := "someone"
		if fc.nameOf != nil {
			name = fc.nameOf(m.project.ID, current)
		}
		opts = append(opts, huh.NewOption(name, current))
	}
	return opts
}

// cycleOptions are none and the project's cycles; current is kept when it
// is archived.
func cycleOptions(fc formCtx, m *projectMeta, current string) []huh.Option[string] {
	opts := []huh.Option[string]{huh.NewOption("None", "")}
	have := current == ""
	now := fc.now
	for _, c := range m.cycles {
		if !c.ArchivedAt.IsZero() && c.ID != current {
			continue
		}
		have = have || c.ID == current
		name := c.Name
		if c.IsCurrent(now) {
			name += " (current)"
		}
		opts = append(opts, huh.NewOption(name, c.ID))
	}
	if !have {
		opts = append(opts, huh.NewOption("(another cycle)", current))
	}
	return opts
}

// --- submitting ---------------------------------------------------------------------

// createInput is the create form's values as a WorkItemCreate.
func createInput(v issueValues) (plane.WorkItemCreate, error) {
	html, err := plane.MarkdownToHTML(v.desc)
	if err != nil {
		return plane.WorkItemCreate{}, fmt.Errorf("plane: description: %w", err)
	}
	in := plane.WorkItemCreate{
		Name: strings.TrimSpace(v.title), DescriptionHTML: html,
		StateID: v.state, Priority: v.priority, LabelIDs: v.labels, CycleID: v.cycle,
	}
	if v.assignee != "" {
		in.AssigneeIDs = []string{v.assignee}
	}
	return in, nil
}

func (t *Tab) submitCreate(project string, v issueValues) tea.Cmd {
	in, err := createInput(v)
	if err != nil {
		return errCmd(err)
	}
	api := t.api
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), shortTimeout)
		defer cancel()
		w, err := api.CreateWorkItem(ctx, project, in)
		return createdMsg{project: project, item: w, err: err}
	}
}

// editUpdate is the PATCH for the edit form: only what changed. The
// description is sent only when its Markdown changed, so content forge
// can't represent is never rewritten by accident.
func editUpdate(orig, v issueValues) (plane.WorkItemUpdate, bool, error) {
	var (
		u       plane.WorkItemUpdate
		changed bool
	)
	if name := strings.TrimSpace(v.title); name != orig.title {
		u.Name, changed = &name, true
	}
	if v.state != orig.state {
		u.StateID, changed = &v.state, true
	}
	if v.priority != orig.priority {
		u.Priority, changed = &v.priority, true
	}
	if !sameSet(v.labels, orig.labels) {
		ids := append([]string{}, v.labels...)
		u.LabelIDs, changed = &ids, true
	}
	if v.assignee != orig.assignee {
		ids := []string{}
		if v.assignee != "" {
			ids = append(ids, v.assignee)
		}
		u.AssigneeIDs, changed = &ids, true
	}
	if v.cycle != orig.cycle {
		u.CycleID, changed = &v.cycle, true
	}
	if strings.TrimSpace(v.desc) != strings.TrimSpace(orig.desc) {
		html, err := plane.MarkdownToHTML(v.desc)
		if err != nil {
			return u, false, fmt.Errorf("description: %w", err)
		}
		if html == "" {
			html = "<p></p>" // Plane's empty document
		}
		u.DescriptionHTML, changed = &html, true
	}
	return u, changed, nil
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

func (t *Tab) submitEdit(w plane.WorkItem, orig, v issueValues) tea.Cmd {
	u, changed, err := editUpdate(orig, v)
	if err != nil {
		return errCmd(fmt.Errorf("plane: %s: %w", w.Key(), err))
	}
	if !changed {
		return func() tea.Msg { return ui.InfoMsg{Text: w.Key() + ": no changes"} }
	}
	api, project, id, k := t.api, w.ProjectID, w.ID, w.Key()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), shortTimeout)
		defer cancel()
		out, err := api.UpdateWorkItem(ctx, project, id, u)
		return updatedMsg{key: k, item: out, err: err}
	}
}

// --- results ------------------------------------------------------------------------

// created shows the new item at once (in the lists whose filters it
// matches), selects it and refreshes.
func (t *Tab) created(msg createdMsg) tea.Cmd {
	if msg.err != nil {
		return errCmd(fmt.Errorf("plane: couldn't create the issue: %s", reason(msg.err)))
	}
	w, shown := t.addCreated(msg.project, msg.item)
	cmds := []tea.Cmd{func() tea.Msg { return ui.SuccessMsg{Text: "created " + w.Key()} }}
	if shown {
		cmds = append(cmds, t.Select(w.ID))
	} else {
		cmds = append(cmds, func() tea.Msg { return ui.InfoMsg{Text: w.Key() + " is hidden by the current filters"} })
	}
	return tea.Batch(append(cmds, t.Refresh())...)
}

// Created shows an item created outside the tab (quick capture, P3.5) in
// the lists whose filters match it, selects it and refreshes; the creator
// reports it.
func (t *Tab) Created(project string, item plane.WorkItem) tea.Cmd {
	w, shown := t.addCreated(project, item)
	if shown {
		return tea.Batch(t.Select(w.ID), t.Refresh())
	}
	return t.Refresh()
}

// addCreated fills in what a create response lacks, inserts the item and
// reports whether the current view shows it.
func (t *Tab) addCreated(project string, w plane.WorkItem) (plane.WorkItem, bool) {
	if w.ProjectID == "" {
		w.ProjectID = project
	}
	if m := t.meta[w.ProjectID]; w.Identifier == "" && m != nil && m.project.Identifier != "" && w.SequenceID > 0 {
		w.Identifier = fmt.Sprintf("%s-%d", m.project.Identifier, w.SequenceID)
	}
	if w.UpdatedAt.IsZero() {
		w.UpdatedAt = plane.Date{Time: t.now()}
	}
	w = t.withState(w)
	shown := t.insertItem(w)
	t.setRows()
	return w, shown
}

// updated puts the saved item in place of the old one and refreshes, so
// the lists follow what the filters now say.
func (t *Tab) updated(msg updatedMsg) tea.Cmd {
	if msg.err != nil {
		return errCmd(fmt.Errorf("plane: couldn't save %s: %s", msg.key, reason(msg.err)))
	}
	if w := msg.item; w.ID != "" {
		w = t.withState(w)
		t.editItem(w.ID, func(old *plane.WorkItem) {
			if w.Identifier == "" {
				w.Identifier = old.Identifier
			}
			if w.ProjectID == "" {
				w.ProjectID = old.ProjectID
			}
			*old = w
		})
		t.setRows()
	}
	return tea.Batch(func() tea.Msg { return ui.SuccessMsg{Text: "updated " + msg.key} }, t.Refresh())
}

// withState fills the expanded state from the project's states; labels and
// assignees are named from their ids (the expansions are dropped, as a
// write's response doesn't carry them).
func (t *Tab) withState(w plane.WorkItem) plane.WorkItem {
	w.Labels, w.Assignees = nil, nil
	if w.State != nil && w.State.ID == w.StateID {
		return w
	}
	w.State = nil
	if st, ok := t.stateFull(w); ok {
		w.State = &st
	}
	return w
}

// insertItem adds a new item to the pooled lists of its project whose
// filters match it, and reports whether the current view shows it.
func (t *Tab) insertItem(w plane.WorkItem) bool {
	_, group := t.stateOf(w)
	shown := false
	for _, l := range t.allLists() {
		if l.id != w.ProjectID || !matchesFilter(l.filter, w, group) {
			continue
		}
		if slices.ContainsFunc(l.items, func(x plane.WorkItem) bool { return x.ID == w.ID }) {
			continue
		}
		l.items = append([]plane.WorkItem{w}, l.items...)
		l.loaded = true
		shown = shown || slices.Contains(t.lists, l)
	}
	return shown
}

// matchesFilter is whether Plane would list w under f.
func matchesFilter(f plane.WorkItemFilter, w plane.WorkItem, group plane.StateGroup) bool {
	anyOf := func(want, have []string) bool {
		return len(want) == 0 || slices.ContainsFunc(want, func(id string) bool { return slices.Contains(have, id) })
	}
	switch {
	case len(f.StateGroups) > 0 && !slices.Contains(f.StateGroups, group):
		return false
	case !anyOf(f.AssigneeIDs, w.AssigneeIDs), !anyOf(f.LabelIDs, w.LabelIDs):
		return false
	case f.CycleID != "" && f.CycleID != w.CycleID:
		return false
	case !f.UpdatedAfter.IsZero() && w.UpdatedAt.Before(f.UpdatedAfter):
		return false
	}
	return true
}

// --- comments -------------------------------------------------------------------------

// commentSelected opens the comment form for the selected item.
func (t *Tab) commentSelected() tea.Cmd {
	w, ok := t.selected()
	if !ok || t.api == nil {
		return nil
	}
	body := new(string)
	form := huh.NewForm(huh.NewGroup(
		huh.NewText().Key("comment").Title("Comment on " + w.Key()).
			Description(w.Name + "\nMarkdown · ctrl+e opens $EDITOR").
			Editor(editor.Command()...).EditorExtension("md").Lines(8).
			Validate(validateBody).Value(body),
	))
	return t.openDialog(form, func() tea.Cmd { return t.submitComment(w, *body) })
}

func (t *Tab) submitComment(w plane.WorkItem, body string) tea.Cmd {
	html, err := plane.MarkdownToHTML(body)
	if err != nil {
		return errCmd(fmt.Errorf("plane: comment: %w", err))
	}
	api, project, id, k := t.api, w.ProjectID, w.ID, w.Key()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), shortTimeout)
		defer cancel()
		_, err := api.CreateComment(ctx, project, id, plane.CommentCreate{CommentHTML: html})
		return commentedMsg{id: id, key: k, err: err}
	}
}

// commented drops the item's fetched details so the new comment shows;
// the old ones stay on screen until the reload lands.
func (t *Tab) commented(msg commentedMsg) tea.Cmd {
	if msg.err != nil {
		return errCmd(fmt.Errorf("plane: couldn't comment on %s: %s", msg.key, reason(msg.err)))
	}
	t.det.forget(msg.id)
	return func() tea.Msg { return ui.SuccessMsg{Text: "commented on " + msg.key} }
}

// forget drops the fetched versions of item id (keeping the newest for
// display) so the next sync fetches it again.
func (d *detailState) forget(id string) {
	for k, e := range d.mem {
		if e.id == id {
			delete(d.mem, k)
		}
	}
	d.order = slices.DeleteFunc(d.order, func(k string) bool { return d.mem[k] == nil })
	if d.wantID == id {
		d.want = ""
	}
}

// --- sessions -------------------------------------------------------------------------

// startSession opens the Sessions tab's new-session form for the selected
// item (P1.9): the implement-issue template, named after its key.
func (t *Tab) startSession() tea.Cmd {
	w, ok := t.selected()
	if !ok {
		return nil
	}
	state, _ := t.stateOf(w)
	return tabs.StartSessionFrom(tabs.Source{
		Kind: store.KindPlaneIssue, ID: w.Key(), URL: t.cfg.Plane.IssueURL(w.Key()),
		Title: w.Name, State: state, Repo: t.repoFor(w.ProjectID),
		Branch: plane.BranchFor(w), // P3.7: PROJ-123-short-title
	})
}

// repoFor is the repo of the forge project using Plane project id: the
// active one first, else the first configured; "" means the active one.
func (t *Tab) repoFor(id string) string {
	if t.project != nil && t.project.PlaneProjectID == id {
		return t.project.Repo
	}
	for _, p := range t.cfg.Projects {
		if p.PlaneProjectID == id {
			return p.Repo
		}
	}
	return ""
}
