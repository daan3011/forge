package refresh

import (
	"testing"
	"time"

	"github.com/daan3011/forge/internal/config"
)

func TestIntervalsFromConfig(t *testing.T) {
	cfg := config.Default()
	cfg.GitHub.RefreshInterval.Duration = 30 * time.Second
	s := New(cfg)
	tests := []struct {
		src  Source
		want time.Duration
	}{
		{GitHub, 30 * time.Second},
		{Plane, 2 * time.Minute},
		{Sessions, 10 * time.Second},
	}
	for _, tt := range tests {
		if got := s.Interval(tt.src); got != tt.want {
			t.Errorf("Interval(%s) = %s, want %s", tt.src, got, tt.want)
		}
	}
	if s.Start() == nil {
		t.Fatal("Start returned nil")
	}
}

func TestHandle(t *testing.T) {
	s := New(config.Default())
	tests := []struct {
		name   string
		msg    TickMsg
		wantOK bool
	}{
		{"current tick", s.Msg(GitHub), true},
		{"stale gen", TickMsg{Source: GitHub, Gen: -1}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, next := s.Handle(tt.msg)
			if ok != tt.wantOK || (next != nil) != tt.wantOK {
				t.Fatalf("Handle = %v, %v; want ok %v", ok, next != nil, tt.wantOK)
			}
		})
	}
	var zero Scheduler
	if ok, _ := zero.Handle(TickMsg{Source: GitHub}); ok {
		t.Fatal("zero scheduler accepted a tick")
	}
}

func TestBackoff(t *testing.T) {
	s := New(config.Default())
	old := s.Msg(GitHub)

	if s.SetDelay(GitHub, 5*time.Minute) == nil {
		t.Fatal("SetDelay did not re-arm")
	}
	if got := s.Interval(GitHub); got != 5*time.Minute {
		t.Fatalf("Interval = %s", got)
	}
	if ok, _ := s.Handle(old); ok {
		t.Fatal("tick from the replaced timer was accepted")
	}
	if s.SetDelay(GitHub, 5*time.Minute) != nil {
		t.Fatal("same delay re-armed")
	}
	if ok, _ := s.Handle(s.Msg(GitHub)); !ok {
		t.Fatal("new tick rejected")
	}
	if got := s.Interval(Plane); got != 2*time.Minute {
		t.Fatalf("backoff leaked into plane: %s", got)
	}

	if s.ClearDelay(GitHub) == nil {
		t.Fatal("ClearDelay did not re-arm")
	}
	if got := s.Interval(GitHub); got != time.Minute {
		t.Fatalf("Interval after clear = %s", got)
	}
	if s.ClearDelay(GitHub) != nil {
		t.Fatal("ClearDelay without override re-armed")
	}
}

func TestShortIntervalsAreClamped(t *testing.T) {
	cfg := config.Default()
	cfg.Plane.RefreshInterval.Duration = 0
	cfg.Sessions.RefreshInterval.Duration = time.Second
	s := New(cfg)
	tests := []struct {
		src  Source
		want time.Duration
	}{
		{GitHub, time.Minute},
		{Plane, MinInterval},
		{Sessions, MinInterval},
	}
	for _, tt := range tests {
		if got := s.Interval(tt.src); got != tt.want {
			t.Errorf("Interval(%s) = %s, want %s", tt.src, got, tt.want)
		}
	}
	want := []string{
		"plane.refresh_interval is 0s, below 5s; using 5s",
		"sessions.refresh_interval is 1s, below 5s; using 5s",
	}
	if got := s.Warnings(); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("warnings = %q", got)
	}
	if w := New(config.Default()).Warnings(); len(w) != 0 {
		t.Fatalf("default config warns: %q", w)
	}
	// A backoff override below the minimum is clamped too.
	s.SetDelay(GitHub, time.Millisecond)
	if got := s.Interval(GitHub); got != MinInterval {
		t.Fatalf("backoff Interval = %s", got)
	}
}
