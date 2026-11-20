package anim

import (
	"math"

	"github.com/charmbracelet/harmonica"
)

// Spring tuning: settles in roughly 10 frames with a slight overshoot.
const (
	springFreq    = 8.0
	springDamping = 0.75
	// springMaxSteps bounds a spring's run; it is settled by then regardless.
	springMaxSteps = 3 * FPS
)

var harmonicaSpring = harmonica.NewSpring(harmonica.FPS(FPS), springFreq, springDamping)

// Spring moves a value from From to To, starting at engine frame Start. It
// is a pure function of the frame: At re-runs the (few) steps from Start, so
// the same frame always gives the same position.
type Spring struct {
	From, To float64
	Start    int
	// Snap jumps straight to To (reduced and off).
	Snap bool
	// Epsilon is how close to To counts as settled; 0 means 1% of the
	// distance.
	Epsilon float64
}

// NewSpring starts a spring at frame, snapping unless s allows motion.
func NewSpring(from, to float64, frame int, s Settings) Spring {
	return Spring{From: from, To: to, Start: frame, Snap: !s.Motion()}
}

// SpringAt returns a settled spring resting at v.
func SpringAt(v float64) Spring { return Spring{From: v, To: v} }

// At returns the position at frame and whether the spring has settled.
func (sp Spring) At(frame int) (pos float64, settled bool) {
	if sp.Snap || sp.From == sp.To {
		return sp.To, true
	}
	n := frame - sp.Start
	if n <= 0 {
		return sp.From, false
	}
	if n >= springMaxSteps {
		return sp.To, true
	}
	eps := sp.Epsilon
	if eps <= 0 {
		eps = math.Abs(sp.To-sp.From) * 0.01
	}
	pos, vel := sp.From, 0.0
	for range n {
		pos, vel = harmonicaSpring.Update(pos, vel, sp.To)
		if math.Abs(pos-sp.To) < eps && math.Abs(vel) < eps*FPS {
			return sp.To, true
		}
	}
	return pos, false
}

// Settled reports whether the spring is at rest at frame.
func (sp Spring) Settled(frame int) bool {
	_, ok := sp.At(frame)
	return ok
}
