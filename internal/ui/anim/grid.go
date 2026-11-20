package anim

import (
	"image/color"
	"math/rand/v2"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/ui"
)

// cell is one character of a grid; a nil colour means unstyled.
type cell struct {
	r    rune
	c    color.Color
	bold bool
}

// grid is a fixed-size character canvas the drawings are composed on.
type grid struct {
	w, h  int
	cells []cell
}

func newGrid(w, h int) *grid {
	g := &grid{w: max(w, 0), h: max(h, 0)}
	g.cells = make([]cell, g.w*g.h)
	for i := range g.cells {
		g.cells[i].r = ' '
	}
	return g
}

// set writes one cell; out-of-range positions are ignored.
func (g *grid) set(x, y int, r rune, c color.Color) {
	if x < 0 || y < 0 || x >= g.w || y >= g.h {
		return
	}
	g.cells[y*g.w+x] = cell{r: r, c: c}
}

// text writes s from (x, y), one rune per cell; spaces are transparent.
func (g *grid) text(x, y int, s string, c color.Color) {
	for i, r := range []rune(s) {
		if r != ' ' {
			g.set(x+i, y, r, c)
		}
	}
}

// art writes a multi-line drawing with its top-left at (x, y).
func (g *grid) art(x, y int, lines []string, c color.Color) {
	for i, l := range lines {
		g.text(x, y+i, l, c)
	}
}

func (g *grid) bold(x, y int) {
	if x >= 0 && y >= 0 && x < g.w && y < g.h {
		g.cells[y*g.w+x].bold = true
	}
}

// String renders the grid, one styled run per colour change.
func (g *grid) String() string {
	var b strings.Builder
	for y := range g.h {
		if y > 0 {
			b.WriteByte('\n')
		}
		row := g.cells[y*g.w : (y+1)*g.w]
		for i := 0; i < len(row); {
			j := i
			var run strings.Builder
			for j < len(row) && sameStyle(row[j], row[i]) {
				run.WriteRune(row[j].r)
				j++
			}
			b.WriteString(paint(row[i], run.String()))
			i = j
		}
	}
	return b.String()
}

func sameStyle(a, b cell) bool {
	if a.bold != b.bold || (a.c == nil) != (b.c == nil) {
		return false
	}
	if a.c == nil {
		return true
	}
	ar, ag, ab, aa := a.c.RGBA()
	br, bg, bb, ba := b.c.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

func paint(c cell, s string) string {
	switch {
	case c.c == nil || strings.TrimSpace(s) == "":
		return s
	case c.bold:
		return ui.TintBold(c.c, s)
	default:
		return ui.Tint(c.c, s)
	}
}

// width is the display width of the widest line.
func width(lines []string) int {
	w := 0
	for _, l := range lines {
		w = max(w, ansi.StringWidth(l))
	}
	return w
}

// rng returns a deterministic generator, so seeded effects render the same
// frames every time (and in golden files).
func rng(seed uint64, salt uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed, salt^0x9e3779b97f4a7c15))
}
