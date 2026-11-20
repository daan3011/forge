package anim

import (
	"math"

	"charm.land/lipgloss/v2"

	"github.com/daan3011/forge/internal/ui"
)

// BurstDuration is how long a spark burst lasts, in frames (~420 ms).
const BurstDuration = 5

// burstSparks is how many sparks a burst throws.
const burstSparks = 8

// Burst is a short spray of sparks around a point, for success toasts.
type Burst struct {
	Start int    // engine frame the burst began on
	Seed  uint64 // fixes the spray pattern
}

// Particle is one spark of a burst, relative to the burst's centre.
type Particle struct {
	DX, DY int
	Glyph  rune
	Heat   float64 // 1 is white-hot, 0 has cooled to dark red
}

// Done reports whether the burst has finished at frame.
func (b Burst) Done(frame int) bool { return frame-b.Start >= BurstDuration }

// Particles returns the sparks visible at frame: they fly outward, shrink
// from ✦ to * to · and cool as they go.
func (b Burst) Particles(frame int) []Particle {
	age := frame - b.Start
	if age < 0 || age >= BurstDuration {
		return nil
	}
	r := rng(b.Seed, 0xb0057)
	out := make([]Particle, 0, burstSparks)
	for i := range burstSparks {
		angle := 2*math.Pi*float64(i)/burstSparks + (r.Float64()-0.5)*0.6
		speed := 0.7 + r.Float64()*0.6
		life := BurstDuration - r.IntN(2)
		if age >= life {
			continue
		}
		dist := float64(age+1) * speed
		p := Particle{
			// Cells are about twice as tall as wide: stretch x.
			DX:   int(math.Round(math.Cos(angle) * dist * 2)),
			DY:   int(math.Round(math.Sin(angle) * dist)),
			Heat: 1 - float64(age)/float64(BurstDuration),
		}
		switch {
		case age == 0:
			p.Glyph = '✦'
		case age < 3:
			p.Glyph = '*'
		default:
			p.Glyph = '·'
		}
		out = append(out, p)
	}
	return out
}

// View renders the burst centred in a w×h block (spaces elsewhere).
func (b Burst) View(frame, w, h int) string {
	g := newGrid(w, h)
	for _, p := range b.Particles(frame) {
		g.set(w/2+p.DX, h/2+p.DY, p.Glyph, ui.Heat(p.Heat))
	}
	return g.String()
}

// Layers returns one single-cell layer per spark around (x, y), for
// compositing over the screen at z. skip, if non-nil, drops sparks at
// positions it returns true for (such as inside the toast itself).
func (b Burst) Layers(frame, x, y, z int, skip func(x, y int) bool) []*lipgloss.Layer {
	ps := b.Particles(frame)
	out := make([]*lipgloss.Layer, 0, len(ps))
	for _, p := range ps {
		px, py := x+p.DX, y+p.DY
		if px < 0 || py < 0 || (skip != nil && skip(px, py)) {
			continue
		}
		out = append(out, lipgloss.NewLayer(ui.Tint(ui.Heat(p.Heat), string(p.Glyph))).X(px).Y(py).Z(z))
	}
	return out
}
