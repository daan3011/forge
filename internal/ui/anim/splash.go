package anim

import (
	"math"

	"github.com/daan3011/forge/internal/ui"
)

// Splash timing and size.
const (
	// SplashLength is the splash's length in frames (~0.92 s).
	SplashLength = 11
	// SplashMinWidth and SplashMinHeight are the smallest terminal the splash
	// is shown in.
	SplashMinWidth  = 60
	SplashMinHeight = 16
)

// Splash art, on a splashW×splashH canvas.
const (
	splashW = 36
	splashH = 15

	anvilX, anvilY   = 5, 6
	hammerX          = 15
	strikeTop        = 3 // hammer top row when the head rests on the anvil
	wordY            = 12
	sparksPerStrike  = 9
	sparkGravity     = 0.15
	impactY          = anvilY - 1
	impactHalfSpread = 2
)

var (
	splashAnvil = []string{
		"▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄",
		"▝▀▀▀▀▀▀██████████████████▛",
		"        ▝▀▀▜██████▛▀▀▀▀▘",
		"           ▐██████▌",
		"        ▗▄▄████████▄▄▖",
	}
	splashHead   = []string{"▄▄▄▄", "████", "▀▀▀▀"}
	splashHandle = "━━━━━━━━━"
	// splashWord is "forge" in a three-row block font.
	splashWord = []string{
		"┏━╸┏━┓┏━┓┏━╸┏━╸",
		"┣╸ ┃ ┃┣┳┛┃╺┓┣╸ ",
		"╹  ┗━┛╹┗╸┗━┛┗━╸",
	}
	// hammerTops is the hammer's top row per frame: up, down, strike, up,
	// up, down, strike, then resting just above the anvil.
	hammerTops = [SplashLength]int{0, 1, strikeTop, 1, 0, 1, strikeTop, 2, 2, 2, 2}
	// strikes are the frames the hammer hits the anvil.
	strikes = []int{2, 6}
)

// Splash is the launch animation: the hammer strikes the anvil twice,
// sparks fly up and fade, and the wordmark heats from dark red to
// white-hot.
type Splash struct {
	Start int    // engine frame the splash began on
	Seed  uint64 // fixes the spark pattern
}

// SplashFits reports whether a w×h terminal is big enough for the splash.
func SplashFits(w, h int) bool { return w >= SplashMinWidth && h >= SplashMinHeight }

// Done reports whether the splash has finished at frame.
func (s Splash) Done(frame int) bool { return frame-s.Start >= SplashLength }

// View renders the splash at frame, centred in exactly w×h cells.
func (s Splash) View(frame, w, h int, th *ui.Theme) string {
	f := min(max(frame-s.Start, 0), SplashLength-1)
	g := newGrid(w, h)
	ox, oy := (w-splashW)/2, (h-splashH)/2

	// Sparks from every strike so far, drawn first so the hammer and anvil
	// hide the ones behind them.
	for i, k := range strikes {
		if f < k {
			continue
		}
		s.sparks(g, ox, oy, f-k, uint64(i))
	}

	// Anvil; its face flashes on a strike.
	g.art(ox+anvilX, oy+anvilY, splashAnvil, th.Border)
	for _, k := range strikes {
		switch f {
		case k:
			g.text(ox+anvilX, oy+anvilY, splashAnvil[0], ui.Heat(0.85))
		case k + 1:
			g.text(ox+anvilX, oy+anvilY, splashAnvil[0], ui.Heat(0.4))
		}
	}

	// Hammer.
	top := hammerTops[f]
	g.art(ox+hammerX, oy+top, splashHead, th.Text)
	g.text(ox+hammerX+len([]rune(splashHead[1])), oy+top+1, splashHandle, th.Muted)

	// Wordmark, heating up across the splash.
	heat := float64(f) / float64(SplashLength-1)
	wx := ox + (splashW-width(splashWord))/2
	g.art(wx, oy+wordY, splashWord, ui.Heat(heat))
	for y := range splashWord {
		for x := range []rune(splashWord[y]) {
			g.bold(wx+x, oy+wordY+y)
		}
	}
	return g.String()
}

// sparks draws one strike's sparks age frames after it.
func (s Splash) sparks(g *grid, ox, oy, age int, strike uint64) {
	r := rng(s.Seed, 0x5a1a54+strike)
	for i := range sparksPerStrike {
		side := 1.0
		if i%2 == 0 {
			side = -1
		}
		x0 := float64(hammerX + 2 + int(side)*impactHalfSpread)
		vx := side * (0.6 + r.Float64()*2.2)
		vy := -(0.9 + r.Float64()*1.1)
		life := 3 + r.IntN(3)
		if age == 0 || age > life {
			continue // born under the hammer head; gone once cooled
		}
		a := float64(age)
		x := x0 + vx*a
		y := float64(impactY) + vy*a + sparkGravity*a*a
		glyph := '·'
		switch {
		case age == 1:
			glyph = '✦'
		case age <= 3:
			glyph = '*'
		}
		g.set(ox+int(math.Round(x)), oy+int(math.Round(y)), glyph, ui.Heat(1-float64(age-1)/float64(life)))
	}
}
