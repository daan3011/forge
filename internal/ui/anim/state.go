package anim

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/ui"
)

// The pane states every tab shares (P6.4): loading, failed and a notice
// such as "not set up". Each renders exactly w×h with its lines centred,
// so tabs look alike whatever they show.

// Loading is the body of a pane whose first data is still on its way: the
// hammer (HammerFrame at frame) above label. The tab keeps the clock
// running while it is visible (see Ticks).
func Loading(label string, frame int, s Settings, th *ui.Theme, w, h int) string {
	lines := []string{HammerFrame(frame, s, th), ""}
	if h < 3 {
		lines = nil // no room: the label alone
	}
	return centred(append(lines, wrap(th.TableEmpty.Render(label), w)...), w, h)
}

// Ticks reports whether a visible Loading body needs the animation clock:
// the hammer moves (full) or pulses (reduced); off draws it static.
func Ticks(s Settings) bool { return s.Motion() || s.Pulses() }

// Failed is the body of a pane whose load failed before anything was
// shown: what went wrong, and that retryKey loads it again.
func Failed(msg, retryKey string, th *ui.Theme, w, h int) string {
	lines := wrap(th.TableEmpty.Render(msg), w)
	if retryKey != "" {
		lines = append(lines, "", th.Hint.Render(retryKey+" to retry"))
	}
	return centred(lines, w, h)
}

// Notice is the body of a pane with nothing to show for a reason the user
// can act on (not set up, no matches): msg, then hint under it.
func Notice(msg, hint string, th *ui.Theme, w, h int) string {
	lines := wrap(th.TableEmpty.Render(msg), w)
	if hint != "" {
		lines = append(lines, "")
		lines = append(lines, wrap(th.Hint.Render(hint), w)...)
	}
	return centred(lines, w, h)
}

// wrap breaks s at word boundaries to fit w.
func wrap(s string, w int) []string {
	if w <= 0 {
		return nil
	}
	return strings.Split(ansi.Wrap(s, w, ""), "\n")
}

// centred places lines in the middle of a w×h block, each line centred;
// lines that don't fit are cut.
func centred(lines []string, w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	out := make([]string, 0, h)
	top := (h - len(lines)) / 2
	blank := strings.Repeat(" ", w)
	for range top {
		out = append(out, blank)
	}
	for _, l := range lines {
		lw := ansi.StringWidth(l)
		if lw > w {
			l, lw = ansi.Truncate(l, w, "…"), w
			lw = min(lw, ansi.StringWidth(l))
		}
		pad := (w - lw) / 2
		out = append(out, strings.Repeat(" ", pad)+l+strings.Repeat(" ", w-pad-lw))
	}
	for len(out) < h {
		out = append(out, blank)
	}
	return strings.Join(out, "\n")
}
