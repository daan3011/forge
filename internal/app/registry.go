package app

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

// Result groups, in their default order. Entity groups follow, named after
// the entity kind ("PRs", "Issues", ...).
const (
	groupRecent   = "Recent"
	groupCommands = "Commands"
	groupTabs     = "Tabs"
	groupProjects = "Projects"
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
				search: e.Kind + " " + e.Title,
				run: func(m Model) (Model, tea.Cmd) {
					m, switchCmd := m.switchTo(i)
					return m, tea.Batch(switchCmd, ep.Select(e.ID))
				},
			})
		}
	}
	return items
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
			id: "project:" + p.Name, title: p.Name, detail: p.Repo, group: groupProjects,
			current: m.project != nil && m.project.Name == p.Name,
			run:     func(m Model) (Model, tea.Cmd) { return m.setProject(p) },
		}
		if it.current {
			it.detail = joinDetail(p.Repo, "current")
		}
		items = append(items, it)
	}
	return items
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
	cmds := []tea.Cmd{saveProject(m.store, p)}
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
