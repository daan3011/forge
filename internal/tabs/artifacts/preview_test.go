package artifacts

import (
	"bytes"
	"image"
	"image/png"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/chafa"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui/termcaps"
)

var (
	kittyCaps = termcaps.Caps{Protocol: termcaps.Kitty, CellW: 10, CellH: 20, Chafa: true, Terminal: "kitty"}
	sixelCaps = termcaps.Caps{Protocol: termcaps.Sixel, CellW: 10, CellH: 20, Chafa: true, Terminal: "WezTerm"}
	plainCaps = termcaps.Caps{
		CellW: 8, CellH: 16, Chafa: true, Terminal: "Apple_Terminal",
		Reason: "no graphics support in Apple_Terminal",
	}
)

// pngFile writes a w x h PNG.
func pngFile(t *testing.T, path string, w, h int, mod time.Time) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = uint8(i * 31 % 256)
	}
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return writeFile(t, path, buf.String(), mod)
}

// outcome is what running the tab's commands produced.
type outcome struct {
	raws   []string
	clears int
}

func (o outcome) has(sub string) bool {
	return slices.ContainsFunc(o.raws, func(r string) bool { return strings.Contains(r, sub) })
}

// run is pump, recording raw output and screen clears. Ticks run for
// real (they are short).
func run(tab *Tab, cmd tea.Cmd) outcome {
	var o outcome
	queue := drain(cmd)
	for len(queue) > 0 {
		msg := queue[0]
		queue = queue[1:]
		switch m := msg.(type) {
		case tea.RawMsg:
			o.raws = append(o.raws, m.Msg.(string))
		case tabs.TargetedMsg:
			_, next := tab.Update(m)
			queue = append(queue, drain(next)...)
		default:
			if msg == tea.ClearScreen() {
				o.clears++
			}
		}
	}
	return o
}

// previewTab is alpha's tab, visible, with art.png (newest, selected),
// mock.html and flow.svg.
func previewTab(t *testing.T, caps termcaps.Caps, opts ...func(*Tab)) (*Tab, *fixture) {
	t.Helper()
	f := newFixture(t)
	pngFile(t, filepath.Join(f.alpha, "design", "art.png"), 400, 200, now.Add(-30*time.Minute))
	tab := f.tab(0, nil)
	for _, o := range opts {
		o(tab)
	}
	run(tab, tab.SetCaps(caps))
	refresh(t, tab)
	if got := names(tab); !slices.Equal(got, []string{"art.png", "mock.html", "flow.svg"}) {
		t.Fatalf("rows = %v", got)
	}
	return tab, f
}

func show(t *testing.T, tab *Tab) outcome {
	t.Helper()
	return run(tab, tab.SetVisible(true))
}

func TestKittyPreview(t *testing.T) {
	tab, _ := previewTab(t, kittyCaps)
	o := show(t, tab)
	if !o.has("a=T") || !o.has("i=16") || !o.has("U=1") || o.clears != 0 {
		t.Fatalf("transmit missing: %d raws, %d clears", len(o.raws), o.clears)
	}
	v := tab.View()
	if !strings.Contains(v, string(kitty.Placeholder)) || !strings.Contains(v, "\x1b[38;5;16m") {
		t.Fatalf("no placeholders in view:\n%s", view(tab))
	}
	plain := view(tab)
	for _, s := range []string{"▣ art.png", "png ·", "400×200", "30m ago · repo"} {
		if !strings.Contains(plain, s) {
			t.Errorf("header lacks %q:\n%s", s, plain)
		}
	}

	// Moving to the HTML file frees the image; its placeholders are gone.
	o = run(tab, press(tab, "down"))
	if !o.has("a=d,d=I,i=16") {
		t.Fatalf("old image not deleted: %q", o.raws)
	}
	v = tab.View()
	if strings.Contains(v, string(kitty.Placeholder)) {
		t.Fatal("placeholders left behind")
	}
	if p := view(tab); !strings.Contains(p, "mock") || !strings.Contains(p, "press o to open") {
		t.Fatalf("HTML summary missing:\n%s", p)
	}

	// Back up: a new id, so a late frame of the old image can't show.
	o = run(tab, press(tab, "up"))
	if !o.has("i=17") {
		t.Fatalf("want a fresh id: %q", o.raws)
	}
}

func TestKittyPreviewHiddenAndDialog(t *testing.T) {
	tab, _ := previewTab(t, kittyCaps)
	show(t, tab)
	o := run(tab, tab.SetVisible(false))
	if !o.has("a=d,d=I,i=16") {
		t.Fatal("hiding the tab should free the image")
	}
	// Hidden: selection changes load nothing.
	o = run(tab, press(tab, "down"))
	if len(o.raws) != 0 || tab.pv.gen == 0 && tab.pv.key.path != "" {
		t.Fatalf("hidden tab worked: %q", o.raws)
	}
	run(tab, press(tab, "up"))
	show(t, tab)
	// Opening the tags dialog hides the preview.
	o = run(tab, press(tab, "t"))
	if tab.dialog == nil || !o.has("a=d,d=I,i=17") {
		t.Fatalf("dialog: open=%v raws=%q", tab.dialog != nil, o.raws)
	}
}

func TestSixelPreview(t *testing.T) {
	tab, _ := previewTab(t, sixelCaps)
	o := show(t, tab)
	// 120x30 at a 0.55 split: the detail pane's inside starts at column 67,
	// row 1; below the tab bar (2) and a 4-line header that is row 7.
	want := "\x1b7\x1b[8;68H\x1bP0;1q"
	if !o.has(want) || o.clears != 0 || !tab.pv.sixelOn {
		t.Fatalf("sixel not placed at %q: %q", want, o.raws)
	}
	if strings.Contains(tab.View(), "\x1bP") {
		t.Fatal("sixel data must not be in the view")
	}

	// An overlay clears it; closing the overlay draws it again.
	o = run(tab, tab.SetOccluded(true))
	if o.clears != 1 || tab.pv.sixelOn || len(o.raws) != 0 {
		t.Fatalf("occluded: %+v", o)
	}
	if o = run(tab, tab.SetOccluded(true)); o.clears != 0 {
		t.Fatal("cleared twice")
	}
	o = run(tab, tab.SetOccluded(false))
	if !o.has(want) || !tab.pv.sixelOn {
		t.Fatalf("not redrawn after the overlay: %+v", o)
	}

	// A resize clears it and draws it at the new place.
	tab.SetSize(100, 30)
	o = run(tab, tab.Resized())
	if o.clears != 1 || !o.has("\x1b7\x1b[8;57H\x1bP") {
		t.Fatalf("resize: %+v", o)
	}

	// Moving away clears it; the HTML preview draws nothing raw.
	o = run(tab, press(tab, "down"))
	if o.clears != 1 || len(o.raws) != 0 || tab.pv.sixelOn {
		t.Fatalf("move: %+v", o)
	}
}

func TestSixelInTmuxIsWrappedAndOffset(t *testing.T) {
	c := sixelCaps
	c.Tmux, c.OffsetX, c.OffsetY = true, 10, 1
	tab, _ := previewTab(t, c)
	o := show(t, tab)
	if !o.has("\x1bPtmux;\x1b\x1b7\x1b\x1b[9;78H\x1b\x1bP0;1q") {
		t.Fatalf("not wrapped/offset: %q", o.raws)
	}
}

func TestSixelNotDrawnWhileOccluded(t *testing.T) {
	tab, _ := previewTab(t, sixelCaps)
	run(tab, tab.SetOccluded(true))
	o := show(t, tab)
	if o.has("\x1bP0;1q") || tab.pv.sixelOn {
		t.Fatal("drew under an overlay")
	}
	if o = run(tab, tab.SetOccluded(false)); !o.has("\x1bP0;1q") {
		t.Fatal("not drawn once uncovered")
	}
}

func TestStalePreviewDropped(t *testing.T) {
	tab, _ := previewTab(t, kittyCaps)
	tab.SetVisible(true) // art.png; its tick is never run
	old := tab.pv.gen
	press(tab, "down") // gen 2: mock.html, before the first tick lands
	if tab.pv.gen == old {
		t.Fatal("selection change should bump the generation")
	}
	if _, next := tab.Update(previewTickMsg{gen: old}); next != nil {
		t.Fatal("a stale tick started a load")
	}
	_, next := tab.Update(previewLoadedMsg{gen: old, seq: "late", id: 99})
	if next != nil || tab.pv.kittyID != 0 {
		t.Fatal("a stale load was shown")
	}
}

func TestChafaPreview(t *testing.T) {
	fake := &chafa.Fake{Func: func(string, int, int) (string, error) { return "▓▓▓▓\n░░░░", nil }}
	tab, _ := previewTab(t, plainCaps, func(tab *Tab) { tab.chafa = fake })
	show(t, tab)
	v := view(tab)
	if !strings.Contains(v, "▓▓▓▓") || !strings.Contains(v, "░░░░") {
		t.Fatalf("no chafa art:\n%s", v)
	}
	if calls := fake.Calls(); len(calls) != 1 || filepath.Base(calls[0]) != "art.png" {
		t.Fatalf("chafa calls = %v", calls)
	}
}

func TestCardReasons(t *testing.T) {
	tab, _ := previewTab(t, plainCaps, func(tab *Tab) { tab.chafa = &chafa.Fake{Missing: true} })
	show(t, tab)
	v := view(tab)
	for _, s := range []string{"no graphics support in Apple_Terminal; install chafa", "400 × 200", "Project  alpha"} {
		if !strings.Contains(v, s) {
			t.Errorf("card lacks %q:\n%s", s, v)
		}
	}
	// flow.svg without rsvg-convert says so.
	run(tab, press(tab, "down"))
	run(tab, press(tab, "down"))
	tab.chafa = &chafa.Fake{}
	run(tab, tab.redrawPreview())
	if v := view(tab); !strings.Contains(v, "install librsvg (rsvg-convert) for SVG previews") {
		t.Fatalf("svg card:\n%s", v)
	}
}

func TestPreviewOffShowsFullCard(t *testing.T) {
	tab, _ := previewTab(t, termcaps.Caps{Off: true, Reason: "artifacts.preview is off"})
	o := show(t, tab)
	if len(o.raws) != 0 || !tab.pv.key.empty() {
		t.Fatal("off mode loaded a preview")
	}
	if v := view(tab); !strings.Contains(v, "Created") || strings.Contains(v, "Loading preview") {
		t.Fatalf("want the full card:\n%s", v)
	}
}

func TestRedrawPreviewCommand(t *testing.T) {
	tab, _ := previewTab(t, sixelCaps)
	show(t, tab)
	var redraw *tabs.Command
	for _, c := range tab.Commands() {
		if c.ID == "artifacts_redraw_preview" {
			redraw = &c
		}
	}
	if redraw == nil {
		t.Fatal("no redraw command")
	}
	o := run(tab, redraw.Run())
	if o.clears < 1 || !o.has("\x1bP0;1q") {
		t.Fatalf("redraw: %+v", o)
	}
}

func TestUnchangedRescanKeepsPreview(t *testing.T) {
	tab, _ := previewTab(t, kittyCaps)
	show(t, tab)
	gen := tab.pv.gen
	refresh(t, tab)
	if tab.pv.gen != gen || tab.pv.kittyID == 0 {
		t.Fatal("a rescan with nothing changed reloaded the preview")
	}
	// The file changing reloads it.
	a, _ := tab.selected()
	pngFile(t, a.Path, 100, 100, now.Add(-time.Minute))
	refresh(t, tab)
	if tab.pv.gen == gen {
		t.Fatal("a changed file kept the old preview")
	}
}

// TestPreviewFlow drives a real program: the PNG's placeholders show,
// moving to the HTML mockup shows its summary and leaves no placeholder.
func TestPreviewFlow(t *testing.T) {
	tab, _ := previewTab(t, kittyCaps)
	h := &host{tab: tab}
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(120, 30))
	tm.Send(visibleMsg{})
	h.wait(t, "400×200")
	h.waitRaw(t, string(kitty.Placeholder))
	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	h.wait(t, "press o to open")
	h.waitNoRaw(t, string(kitty.Placeholder))
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t)
}
