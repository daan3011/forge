// Package app holds the root Bubble Tea model, tab routing, global keys and
// the command palette.
package app

import (
	"slices"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/artifacts"
	contexttab "github.com/daan3011/forge/internal/tabs/context"
	"github.com/daan3011/forge/internal/tabs/docs"
	"github.com/daan3011/forge/internal/tabs/home"
	"github.com/daan3011/forge/internal/tabs/pipelines"
	"github.com/daan3011/forge/internal/tabs/plane"
	"github.com/daan3011/forge/internal/tabs/prs"
	"github.com/daan3011/forge/internal/tabs/sessions"
	"github.com/daan3011/forge/internal/ui"
)

// statusBarHeight is the number of rows the status bar takes.
const statusBarHeight = 1

// clockInterval is how often "updated Ns ago" is refreshed.
const clockInterval = time.Second

// clockMsg ticks the status bar clock.
type clockMsg time.Time

// Model is the root model.
type Model struct {
	cfg     *config.Config
	theme   *ui.Theme
	keys    *ui.KeyMap
	tabs    []tabs.Tab
	active  int
	width   int
	height  int
	project *config.Project
	toast   ui.Toast
	status  ui.StatusBar
	message string
	now     time.Time
	clockOn bool

	help     ui.HelpView
	helpOpen bool

	palette     ui.CommandPalette
	paletteOpen bool
	items       []item // registry snapshot taken when the palette opened
	shown       []item // ranked results
	recent      []string
	recents     RecentStore
}

// New returns the root model for the given config. It fails if the keybinds
// overrides are invalid.
func New(cfg *config.Config, opts ...Option) (Model, error) {
	keys, err := ui.NewKeyMap(cfg.Keybinds)
	if err != nil {
		return Model{}, err
	}
	theme := ui.NewTheme(true, cfg.UI.Theme)
	m := newModel(cfg, theme, keys, []tabs.Tab{
		home.New(theme, keys),
		prs.New(theme, keys),
		pipelines.New(theme, keys),
		plane.New(theme, keys),
		sessions.New(theme, keys),
		docs.New(theme, keys),
		artifacts.New(theme, keys),
		contexttab.New(theme, keys),
	})
	for _, o := range opts {
		o(&m)
	}
	return m, nil
}

func newModel(cfg *config.Config, theme *ui.Theme, keys *ui.KeyMap, ts []tabs.Tab) Model {
	m := Model{
		cfg:     cfg,
		theme:   theme,
		keys:    keys,
		help:    ui.NewHelpView(theme, keys),
		palette: ui.NewCommandPalette(theme, keys),
		tabs:    ts,
		toast:   ui.NewToast(theme),
		status:  ui.NewStatusBar(theme),
		now:     time.Now(),
		clockOn: true, // Init starts the first tick
	}
	if len(cfg.Projects) > 0 {
		m.project = &cfg.Projects[0]
	}
	for _, t := range m.tabs {
		t.SetProject(m.project)
	}
	return m
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.RequestBackgroundColor, clockTick(), loadRecents(m.recents)}
	for _, t := range m.tabs {
		cmds = append(cmds, t.Init())
	}
	return tea.Batch(cmds...)
}

func clockTick() tea.Cmd {
	return tea.Tick(clockInterval, func(t time.Time) tea.Msg { return clockMsg(t) })
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.toast.Update(msg) {
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		for _, t := range m.tabs {
			t.SetSize(m.contentSize())
		}
		m.help.SetSize(m.width, m.height)
		return m, nil

	case tea.BackgroundColorMsg:
		*m.theme = *ui.NewTheme(msg.IsDark(), m.cfg.UI.Theme)
		return m, nil

	case clockMsg:
		m.now = time.Time(msg)
		if m.activeUpdated().IsZero() {
			m.clockOn = false
			return m, nil
		}
		return m, clockTick()

	case ui.ErrMsg:
		if msg.Err == nil {
			return m, nil
		}
		return m, m.toast.Show(msg.Err.Error(), ui.ToastError)

	case recentsMsg:
		m.recent = mergeRecent(m.recent, msg)
		return m, nil

	case ui.InfoMsg:
		m.message = msg.Text
		return m, m.toast.Show(msg.Text, ui.ToastInfo)

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m.updateActive(msg)
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	g := m.keys.Global
	if key.Matches(msg, g.ForceQuit) {
		return m, tea.Quit
	}
	if key.Matches(msg, g.Palette) {
		if m.paletteOpen {
			m.paletteOpen = false
		} else {
			m.openPalette()
		}
		return m, nil
	}
	if m.paletteOpen {
		return m.handlePaletteKey(msg)
	}
	if m.helpOpen {
		return m.handleHelpKey(msg)
	}
	if m.capturingInput() {
		return m.updateActive(msg)
	}
	switch {
	case key.Matches(msg, g.Quit):
		return m, tea.Quit
	case key.Matches(msg, g.Help):
		m.openHelp()
		return m, nil
	case key.Matches(msg, g.NextTab):
		return m.switchTo((m.active + 1) % len(m.tabs))
	case key.Matches(msg, g.PrevTab):
		return m.switchTo((m.active - 1 + len(m.tabs)) % len(m.tabs))
	case key.Matches(msg, g.GotoTab):
		if i := m.keys.TabIndex(msg); i >= 0 && i < len(m.tabs) {
			return m.switchTo(i)
		}
		return m, nil
	case key.Matches(msg, g.Refresh):
		return m.refresh()
	}
	return m.updateActive(msg)
}

// openPalette snapshots the registry and shows every item, recents first.
func (m *Model) openPalette() {
	m.helpOpen = false
	m.items = m.paletteItems()
	m.palette.Reset()
	m.rerank()
	m.paletteOpen = true
}

func (m *Model) rerank() {
	m.shown = rank(m.items, m.palette.Query(), m.recent)
	rows := make([]ui.PaletteRow, len(m.shown))
	for i, it := range m.shown {
		rows[i] = ui.PaletteRow{Group: it.group, Title: it.title, Detail: it.detail, Hint: it.hint}
	}
	m.palette.SetRows(rows)
}

func (m Model) handlePaletteKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	in := m.keys.Input
	switch {
	case key.Matches(msg, in.Cancel):
		m.paletteOpen = false
		return m, nil
	case key.Matches(msg, in.Accept):
		i := m.palette.Selected()
		if i < 0 {
			return m, nil
		}
		it := m.shown[i]
		m.paletteOpen = false
		m.items, m.shown = nil, nil
		m.recent = pushRecent(m.recent, it.id)
		save := saveRecents(m.recents, m.recent)
		var cmd tea.Cmd
		m, cmd = it.run(m)
		return m, tea.Batch(cmd, save)
	}
	if m.palette.Update(msg) {
		m.rerank()
	}
	return m, nil
}

func (m Model) handleHelpKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Global.Help, m.keys.Input.Cancel, m.keys.Global.Quit):
		m.helpOpen = false
	default:
		m.help.Update(msg)
	}
	return m, nil
}

// openHelp shows the help overlay for the global keys and the active tab.
func (m *Model) openHelp() {
	sections := []ui.HelpSection{{Title: "Global", Groups: [][]key.Binding{m.keys.GlobalHelp()}}}
	if len(m.tabs) > 0 {
		t := m.tabs[m.active]
		sections = append(sections, ui.HelpSection{Title: t.Title(), Groups: t.FullHelp()})
	}
	m.help.SetContent(sections)
	m.help.SetSize(m.width, m.height)
	m.helpOpen = true
}

// statusKeys are the status bar hints: the tab's own, plus help unless the
// tab is taking text input (where ? would be typed).
func (m Model) statusKeys(active tabs.Tab) []key.Binding {
	if m.paletteOpen {
		in := m.keys.Input
		return []key.Binding{in.Next, in.Prev, in.Cancel, in.Accept}
	}
	if m.capturingInput() {
		return active.ShortHelp()
	}
	return slices.Concat(active.ShortHelp(), []key.Binding{m.keys.Global.Help})
}

func (m Model) capturingInput() bool {
	if len(m.tabs) == 0 {
		return false
	}
	c, ok := m.tabs[m.active].(tabs.InputCapturer)
	return ok && c.CapturingInput()
}

func (m Model) refresh() (Model, tea.Cmd) {
	if len(m.tabs) == 0 {
		return m, nil
	}
	cmd := m.tabs[m.active].Refresh()
	m.now = time.Now()
	return m, tea.Batch(cmd, m.ensureClock())
}

func (m Model) switchTo(i int) (Model, tea.Cmd) {
	m.active = i
	m.now = time.Now()
	return m, m.ensureClock()
}

// ensureClock restarts the clock tick if the active tab shows an update time.
func (m *Model) ensureClock() tea.Cmd {
	if m.clockOn || m.activeUpdated().IsZero() {
		return nil
	}
	m.clockOn = true
	return clockTick()
}

func (m Model) updateActive(msg tea.Msg) (tea.Model, tea.Cmd) {
	if len(m.tabs) == 0 {
		return m, nil
	}
	t, cmd := m.tabs[m.active].Update(msg)
	m.tabs[m.active] = t
	return m, cmd
}

func (m Model) activeUpdated() time.Time {
	if len(m.tabs) == 0 {
		return time.Time{}
	}
	if u, ok := m.tabs[m.active].(tabs.Updater); ok {
		return u.LastUpdated()
	}
	return time.Time{}
}

// contentSize is the space left for the active tab.
func (m Model) contentSize() (width, height int) {
	return m.width, max(m.height-ui.TabBarHeight-statusBarHeight, 0)
}

// View implements tea.Model.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.BackgroundColor = m.theme.Bg
	return v
}

func (m Model) render() string {
	if m.width == 0 || m.height == 0 || len(m.tabs) == 0 {
		return m.theme.Wordmark.Render("forge")
	}
	if m.helpOpen {
		return m.help.View()
	}
	titles := make([]string, len(m.tabs))
	for i, t := range m.tabs {
		titles[i] = t.Title()
	}
	cw, ch := m.contentSize()
	active := m.tabs[m.active]

	project := ""
	if m.project != nil {
		project = m.project.Name
	}
	screen := lipgloss.JoinVertical(lipgloss.Left,
		ui.TabBar(m.theme, titles, m.active, m.width),
		ui.Box(active.View(), cw, ch),
		m.status.View(m.width, ui.StatusInfo{
			Tab:     active.Title(),
			Project: project,
			Message: m.message,
			Updated: m.activeUpdated(),
			Now:     m.now,
			Keys:    m.statusKeys(active),
		}),
	)
	screen = ui.Box(screen, m.width, m.height)

	layers := []*lipgloss.Layer{lipgloss.NewLayer(screen)}
	if m.paletteOpen {
		if l := m.paletteLayer(); l != nil {
			layers = append(layers, l)
		}
	}
	if !m.toast.Empty() {
		toast := m.toast.View(max(m.width*2/5, 24))
		x := max(m.width-lipgloss.Width(toast)-1, 0)
		y := max(m.height-statusBarHeight-lipgloss.Height(toast), 0)
		layers = append(layers, lipgloss.NewLayer(toast).X(x).Y(y).Z(2))
	}
	if len(layers) == 1 {
		return screen
	}
	canvas := lipgloss.NewCanvas(m.width, m.height)
	canvas.Compose(lipgloss.NewCompositor(layers...))
	return ui.Box(canvas.Render(), m.width, m.height) // Render trims trailing spaces
}

// Palette size limits.
const (
	paletteMaxWidth  = 72
	paletteMinHeight = 8
)

// paletteLayer renders the palette centred over the screen.
func (m Model) paletteLayer() *lipgloss.Layer {
	w := min(paletteMaxWidth, m.width-4)
	h := min(max(m.height*3/5, paletteMinHeight), m.height-2)
	view := m.palette.View(w, h)
	if view == "" {
		return nil
	}
	x := (m.width - lipgloss.Width(view)) / 2
	y := (m.height - lipgloss.Height(view)) / 2
	return lipgloss.NewLayer(view).X(x).Y(y).Z(1)
}
