package app

import (
	"bytes"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/config"
)

func TestQuitFlow(t *testing.T) {
	tm := teatest.NewTestModel(t, New(config.Default()), teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("forge"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyPressMsg{Code: 'q', Text: "q"})
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))
}

func TestQuitKeys(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.KeyPressMsg
		quit bool
	}{
		{"q", tea.KeyPressMsg{Code: 'q', Text: "q"}, true},
		{"ctrl+c", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, true},
		{"x", tea.KeyPressMsg{Code: 'x', Text: "x"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, cmd := New(config.Default()).Update(tt.msg)
			gotQuit := cmd != nil && isQuit(cmd())
			if gotQuit != tt.quit {
				t.Fatalf("quit = %v, want %v", gotQuit, tt.quit)
			}
		})
	}
}

func isQuit(msg tea.Msg) bool {
	_, ok := msg.(tea.QuitMsg)
	return ok
}
