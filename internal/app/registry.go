package app

import (
	"os"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/docs"
	"github.com/daan3011/forge/internal/tabs/sessions"
	"github.com/daan3011/forge/internal/ui"
)

// Result groups, in their default order. Entity groups follow, named after
// the entity kind ("PRs", "Issues", ...).
const (
	groupRecent   = "Recent"
	groupCommands = "Commands"
	groupTabs     = "Tabs"
	groupProjects = "Projects"
	groupSessions = "Sessions"
)

// item is one palette entry: a command, a tab, a project or an entity.
type item struct {
	id      string // stable id, used for recents
	title   string
	detail  string
	group   string
	hint    string // key hint
	search  string // text matched against the query; title if empty
	current bool   // the active tab or project
	run     func(m Model) (Model, tea.Cmd)
}

func (it item) searchText() string {
	if it.search != "" {
		return it.search
	}
	return it.title
}

// paletteItems builds the registry: every user action plus the entities the
// tabs contribute. It is rebuilt each time the palette opens, so it always
// reflects the current tabs, project and keymap.
func (m Model) paletteItems() []item {
	g := m.keys.Global
	hint := func(b key.Binding) string { return b.Help().Key }
	items := []item{
		{
			id: "quit", title: "Quit forge", group: groupCommands, hint: hint(g.Quit),
			run: func(m Model) (Model, tea.Cmd) { return m, tea.Quit },
		},
		{
			id: "help", title: "Show keys", group: groupCommands, hint: hint(g.Help),
			run: func(m Model) (Model, tea.Cmd) { m.openHelp(); return m, nil },
		},
		{
			id: "refresh", title: "Refresh tab", group: groupCommands, hint: hint(g.Refresh),
			run: func(m Model) (Model, tea.Cmd) { return m.refresh() },
		},
		{
			id: "refresh_all", title: "Refresh all tabs", group: groupCommands, hint: hint(g.RefreshAll),
			run: func(m Model) (Model, tea.Cmd) { return m.refreshAll() },
		},
		{
			id: "next_tab", title: "Next tab", group: groupCommands, hint: hint(g.NextTab),
			run: func(m Model) (Model, tea.Cmd) { return m.switchTo((m.active + 1) % len(m.tabs)) },
		},
		{
			id: "prev_tab", title: "Previous tab", group: groupCommands, hint: hint(g.PrevTab),
			run: func(m Model) (Model, tea.Cmd) { return m.switchTo((m.active - 1 + len(m.tabs)) % len(m.tabs)) },
		},
		{
			id: "projects", title: "Switch project…", group: groupCommands, hint: hint(g.Projects),
			run: func(m Model) (Model, tea.Cmd) { return m.openProjects() },
		},
		{
			id: "notify_test", title: "Notifications: send test notification", group: groupCommands,
			run: func(m Model) (Model, tea.Cmd) {
				if m.alerts == nil {
					return m, func() tea.Msg { return ui.InfoMsg{Text: "Notifications are not available"} }
				}
				return m, m.testNotification()
			},
		},
		{
			id: "goto_session", title: "Go to session…", group: groupCommands, hint: hint(g.GotoSession),
			run: func(m Model) (Model, tea.Cmd) { return m.openSessions() },
		},
		{
			id: "capture", title: "Quick capture issue…", group: groupCommands, hint: hint(g.Capture),
			detail: "title #project !priority @me +label",
			run:    func(m Model) (Model, tea.Cmd) { return m.openCapture() },
		},
	}
	// Log a decision from any tab (P4.4); the Docs tab lists its own.
	if i := m.tabIndex(docs.Title); i >= 0 && i != m.active {
		items = append(items, item{
			id: "adr", title: "Log a decision (ADR)…", group: groupCommands,
			detail: "title, context, decision → docs/decisions",
			run: func(m Model) (Model, tea.Cmd) {
				m, cmd := m.switchTo(i)
				return m, tea.Batch(cmd, func() tea.Msg { return docs.LogDecisionMsg{} })
			},
		})
	}
	if m.templatesDir != "" {
		dir := m.templatesDir
		items = append(items, item{
			id: "templates_edit", title: "Edit templates", group: groupCommands,
			detail: "session templates in $EDITOR",
			run:    func(m Model) (Model, tea.Cmd) { return m, editTemplates(dir) },
		})
	}

	// Commands of the active tab.
	if len(m.tabs) > 0 {
		if c, ok := m.tabs[m.active].(tabs.Commander); ok {
			for _, cmd := range c.Commands() {
				items = append(items, item{
					id: cmd.ID, title: cmd.Title, group: groupCommands, hint: hint(cmd.Key),
					run: func(m Model) (Model, tea.Cmd) { return m, cmd.Run() },
				})
			}
		}
	}

	// Switch tab.
	tabKeys := g.GotoTab.Keys()
	for i, t := range m.tabs {
		it := item{
			id: "tab:" + t.Title(), title: "Switch tab: " + t.Title(), group: groupTabs,
			run: func(m Model) (Model, tea.Cmd) { return m.switchTo(i) },
		}
		if i < len(tabKeys) {
			it.hint = tabKeys[i]
		}
		if i == m.active {
			it.detail = "current"
		}
		items = append(items, it)
	}

	// Switch project.
	for _, it := range m.projectItems() {
		it.title = "Switch project: " + it.title
		items = append(items, it)
	}

	// Entities from every tab.
	for i, t := range m.tabs {
		ep, ok := t.(tabs.EntityProvider)
		if !ok {
			continue
		}
		for _, e := range ep.Entities() {
			items = append(items, item{
				id:     "entity:" + t.Title() + ":" + e.ID,
				title:  e.Title,
				detail: e.Detail,
				group:  e.Kind + "s",
				search: strings.TrimSpace(e.Kind + " " + e.Title + " " + e.Search),
				run: func(m Model) (Model, tea.Cmd) {
					m, switchCmd := m.switchTo(i)
					return m, tea.Batch(switchCmd, ep.Select(e.ID))
				},
			})
		}
	}
	return items
}

// sessionItems lists the sessions of the tab that owns them, for the
// session picker: matched by name, linked issue or PR and branch; picking
// one switches to that tab and attaches to it (resuming it if needed).
func (m Model) sessionItems() []item {
	for i, t := range m.tabs {
		sg, ok := t.(tabs.SessionGoer)
		if !ok {
			continue
		}
		var items []item
		for _, e := range sg.Entities() {
			if e.Kind != sessions.EntitySession { // archived sessions can't be attached
				continue
			}
			items = append(items, item{
				id: "session:" + e.ID, title: e.Title, detail: e.Detail, group: groupSessions,
				search: strings.TrimSpace(e.Title + " " + e.Search),
				run: func(m Model) (Model, tea.Cmd) {
					m, switchCmd := m.switchTo(i)
					return m, tea.Batch(switchCmd, sg.AttachSession(e.ID))
				},
			})
		}
		return items
	}
	return nil
}

// projectItems lists "All projects" and then every configured project, for
// the project switcher and the palette. It is empty without projects.
func (m Model) projectItems() []item {
	if len(m.cfg.Projects) == 0 {
		return nil
	}
	all := item{
		id: "project:*", title: "All projects", group: groupProjects, current: m.project == nil,
		run: func(m Model) (Model, tea.Cmd) { return m.setProject(nil) },
	}
	if all.current {
		all.detail = "current"
	}
	items := []item{all}
	for i := range m.cfg.Projects {
		p := &m.cfg.Projects[i]
		it := item{
			id: "project:" + p.Name, title: p.Name, detail: projectDetail(p), group: groupProjects,
			current: m.project != nil && m.project.Name == p.Name,
			run:     func(m Model) (Model, tea.Cmd) { return m.setProject(p) },
		}
		if it.current {
			it.detail = joinDetail(it.detail, "current")
		}
		items = append(items, it)
	}
	return items
}

// projectDetail is "owner/name · ~/code/name", or "· no checkout".
func projectDetail(p *config.Project) string {
	where := "no checkout"
	if p.Path != "" {
		where = tildePath(p.Path)
	}
	if p.Repo == "" {
		return where
	}
	return p.Repo + " · " + where
}

// tildePath shortens a path under the home folder to ~/….
func tildePath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if rest, ok := strings.CutPrefix(path, home+string(os.PathSeparator)); ok {
		return "~/" + rest
	}
	return path
}

// handlePlaneLinked records Plane links a tab matched or the user picked
// (already saved). When the active project (or "all") is affected every
// tab gets it again, and visible ones refresh.
func (m Model) handlePlaneLinked(msg tabs.PlaneLinkedMsg) (Model, tea.Cmd) {
	resend := false
	for _, l := range msg.Links {
		for i := range m.cfg.Projects {
			p := &m.cfg.Projects[i]
			if !strings.EqualFold(p.Repo, l.Repo) {
				continue
			}
			p.PlaneProjectID, p.PlaneDecided = l.PlaneProjectID, true
			if m.project == nil || m.project == p {
				resend = true
			}
		}
	}
	if !resend {
		return m, nil
	}
	var cmds []tea.Cmd
	for i, t := range m.tabs {
		t.SetProject(m.project)
		if m.refreshesNow(i) {
			cmds = append(cmds, t.Refresh())
		}
	}
	return m, tea.Batch(cmds...)
}

func joinDetail(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + " · " + b
}

// setProject makes p the active project (nil = all projects): every tab is
// told, the visible and background tabs reload (the rest catch up when
// shown), and the choice is saved for the next start.
func (m Model) setProject(p *config.Project) (Model, tea.Cmd) {
	m.project = p
	cmds := []tea.Cmd{saveProject(m.store, p), touchProject(m.projStore, p)}
	for i, t := range m.tabs {
		t.SetProject(p)
		if m.refreshesNow(i) {
			cmds = append(cmds, t.Refresh())
		}
	}
	name := "all projects"
	if p != nil {
		name = p.Name
	}
	m.now = m.clock()
	cmds = append(cmds, m.toast.Show("project: "+name, ui.ToastInfo), m.ensureClock())
	return m, tea.Batch(cmds...)
}

// tabIndex is the index of the tab titled title, or -1.
func (m Model) tabIndex(title string) int {
	for i, t := range m.tabs {
		if t.Title() == title {
			return i
		}
	}
	return -1
}
