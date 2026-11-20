package anim

import (
	"testing"

	"github.com/daan3011/forge/internal/config"
)

func TestSpinnerLevels(t *testing.T) {
	tests := []struct {
		level     config.Animations
		wantTicks bool
	}{
		{config.AnimationsFull, true},
		{config.AnimationsReduced, true},
		{config.AnimationsOff, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.level), func(t *testing.T) {
			s := NewSpinner(tt.level)
			if cmd := s.Start(); (cmd != nil) != tt.wantTicks {
				t.Fatalf("Start cmd = %v, want ticks %v", cmd != nil, tt.wantTicks)
			}
			if !s.Running() {
				t.Fatal("not running after Start")
			}
			if s.Start() != nil {
				t.Fatal("second Start started another tick loop")
			}
			if !tt.wantTicks && s.View() != StaticSpinner {
				t.Fatalf("View = %q", s.View())
			}
		})
	}
	if full, reduced := NewSpinner(config.AnimationsFull), NewSpinner(config.AnimationsReduced); reduced.fps != 2*full.fps {
		t.Fatalf("reduced fps %s, full %s", reduced.fps, full.fps)
	}
}

func TestSpinnerTicks(t *testing.T) {
	s := NewSpinner(config.AnimationsFull)
	other := NewSpinner(config.AnimationsFull)
	s.Start()
	first := s.View()

	if handled, _ := s.Update(SpinnerTickMsg{id: other.id, gen: s.gen}); handled {
		t.Fatal("handled another spinner's tick")
	}
	handled, cmd := s.Update(SpinnerTickMsg{id: s.id, gen: s.gen})
	if !handled || cmd == nil || s.View() == first {
		t.Fatalf("tick: handled %v, cmd %v, frame %q", handled, cmd != nil, s.View())
	}

	stale := SpinnerTickMsg{id: s.id, gen: s.gen}
	s.Stop()
	if _, cmd := s.Update(stale); cmd != nil {
		t.Fatal("ticked after Stop")
	}
	s.Start()
	if _, cmd := s.Update(stale); cmd != nil {
		t.Fatal("tick from before Stop restarted a second loop")
	}
	if _, cmd := s.Update(SpinnerTickMsg{id: s.id, gen: s.gen}); cmd == nil {
		t.Fatal("current tick dropped")
	}
}
