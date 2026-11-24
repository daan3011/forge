package app

import (
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/setup"
)

// TestSetupPaletteCommands (P6.6): "Run setup" and one entry per step hand
// the terminal to `forge setup [step]` and toast when it ends.
func TestSetupPaletteCommands(t *testing.T) {
	if hasItem(mustNew(t, config.Default()), "setup") {
		t.Fatal("no setup wired: no palette entry")
	}
	var asked []string
	m := mustNew(t, config.Default(), WithSetup(func(step string) *exec.Cmd {
		asked = append(asked, step)
		return exec.CommandContext(t.Context(), "forge", "setup", step)
	}))
	for _, id := range append([]string{"setup"}, func() []string {
		var ids []string
		for _, s := range setup.Steps {
			ids = append(ids, "setup_"+string(s.ID))
		}
		return ids
	}()...) {
		if !hasItem(m, id) {
			t.Errorf("no palette entry %s", id)
		}
	}
	var ran []*exec.Cmd
	m.custom.exec = func(c *exec.Cmd, cb tea.ExecCallback) tea.Cmd {
		ran = append(ran, c)
		return func() tea.Msg { return cb(nil) }
	}
	var run func(Model) (Model, tea.Cmd)
	for _, it := range m.paletteItems() {
		if it.id == "setup_plane" {
			run = it.run
		}
	}
	m, cmd := run(m)
	if len(ran) != 1 || !slices.Equal(asked, []string{"plane"}) {
		t.Fatalf("ran %v, asked %v", ran, asked)
	}
	msgs := runAll(cmd)
	if len(msgs) != 1 {
		t.Fatalf("msgs = %v", msgs)
	}
	if _, ok := msgs[0].(setupDoneMsg); !ok {
		t.Fatalf("msg = %#v", msgs[0])
	}
	m, _ = m.setupDone(setupDoneMsg{})
	if !strings.Contains(m.toast.View(80), "restart forge") {
		t.Errorf("toast = %q", m.toast.View(80))
	}
	m, _ = m.setupDone(setupDoneMsg{err: errors.New("boom")})
	if !strings.Contains(m.toast.View(80), "boom") {
		t.Errorf("error toast = %q", m.toast.View(80))
	}
}
