package app

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/config"
)

// TestDoctorPaletteCommand (P6.4): "Run doctor" runs the checks off the
// update loop and shows the report full screen.
func TestDoctorPaletteCommand(t *testing.T) {
	if hasItem(mustNew(t, config.Default()), "doctor") {
		t.Fatal("no doctor wired: no palette entry")
	}
	ran := 0
	m := mustNew(t, config.Default(), WithDoctor(func(context.Context) (string, bool) {
		ran++
		return "✗ gh  not logged in\n         fix: gh auth login\n", true
	}))
	var model tea.Model = m
	model, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = model.(Model)
	var run func(Model) (Model, tea.Cmd)
	for _, it := range m.paletteItems() {
		if it.id == "doctor" {
			run = it.run
		}
	}
	if run == nil {
		t.Fatal("no Run doctor palette entry")
	}
	m, cmd := run(m)
	if ran != 0 {
		t.Fatal("the checks ran inside Update")
	}
	var done tea.Msg
	for _, msg := range runAll(cmd) {
		if _, ok := msg.(doctorDoneMsg); ok {
			done = msg
		}
	}
	if done == nil || ran != 1 {
		t.Fatalf("doctor ran %d times, done = %v", ran, done)
	}
	model, _ = m.Update(done)
	view := ansi.Strip(model.(Model).render())
	if !strings.Contains(view, "fix: gh auth login") || !strings.Contains(view, "exit 1") {
		t.Fatalf("report not shown:\n%s", view)
	}
	assertScreen(t, model.(Model).render(), 80, 24)
}
