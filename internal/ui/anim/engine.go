package anim

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// FPS is the frame rate of the shared animation clock. Every frame count in
// this package (durations, periods) is in these frames.
const FPS = 12

// Interval is the time between frames at full animation.
const Interval = time.Second / FPS

// reducedStep is how many frames one tick advances in reduced mode: it ticks
// at half rate but keeps frame-based timings the same length.
const reducedStep = 2

// FrameMsg is the shared clock tick. Only the Engine that scheduled it acts
// on it.
type FrameMsg struct {
	engine *Engine
	gen    int
}

// Engine is the one animation clock. Components are pure functions of the
// engine's frame number; the owner calls Sync after every message with
// whether anything visible is animating, and the engine schedules at most one
// tick at a time, only while that is true. With animations off it never
// schedules anything.
//
// It is shared by pointer (like *ui.Theme) so tabs can read Frame in View.
type Engine struct {
	s       Settings
	frame   int
	gen     int
	pending bool // a tick for gen is in flight
}

// NewEngine returns a stopped engine.
func NewEngine(s Settings) *Engine { return &Engine{s: s} }

// Settings returns the animation settings.
func (e *Engine) Settings() Settings { return e.s }

// SetSettings changes the level; any tick in flight is dropped. Call Sync
// afterwards.
func (e *Engine) SetSettings(s Settings) {
	e.s = s
	e.stop()
}

// Frame returns the current frame number. It only grows.
func (e *Engine) Frame() int { return e.frame }

// Ticking reports whether a tick is scheduled.
func (e *Engine) Ticking() bool { return e.pending }

// Sync schedules the next tick if want is true and none is in flight, or
// cancels the pending one if want is false.
func (e *Engine) Sync(want bool) tea.Cmd {
	if !want || e.s.Off() {
		e.stop()
		return nil
	}
	if e.pending {
		return nil
	}
	e.pending = true
	msg := FrameMsg{engine: e, gen: e.gen}
	d := Interval
	if !e.s.Motion() {
		d *= reducedStep
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return msg })
}

// Update advances the frame on this engine's own tick. handled is false for
// any other message. The caller must call Sync afterwards to keep ticking.
func (e *Engine) Update(msg tea.Msg) (handled bool) {
	f, ok := msg.(FrameMsg)
	if !ok || f.engine != e {
		return false
	}
	if !e.pending || f.gen != e.gen {
		return true // stale
	}
	e.pending = false
	if e.s.Motion() {
		e.frame++
	} else {
		e.frame += reducedStep
	}
	return true
}

func (e *Engine) stop() {
	if e.pending {
		e.gen++
		e.pending = false
	}
}
