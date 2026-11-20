package anim

import (
	"github.com/daan3011/forge/internal/ui"
)

// Status is an agent session's state, as shown by Flame.
type Status int

// Agent statuses.
const (
	StatusIdle    Status = iota // cold grey anvil
	StatusWorking               // flickering flame
	StatusWaiting               // slow amber pulse
	StatusError                 // cracked red
	StatusDead                  // dim ash
)

// Statuses lists every status, in display order.
var Statuses = []Status{StatusWorking, StatusWaiting, StatusIdle, StatusError, StatusDead}

func (s Status) String() string {
	switch s {
	case StatusWorking:
		return "working"
	case StatusWaiting:
		return "waiting"
	case StatusError:
		return "error"
	case StatusDead:
		return "dead"
	default:
		return "idle"
	}
}

// Flame glyphs.
const (
	flameGlyph   = "▲"
	emberGlyph   = "●"
	anvilGlyph   = "▬"
	crackedGlyph = "╳"
	ashGlyph     = "·"
)

// flickerGlyphs is the working flame in full mode.
var flickerGlyphs = [...]string{"▲", "▴", "▲", "♦"}

// waitingPeriod is the amber pulse period in frames (1.5 s).
const waitingPeriod = 18

// Flame renders the one-cell status icon of an agent at frame.
func Flame(st Status, frame int, s Settings, th *ui.Theme) string {
	switch st {
	case StatusWorking:
		switch {
		case s.Motion() || s.Pulses():
			r := rng(uint64(frame/2), 0xf1a3e)
			heat := 0.45 + r.Float64()*0.45
			glyph := flameGlyph
			if s.Motion() {
				glyph = flickerGlyphs[r.IntN(len(flickerGlyphs))]
			}
			return ui.Tint(ui.Heat(heat), glyph)
		default:
			return ui.Tint(th.Accent, flameGlyph)
		}
	case StatusWaiting:
		if s.Pulses() {
			return ui.Tint(ui.Heat(0.3+0.4*pulse(frame, waitingPeriod)), emberGlyph)
		}
		return ui.Tint(th.Highlight, emberGlyph)
	case StatusError:
		return ui.Tint(th.Error, crackedGlyph)
	case StatusDead:
		return ui.Tint(th.Muted, ashGlyph)
	default:
		return ui.Tint(th.Border, anvilGlyph)
	}
}

// FlameAnimating reports whether Flame changes over time for st, so the
// owner only keeps the clock running for visible working/waiting agents.
func FlameAnimating(st Status, s Settings) bool {
	switch st {
	case StatusWorking:
		return s.Motion() || s.Pulses()
	case StatusWaiting:
		return s.Pulses()
	default:
		return false
	}
}
