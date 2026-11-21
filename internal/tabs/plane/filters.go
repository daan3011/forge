package plane

import (
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/ui"
)

// assigneeMe is the assignee filter meaning the API key's user.
const assigneeMe = "me"

// filters narrow the items; all of them are sent to Plane.
type filters struct {
	project  string             // Plane project id shown instead of the active one; "" = follow
	assignee string             // assigneeMe, a user id, or "" for anyone
	groups   []plane.StateGroup // nil = every state
	label    string             // label id (one project only)
	cycle    string             // cycle id (one project only)
}

// openGroups are the state groups of work that isn't finished.
var openGroups = []plane.StateGroup{plane.GroupTriage, plane.GroupBacklog, plane.GroupUnstarted, plane.GroupStarted}

// allGroups are every state group, in workflow order.
var allGroups = []plane.StateGroup{
	plane.GroupTriage, plane.GroupBacklog, plane.GroupUnstarted,
	plane.GroupStarted, plane.GroupCompleted, plane.GroupCancelled,
}

// defaultFilters are what the tab starts with: my open items.
func defaultFilters() filters {
	return filters{assignee: assigneeMe, groups: slices.Clone(openGroups)}
}

func (f filters) equal(g filters) bool {
	return f.project == g.project && f.assignee == g.assignee && f.label == g.label &&
		f.cycle == g.cycle && slices.Equal(f.groups, g.groups)
}

func (f filters) isZero() bool { return f.equal(filters{}) }

// groupsLabel names a set of state groups: "open", "started, completed".
func groupsLabel(gs []plane.StateGroup) string {
	switch {
	case len(gs) == 0 || len(gs) == len(allGroups):
		return ""
	case slices.Equal(gs, openGroups):
		return "open"
	}
	parts := make([]string, len(gs))
	for i, g := range gs {
		parts[i] = string(g)
	}
	return strings.Join(parts, ", ")
}

// --- text filter -------------------------------------------------------------------

func (t *Tab) startFilter() {
	t.filter.Start()
	t.resizeTable()
}

func (t *Tab) updateFilter(msg tea.KeyPressMsg) {
	in := t.keys.Input
	switch {
	case key.Matches(msg, in.Accept):
		t.filter.Stop()
		if t.filter.Value() == "" {
			t.resizeTable()
		}
	case key.Matches(msg, in.Cancel):
		t.clearFilter()
	default:
		if t.filter.Update(msg) {
			t.table.SetFilter(t.filter.Value())
		}
	}
}

func (t *Tab) clearFilter() {
	t.filter.Clear()
	t.table.SetFilter("")
	t.resizeTable()
}

// --- filter form -------------------------------------------------------------------

// openFilters opens the project / assignee / state / label / cycle form.
// Label and cycle are offered when one project is listed and its
// details have loaded.
func (t *Tab) openFilters() tea.Cmd {
	f := t.filt
	groups := slices.Clone(f.groups)
	if len(groups) == 0 {
		groups = slices.Clone(allGroups)
	}

	var fields []huh.Field
	if opts := t.projectOptions(); len(opts) > 1 {
		fields = append(fields, huh.NewSelect[string]().Key("project").Title("Project").Options(opts...).Value(&f.project))
	}
	fields = append(fields,
		huh.NewSelect[string]().Key("assignee").Title("Assignee").Options(t.assigneeOptions(f.assignee)...).Value(&f.assignee),
		huh.NewMultiSelect[plane.StateGroup]().Key("states").Title("States").Options(groupOptions()...).Value(&groups),
	)
	var meta *projectMeta
	if t.single() {
		meta = t.meta[t.lists[0].id]
	}
	if meta != nil && len(meta.labels) > 0 {
		opts := []huh.Option[string]{huh.NewOption("Any label", "")}
		for _, l := range meta.labels {
			opts = append(opts, huh.NewOption(l.Name, l.ID))
		}
		fields = append(fields, huh.NewSelect[string]().Key("label").Title("Label").Options(opts...).Value(&f.label))
	}
	if meta != nil && len(meta.cycles) > 0 {
		opts := []huh.Option[string]{huh.NewOption("Any cycle", "")}
		now := t.now()
		for _, c := range meta.cycles {
			if !c.ArchivedAt.IsZero() {
				continue
			}
			name := c.Name
			if c.IsCurrent(now) {
				name += " (current)"
			}
			opts = append(opts, huh.NewOption(name, c.ID))
		}
		fields = append(fields, huh.NewSelect[string]().Key("cycle").Title("Cycle").Options(opts...).Value(&f.cycle))
	}
	form := huh.NewForm(huh.NewGroup(fields...))
	prevProject := t.filt.project
	return t.openDialog(form, func() tea.Cmd {
		if f.project != prevProject {
			f.label, f.cycle = "", "" // they belong to the old project
		}
		f.groups = groups
		if len(f.groups) == len(allGroups) {
			f.groups = nil
		}
		return t.setFilters(f)
	})
}

// projectOptions are the projects the form offers: the active project
// (or all), then each configured Plane project.
func (t *Tab) projectOptions() []huh.Option[string] {
	first := "All projects"
	if t.project != nil {
		first = "Active project (" + t.project.Name + ")"
	}
	opts := []huh.Option[string]{huh.NewOption(first, "")}
	seen := map[string]bool{}
	for _, p := range t.cfg.Projects {
		if p.PlaneProjectID == "" || seen[p.PlaneProjectID] {
			continue
		}
		seen[p.PlaneProjectID] = true
		opts = append(opts, huh.NewOption(p.Name, p.PlaneProjectID))
	}
	return opts
}

// assigneeOptions are me, anyone and the listed project's members.
func (t *Tab) assigneeOptions(current string) []huh.Option[string] {
	me := "Me"
	if t.me.ID != "" {
		me += " (" + userName(t.me) + ")"
	}
	opts := []huh.Option[string]{huh.NewOption(me, assigneeMe), huh.NewOption("Anyone", "")}
	have := current == "" || current == assigneeMe
	if t.single() {
		if m := t.meta[t.lists[0].id]; m != nil {
			for _, mem := range m.members {
				if mem.Member == nil || mem.MemberID == t.me.ID {
					continue
				}
				opts = append(opts, huh.NewOption(userName(*mem.Member), mem.MemberID))
				have = have || mem.MemberID == current
			}
		}
	}
	if !have {
		opts = append(opts, huh.NewOption("someone else", current))
	}
	return opts
}

func groupOptions() []huh.Option[plane.StateGroup] {
	opts := make([]huh.Option[plane.StateGroup], len(allGroups))
	for i, g := range allGroups {
		opts[i] = huh.NewOption(string(g), g)
	}
	return opts
}

// setFilters applies f: new lists (and a refresh) when anything changed.
func (t *Tab) setFilters(f filters) tea.Cmd {
	if f.equal(t.filt) {
		return nil
	}
	t.filt = f
	t.rebuildLists()
	t.table.SetRows(nil) // a new list starts at the top
	t.setRows()
	return t.Refresh()
}

// toggleMine switches between my items and anyone's.
func (t *Tab) toggleMine() tea.Cmd {
	f := t.filt
	if f.assignee == assigneeMe {
		f.assignee = ""
	} else {
		f.assignee = assigneeMe
	}
	return t.setFilters(f)
}

// clearFilters shows every item of the active project(s): anyone's, in
// every state.
func (t *Tab) clearFilters() tea.Cmd {
	if t.filter.Visible() {
		t.clearFilter()
	}
	return t.setFilters(filters{})
}

// --- dialog --------------------------------------------------------------------------

// openDialog shows form over the tab; done runs once it is completed.
func (t *Tab) openDialog(form *huh.Form, done func() tea.Cmd) tea.Cmd {
	t.dialog = form.WithTheme(t.theme.Huh()).WithKeyMap(t.keys.HuhKeyMap()).WithShowHelp(false)
	t.onDone = done
	init := t.dialog.Init()
	t.sizeDialog()
	return init
}

func (t *Tab) updateDialog(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		return nil // the app owns the window size; SetSize resizes the form
	}
	_, cmd := t.dialog.Update(msg)
	switch t.dialog.State {
	case huh.StateCompleted:
		done := t.onDone
		t.dialog, t.onDone = nil, nil
		if done == nil {
			return nil
		}
		return done()
	case huh.StateAborted:
		t.dialog, t.onDone = nil, nil
		return nil
	}
	return cmd
}

func (t *Tab) dialogSize() (w, h int) { return max(t.width-4, 10), max(t.height-2, 3) }

func (t *Tab) sizeDialog() {
	if t.dialog == nil {
		return
	}
	w, h := t.dialogSize()
	_, _ = t.dialog.Update(tea.WindowSizeMsg{Width: w, Height: h})
}

// --- filter box ------------------------------------------------------------------------

// filterBox is the bordered box naming the project(s) and active filters.
func (t *Tab) filterBox(width int) string {
	th := t.theme
	sep := th.SectionSep.Render(" · ")
	label := func(k, v string) string { return th.SearchIcon.Render(k+": ") + th.Body.Render(v) }
	var parts []string
	switch len(t.lists) {
	case 0:
	case 1:
		name := t.projectLabel(t.lists[0].id)
		if name == "" {
			name = "project"
		}
		parts = append(parts, th.SearchQuery.Render(name))
	default:
		parts = append(parts, th.SearchQuery.Render(strconv.Itoa(len(t.lists))+" projects"))
	}
	switch t.filt.assignee {
	case "":
	case assigneeMe:
		who := "me"
		if t.me.ID != "" {
			who = "@" + userName(t.me)
		}
		parts = append(parts, label("assignee", who))
	default:
		id := ""
		if len(t.lists) > 0 {
			id = t.lists[0].id
		}
		parts = append(parts, label("assignee", t.memberName(id, t.filt.assignee)))
	}
	if g := groupsLabel(t.filt.groups); g != "" {
		parts = append(parts, label("state", g))
	}
	if t.single() {
		m := t.meta[t.lists[0].id]
		if t.filt.label != "" {
			parts = append(parts, label("label", metaName(m, t.filt.label)))
		}
		if t.filt.cycle != "" {
			parts = append(parts, label("cycle", metaName(m, t.filt.cycle)))
		}
	}
	parts = append(parts, th.Hint.Render(t.keys.Plane.Filters.Help().Key+" filters"))
	line := th.SearchIcon.Render("⌕ ") + strings.Join(parts, sep)
	return th.SearchBox.Render(ui.Box(line, max(width-4, 1), 1))
}

// metaName is the name of a label or cycle id in m, or "…" until known.
func metaName(m *projectMeta, id string) string {
	if m == nil {
		return "…"
	}
	for _, l := range m.labels {
		if l.ID == id {
			return l.Name
		}
	}
	for _, c := range m.cycles {
		if c.ID == id {
			return c.Name
		}
	}
	return "…"
}
