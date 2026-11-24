package app

import (
	"os/exec"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/setup"
	"github.com/daan3011/forge/internal/ui"
)

// SetupCmd returns the command that runs `forge setup [step]` (P6.6);
// step "" runs every step.
type SetupCmd func(step string) *exec.Cmd

// WithSetup adds the "Run setup" palette commands, which hand the terminal
// to the setup wizard and come back when it ends.
func WithSetup(f SetupCmd) Option {
	return func(m *Model) { m.setupCmd = f }
}

// setupDoneMsg comes back when the wizard ends.
type setupDoneMsg struct{ err error }

// setupItems are the palette entries, when setup is wired: all steps, and
// each step on its own.
func (m Model) setupItems() []item {
	if m.setupCmd == nil {
		return nil
	}
	items := []item{{
		id: "setup", title: "Run setup", group: groupCommands,
		detail: "guided setup: tools, GitHub, Plane, projects, tmux, smoke test",
		run:    func(m Model) (Model, tea.Cmd) { return m, m.runSetup("") },
	}}
	for _, s := range setup.Steps {
		id := string(s.ID)
		items = append(items, item{
			id: "setup_" + id, title: "Run setup: " + s.Title, group: groupCommands,
			detail: "forge setup " + id + " · " + s.Blurb,
			run:    func(m Model) (Model, tea.Cmd) { return m, m.runSetup(id) },
		})
	}
	return items
}

func (m Model) runSetup(step string) tea.Cmd {
	c := m.setupCmd(step)
	return m.execFn()(c, func(err error) tea.Msg { return setupDoneMsg{err} })
}

// setupDone tells how the wizard ended. Config changes apply on the next
// start: the running tabs keep the config they were built with.
func (m Model) setupDone(msg setupDoneMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		return m, m.toast.Show("setup: "+msg.err.Error(), ui.ToastError)
	}
	return m, m.toast.Show("setup finished · restart forge to apply config changes", ui.ToastSuccess)
}
