package anim

import (
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/config"
)

// StaticSpinner is what Spinner shows with animations off.
const StaticSpinner = "…"

var lastSpinnerID atomic.Int64

// SpinnerTickMsg advances a Spinner by one frame.
type SpinnerTickMsg struct{ id, gen int }

// Spinner is the loading indicator. It is a stand-in until P0.9's hammer
// spinner and has the same contract: it only ticks between Start and Stop,
// so the owner starts it while a visible fetch runs. Animations "reduced"
// halves the frame rate; "off" shows StaticSpinner and never ticks.
type Spinner struct {
	frames  []string
	fps     time.Duration
	level   config.Animations
	id      int
	gen     int
	frame   int
	running bool
}

// NewSpinner returns a stopped spinner for the given animation level.
func NewSpinner(level config.Animations) Spinner {
	s := Spinner{
		frames: spinner.MiniDot.Frames,
		fps:    spinner.MiniDot.FPS,
		level:  level,
		id:     int(lastSpinnerID.Add(1)),
	}
	if level == config.AnimationsReduced {
		s.fps *= 2
	}
	return s
}

// Running reports whether the spinner is between Start and Stop.
func (s Spinner) Running() bool { return s.running }

// Start begins ticking. It returns nil if the spinner is already running or
// animations are off.
func (s *Spinner) Start() tea.Cmd {
	if s.running {
		return nil
	}
	s.running = true
	if s.level == config.AnimationsOff {
		return nil
	}
	s.gen++ // orphan any tick left over from before Stop
	return s.tick()
}

// Stop halts ticking; a tick already in flight is dropped.
func (s *Spinner) Stop() { s.running = false }

// Update advances the spinner on its own tick. handled is false for any
// other message, so the caller can pass it on.
func (s *Spinner) Update(msg tea.Msg) (handled bool, cmd tea.Cmd) {
	t, ok := msg.(SpinnerTickMsg)
	if !ok || t.id != s.id {
		return false, nil
	}
	if !s.running || t.gen != s.gen {
		return true, nil
	}
	s.frame = (s.frame + 1) % len(s.frames)
	return true, s.tick()
}

// View returns the current frame.
func (s Spinner) View() string {
	if s.level == config.AnimationsOff {
		return StaticSpinner
	}
	return s.frames[s.frame]
}

func (s *Spinner) tick() tea.Cmd {
	msg := SpinnerTickMsg{id: s.id, gen: s.gen}
	return tea.Tick(s.fps, func(time.Time) tea.Msg { return msg })
}
