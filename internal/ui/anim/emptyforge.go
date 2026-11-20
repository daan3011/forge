package anim

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/ui"
)

// The cold forge: a hearth of dead coals with one live ember, and an anvil.
var (
	forgeHearth = []string{
		"▗▄▄▄▄▄▄▄▖",
		"▐       ▌",
		"▐▀▀▀▀▀▀▀▌",
		"▐▌     ▐▌",
	}
	forgeCoals = "░▒░ ░▒░" // inside the hearth, row 1; the gap is the ember
	forgeAnvil = []string{
		"▄▄▄▄▄▄▄▄",
		" ▀▜██▛▀ ",
		" ▗████▖ ",
	}
)

const (
	forgeArtW   = 20 // hearth 9 + gap 3 + anvil 8
	forgeArtH   = 4
	forgeAnvilX = 12
	forgeEmberX = 4
)

// EmptyForge renders the empty state, exactly w×h: a cold forge with one
// glowing ember above the caption (ui.EmptyText). If the drawing does not
// fit only the caption is shown. It is static: an empty list never keeps the
// animation clock running.
func EmptyForge(w, h int, th *ui.Theme) string {
	return EmptyForgeCaption(ui.EmptyText, w, h, th)
}

// EmptyForgeCaption is EmptyForge with a custom caption.
func EmptyForgeCaption(caption string, w, h int, th *ui.Theme) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	capW := ansi.StringWidth(caption)
	if capW > w {
		caption = ansi.Truncate(caption, w, "…")
		capW = ansi.StringWidth(caption)
	}
	captionLine := func() string {
		pad := (w - capW) / 2
		return strings.Repeat(" ", pad) + th.TableEmpty.Render(caption) + strings.Repeat(" ", w-pad-capW)
	}
	if w < forgeArtW || h < forgeArtH+2 {
		lines := []string{captionLine()}
		for len(lines) < h {
			lines = append(lines, strings.Repeat(" ", w))
		}
		return strings.Join(lines, "\n")
	}

	g := newGrid(w, h)
	x0 := (w - forgeArtW) / 2
	y0 := max((h-forgeArtH-2)/2, 0)
	g.art(x0, y0, forgeHearth, th.Border)
	g.text(x0+1, y0+1, forgeCoals, th.Muted)
	g.set(x0+forgeEmberX, y0+1, '•', ui.Heat(0.55))
	g.bold(x0+forgeEmberX, y0+1)
	g.art(x0+forgeAnvilX, y0+1, forgeAnvil, th.Border)

	lines := strings.Split(g.String(), "\n")
	lines[y0+forgeArtH+1] = captionLine()
	return strings.Join(lines, "\n")
}
