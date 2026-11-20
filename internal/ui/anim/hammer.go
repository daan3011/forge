package anim

import (
	"math"

	"charm.land/bubbles/v2/spinner"

	"github.com/daan3011/forge/internal/ui"
)

// hammerFrames are the hammer spinner: hammer raised, tipping, striking the
// anvil with a spark, rebounding while the spark flares.
var hammerFrames = [...][3]rune{
	{'┳', '▂', ' '},
	{'┓', '▂', ' '},
	{'━', '▂', '*'},
	{'┳', '▂', '✦'},
}

// HammerStatic is the hammer spinner without motion.
const HammerStatic = "⚒"

// Hammer is the hammer spinner as a bubbles spinner, a drop-in for
// spinner.MiniDot and friends: spinner.New(spinner.WithSpinner(anim.Hammer)).
// Inside forge prefer HammerFrame, which runs on the shared Engine.
var Hammer = spinner.Spinner{
	Frames: func() []string {
		out := make([]string, len(hammerFrames))
		for i, f := range hammerFrames {
			out[i] = string(f[:])
		}
		return out
	}(),
	FPS: Interval * 2, // one hammer frame per two engine frames
}

// HammerFrame renders the loading indicator at frame. Full: the hammer
// strikes. Reduced: a static hammer whose colour pulses. Off: a static
// hammer.
func HammerFrame(frame int, s Settings, th *ui.Theme) string {
	switch {
	case s.Motion():
		f := hammerFrames[(frame/2)%len(hammerFrames)]
		g := newGrid(3, 1)
		g.set(0, 0, f[0], th.Text)
		g.set(1, 0, f[1], th.Border)
		g.set(2, 0, f[2], ui.Heat(0.9))
		return g.String()
	case s.Pulses():
		return ui.Tint(ui.Mix(th.Accent, th.Highlight, pulse(frame, 12)), HammerStatic)
	default:
		return ui.Tint(th.Accent, HammerStatic)
	}
}

// pulse is a smooth 0..1..0 wave with the given period in frames.
func pulse(frame, period int) float64 {
	return (1 - math.Cos(2*math.Pi*float64(frame%period)/float64(period))) / 2
}
