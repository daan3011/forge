// Package tmuxdemo is the `forge demo tmux` program: it starts bash in a
// window on forge's tmux server, previews it with capture-pane, and attaches
// on Enter (in a popup when run inside your own tmux). Alt+d detaches back
// here.
package tmuxdemo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/ui"
)

const (
	// Project is the demo's tmux session.
	Project = "forge-demo"
	// Window is the demo's bash window.
	Window = "bash"

	pollEvery = 500 * time.Millisecond
	opTimeout = 5 * time.Second
)

type (
	readyMsg   struct{ target tmux.Target }
	previewMsg struct {
		gen   int
		text  string
		alive bool
	}
	pollMsg     struct{ gen int }
	attachMsg   struct{ a tmux.Attachment }
	detachedMsg struct{ err error }
	killedMsg   struct{}
	errMsg      struct{ err error }
)

// Model is the demo program.
type Model struct {
	d     tmux.Driver
	dir   string
	theme *ui.Theme
	keys  *ui.KeyMap

	target  tmux.Target
	ready   bool
	alive   bool
	preview string
	status  string
	warning string
	err     error
	gen     int // invalidates stale polls after restart/kill
	w, h    int
}

// New returns the demo driving d; bash starts in dir.
func New(d tmux.Driver, dir string) Model {
	return Model{
		d:      d,
		dir:    dir,
		theme:  ui.NewTheme(true, nil),
		keys:   ui.DefaultKeyMap(),
		status: "starting bash on the forge tmux server…",
	}
}

func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), opTimeout)
}

// start ensures the session and a fresh bash window (replacing any left
// over from an earlier run).
func (m Model) start() tea.Cmd {
	d, dir := m.d, m.dir
	return func() tea.Msg {
		c, cancel := ctx()
		defer cancel()
		s, err := d.EnsureSession(c, Project)
		if err != nil {
			return errMsg{err}
		}
		t := tmux.Target{Session: s, Window: Window}
		if err := d.KillWindow(c, t); err != nil {
			return errMsg{err}
		}
		t, err = d.NewWindow(c, s, Window, dir, []string{"bash"}, map[string]string{"FORGE_DEMO": "1"})
		if err != nil {
			return errMsg{err}
		}
		return readyMsg{t}
	}
}

func (m Model) capture() tea.Cmd {
	d, t, gen, lines := m.d, m.target, m.gen, max(m.h-6, 5)
	return func() tea.Msg {
		c, cancel := ctx()
		defer cancel()
		alive, err := d.WindowAlive(c, t)
		if err != nil {
			return errMsg{err}
		}
		text, err := d.CapturePane(c, t, lines)
		if err != nil && !errors.Is(err, tmux.ErrNotFound) {
			return errMsg{err}
		}
		return previewMsg{gen: gen, text: text, alive: alive}
	}
}

func poll(gen int) tea.Cmd {
	return tea.Tick(pollEvery, func(time.Time) tea.Msg { return pollMsg{gen} })
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, m.start())
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		*m.theme = *ui.NewTheme(msg.IsDark(), nil)
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case readyMsg:
		m.target, m.ready, m.err = msg.target, true, nil
		m.gen++
		m.status = "bash is running in " + msg.target.String() + " — enter to attach, Alt+d to come back"
		return m, m.capture()
	case pollMsg:
		if msg.gen == m.gen && m.ready {
			return m, m.capture()
		}
	case previewMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		m.preview, m.alive = msg.text, msg.alive
		return m, poll(m.gen)
	case attachMsg:
		m.warning = msg.a.Warning
		switch msg.a.Mode {
		case tmux.AttachSwitched:
			m.status = "switched this tmux client to " + m.target.String()
			return m, nil
		case tmux.AttachPopup:
			// display-popup blocks until the popup closes; forge keeps the
			// terminal and stays visible behind it.
			m.status = "opened " + m.target.String() + " in a popup of your tmux — Alt+d closes it"
			cmd := msg.a.Cmd
			return m, func() tea.Msg { return detachedMsg{cmd.Run()} }
		default:
			return m, tea.ExecProcess(msg.a.Cmd, func(err error) tea.Msg { return detachedMsg{err} })
		}
	case detachedMsg:
		m.err = msg.err
		m.status = "back in forge ✓ (detached from " + m.target.String() + ")"
		m.gen++ // the preview refresh below restarts polling
		return m, m.capture()
	case killedMsg:
		m.ready, m.alive, m.preview = false, false, ""
		m.gen++
		m.status = "window killed — n starts a new one"
	case errMsg:
		m.err = msg.err
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k, d, t := m.keys.Tmux, m.d, m.target
	switch {
	case key.Matches(msg, k.Quit):
		ready := m.ready
		return m, func() tea.Msg {
			if ready {
				c, cancel := ctx()
				defer cancel()
				_ = d.KillWindow(c, t)
			}
			return tea.QuitMsg{}
		}
	case key.Matches(msg, k.Attach) && m.ready:
		return m, func() tea.Msg {
			c, cancel := ctx()
			defer cancel()
			a, err := d.Attach(c, t)
			if err != nil {
				return errMsg{err}
			}
			return attachMsg{a}
		}
	case key.Matches(msg, k.Restart):
		m.ready = false
		m.gen++
		m.status = "starting a fresh bash…"
		return m, m.start()
	case key.Matches(msg, k.Kill) && m.ready:
		return m, func() tea.Msg {
			c, cancel := ctx()
			defer cancel()
			if err := d.KillWindow(c, t); err != nil {
				return errMsg{err}
			}
			return killedMsg{}
		}
	}
	return m, nil
}

// View implements tea.Model.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.BackgroundColor = m.theme.Bg
	return v
}

func (m Model) render() string {
	th := m.theme
	w, h := max(m.w-4, 20), max(m.h-6, 5)

	state := "not running"
	switch {
	case m.ready && m.alive:
		state = "alive"
	case m.ready:
		state = "exited (pane kept)"
	}
	head := th.DetailTitle.Render("tmux driver demo") + "  " +
		th.DetailLabel.Render(fmt.Sprintf("socket %s · window %s · %s", tmux.DefaultSocket, m.target, state))
	status := th.Body.Render(m.status)
	if m.err != nil {
		status = ui.Tint(th.Error, "error: "+m.err.Error())
	}
	if m.warning != "" {
		status += "\n" + ui.Tint(th.Error, "warning: "+m.warning)
	}
	preview := m.preview
	if preview == "" {
		preview = th.Hint.Render("(no output yet)")
	}
	k := m.keys.Tmux
	return lipgloss.JoinVertical(lipgloss.Left,
		head,
		status,
		th.Pane.Render(ui.Box(preview, w, h)),
		ui.HelpLine(th, []key.Binding{k.Attach, k.Restart, k.Kill, k.Quit}),
	)
}
