package tmuxdemo

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/sessions/tmux/tmuxtest"
)

var demoTarget = tmux.Target{Session: Project, Window: Window}

// step feeds msg to m and returns the new model plus the message its cmd
// produced (nil for no cmd).
func step(t *testing.T, m Model, msg tea.Msg) (Model, tea.Msg) {
	t.Helper()
	next, cmd := m.Update(msg)
	if cmd == nil {
		return next.(Model), nil
	}
	return next.(Model), cmd()
}

func press(code rune) tea.KeyPressMsg {
	if code == tea.KeyEnter {
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	}
	return tea.KeyPressMsg{Code: code, Text: string(code)}
}

func started(t *testing.T, f *tmuxtest.Fake) Model {
	t.Helper()
	m := New(f, "/work")
	m, msg := step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if msg != nil {
		t.Fatalf("resize produced %T", msg)
	}
	msg = m.start()()
	m, msg = step(t, m, msg) // readyMsg -> capture
	if _, ok := msg.(previewMsg); !ok {
		t.Fatalf("after ready got %T (%v)", msg, msg)
	}
	m, _ = step(t, m, msg)
	return m
}

func TestDemoStartsBashAndPreviews(t *testing.T) {
	f := tmuxtest.NewFake()
	m := started(t, f)
	dir, cmd, _, ok := f.Started(demoTarget)
	if !ok || dir != "/work" || len(cmd) != 1 || cmd[0] != "bash" {
		t.Fatalf("Started = %q %q %v", dir, cmd, ok)
	}
	if !m.ready || !m.alive {
		t.Errorf("ready=%v alive=%v", m.ready, m.alive)
	}

	f.SetOutput(demoTarget, "$ echo hi\nhi\n$")
	_, msg := step(t, m, pollMsg{gen: m.gen})
	m, _ = step(t, m, msg)
	if !strings.Contains(m.render(), "hi") {
		t.Errorf("preview missing output:\n%s", m.render())
	}

	// A poll from before a restart is ignored.
	if _, msg := step(t, m, pollMsg{gen: m.gen - 1}); msg != nil {
		t.Errorf("stale poll ran %T", msg)
	}
}

func TestDemoAttachAndDetach(t *testing.T) {
	f := tmuxtest.NewFake()
	m := started(t, f)

	m, msg := step(t, m, press(tea.KeyEnter))
	am, ok := msg.(attachMsg)
	if !ok || am.a.Cmd == nil || am.a.Mode != tmux.AttachExec {
		t.Fatalf("enter gave %T %+v", msg, msg)
	}
	if got := f.Attached(); len(got) != 1 || got[0] != demoTarget {
		t.Errorf("Attached = %v", got)
	}
	_, cmd := m.Update(am)
	if cmd == nil {
		t.Fatal("attachMsg should return tea.ExecProcess")
	}

	// tea.ExecProcess resumes with detachedMsg once tmux exits.
	m, msg = step(t, m, detachedMsg{})
	if !strings.Contains(m.status, "back in forge") {
		t.Errorf("status = %q", m.status)
	}
	if _, ok := msg.(previewMsg); !ok {
		t.Errorf("detach should refresh preview, got %T", msg)
	}
}

func TestDemoSwitchInPlace(t *testing.T) {
	f := tmuxtest.NewFake()
	f.SwitchInPlace = true
	m := started(t, f)
	m, msg := step(t, m, press(tea.KeyEnter))
	next, cmd := m.Update(msg)
	if cmd != nil {
		t.Error("in-place switch should not exec")
	}
	if s := next.(Model).status; !strings.Contains(s, "switched") {
		t.Errorf("status = %q", s)
	}
}

func TestDemoPopup(t *testing.T) {
	f := tmuxtest.NewFake()
	f.Popup = true
	f.Warning = tmux.OuterMdWarning
	m := started(t, f)
	m, msg := step(t, m, press(tea.KeyEnter))
	if am, ok := msg.(attachMsg); !ok || am.a.Mode != tmux.AttachPopup {
		t.Fatalf("enter gave %T %+v", msg, msg)
	}
	// The popup command runs in a plain tea.Cmd (forge keeps the terminal)
	// and reports detachedMsg once the popup closes.
	m, msg = step(t, m, msg)
	if _, ok := msg.(detachedMsg); !ok {
		t.Fatalf("popup cmd gave %T", msg)
	}
	if !strings.Contains(m.render(), "unbind -n M-d") {
		t.Error("M-d warning not shown")
	}
	m, msg = step(t, m, msg)
	if !strings.Contains(m.status, "back in forge") {
		t.Errorf("status = %q", m.status)
	}
	if _, ok := msg.(previewMsg); !ok {
		t.Errorf("popup close should refresh preview, got %T", msg)
	}
}

func TestDemoKillRestartQuit(t *testing.T) {
	ctx := context.Background()
	f := tmuxtest.NewFake()
	m := started(t, f)

	m, msg := step(t, m, press('x'))
	m, _ = step(t, m, msg)
	if alive, _ := f.WindowAlive(ctx, demoTarget); alive || m.ready {
		t.Errorf("after kill: fake alive=%v ready=%v", alive, m.ready)
	}

	m, msg = step(t, m, press('n'))
	m, msg = step(t, m, msg)
	if _, ok := msg.(previewMsg); !ok || !m.ready {
		t.Fatalf("restart: %T ready=%v", msg, m.ready)
	}

	f.Exit(demoTarget, 0)
	m, msg = step(t, m, pollMsg{gen: m.gen})
	m, _ = step(t, m, msg)
	if m.alive || !strings.Contains(m.render(), "exited") {
		t.Errorf("exited window not shown:\n%s", m.render())
	}

	_, msg = step(t, m, press('q'))
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("quit gave %T", msg)
	}
	if ws, _ := f.ListWindows(ctx, Project); len(ws) != 0 {
		t.Errorf("quit left windows: %+v", ws)
	}
}
