// Package app holds the root Bubble Tea model, tab routing, global keys and
// the command palette.
package app

import (
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

// TODO(P0.6): register every action handled here (quit, next/prev tab, go to
// tab N, widen/narrow list) as palette commands.

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
}

// New returns the root model for the given config.
func New(cfg *config.Config) Model {
	theme := ui.NewTheme(true, cfg.UI.Theme)
	return newModel(cfg, theme, []tabs.Tab{
		home.New(theme),
		prs.New(theme),
		pipelines.New(theme),
		plane.New(theme),
		sessions.New(theme),
		docs.New(theme),
		artifacts.New(theme),
		contexttab.New(theme),
	})
}

func newModel(cfg *config.Config, theme *ui.Theme, ts []tabs.Tab) Model {
	m := Model{
		cfg:     cfg,
		theme:   theme,
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
	cmds := []tea.Cmd{tea.RequestBackgroundColor, clockTick()}
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

	case ui.InfoMsg:
		m.message = msg.Text
		return m, m.toast.Show(msg.Text, ui.ToastInfo)

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m.updateActive(msg)
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, ui.Keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, ui.Keys.NextTab):
		return m.switchTo((m.active + 1) % len(m.tabs))
	case key.Matches(msg, ui.Keys.PrevTab):
		return m.switchTo((m.active - 1 + len(m.tabs)) % len(m.tabs))
	case key.Matches(msg, ui.Keys.GotoTab):
		if i := ui.Keys.TabIndex(msg); i >= 0 && i < len(m.tabs) {
			return m.switchTo(i)
		}
		return m, nil
	}
	return m.updateActive(msg)
}

func (m Model) switchTo(i int) (tea.Model, tea.Cmd) {
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
		}),
	)
	screen = ui.Box(screen, m.width, m.height)

	if m.toast.Empty() {
		return screen
	}
	toast := m.toast.View(max(m.width*2/5, 24))
	x := max(m.width-lipgloss.Width(toast)-1, 0)
	y := max(m.height-statusBarHeight-lipgloss.Height(toast), 0)
	canvas := lipgloss.NewCanvas(m.width, m.height)
	canvas.Compose(lipgloss.NewCompositor(
		lipgloss.NewLayer(screen),
		lipgloss.NewLayer(toast).X(x).Y(y).Z(1),
	))
	return ui.Box(canvas.Render(), m.width, m.height) // Render trims trailing spaces
}
