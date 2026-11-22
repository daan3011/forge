package artifacts

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/preview"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/termcaps"
)

// Inline previews (P4.7). The detail pane shows the selected artifact's
// picture, chafa text art or HTML summary under a compact header.
//
// Kitty images use Unicode placeholders, so they are ordinary cells: when
// the view changes they go with it. Sixel images are drawn over the cells
// with tea.Raw, so the tab clears them (tea.ClearScreen repaints
// everything) whenever they could be moved or covered, and draws them
// again once things settle.

// Preview timing.
const (
	// previewDebounce waits for the selection to settle, so scrolling
	// fast doesn't decode every image on the way.
	previewDebounce = 120 * time.Millisecond
	// sixelDelay lets the frame that blanks the image area reach the
	// terminal before the sixel is drawn over it.
	sixelDelay = 50 * time.Millisecond
	// previewTimeout bounds one load (decode, rsvg-convert, chafa).
	previewTimeout = 10 * time.Second
	// minPreviewRows is the smallest area worth previewing in.
	minPreviewRows = 3
)

// Kitty image ids are 256-colour indexes in the placeholder text; 16-255
// avoids the 16 basic colours, which some layers rewrite.
const (
	firstKittyID = 16
	lastKittyID  = 255
)

// previewKey is what the preview shows: the file as it is now, where, at
// what size and for which terminal. A new key means a new preview.
type previewKey struct {
	path       string
	mod        time.Time
	size       int64
	typ        string
	top        int // first row of the preview area in the detail pane
	cols, rows int
	caps       termcaps.Caps
}

func (k previewKey) empty() bool { return k.path == "" }

// previewState is the tab's preview bookkeeping.
type previewState struct {
	caps     termcaps.Caps
	visible  bool
	occluded bool

	key previewKey // what should be shown; zero for nothing
	gen int        // bumped with key; older ticks and loads are dropped
	res *preview.Result

	nextID   int    // next kitty image id
	kittyID  int    // the kitty image on screen, 0 for none
	kittyDel string // the sequence that deletes it
	sixel    string // the placed sixel for res, ready to draw
	sixelOn  bool   // a sixel is on screen
}

type previewTickMsg struct{ gen int }

type previewLoadedMsg struct {
	gen int
	res preview.Result
	seq string // kitty transmit or placed sixel ("" for neither)
	id  int    // kitty image id
	del string // kitty delete sequence
}

type sixelDrawMsg struct{ gen int }

// TargetTab implements tabs.TargetedMsg.
func (previewTickMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (previewLoadedMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (sixelDrawMsg) TargetTab() string { return Title }

var (
	_ tabs.GraphicsAware   = (*Tab)(nil)
	_ tabs.VisibilityAware = (*Tab)(nil)
)

// SetCaps implements tabs.GraphicsAware.
func (t *Tab) SetCaps(c termcaps.Caps) tea.Cmd {
	t.pv.caps = c
	return t.syncPreview()
}

// SetOccluded implements tabs.GraphicsAware: a sixel under an overlay is
// cleared, and drawn again when the overlay goes.
func (t *Tab) SetOccluded(covered bool) tea.Cmd {
	p := &t.pv
	p.occluded = covered
	if covered {
		if p.sixelOn {
			p.sixelOn = false
			return tea.ClearScreen
		}
		return nil
	}
	return t.drawSixelSoon()
}

// Resized implements tabs.GraphicsAware.
func (t *Tab) Resized() tea.Cmd { return t.syncPreview() }

// SetVisible implements tabs.VisibilityAware: previews load and draw only
// while the tab is on screen.
func (t *Tab) SetVisible(visible bool) tea.Cmd {
	t.pv.visible = visible
	return t.syncPreview()
}

// redrawPreview forgets the preview and builds it again (the palette's
// "redraw preview", for when a terminal mangled an image).
func (t *Tab) redrawPreview() tea.Cmd {
	drop := t.dropPreview()
	t.pv.key = previewKey{}
	return tea.Batch(drop, tea.ClearScreen, t.syncPreview())
}

// previewable reports whether a gets the preview layout.
func (t *Tab) previewable(a store.Artifact) bool {
	if !t.pv.visible || t.pv.caps.Off {
		return false
	}
	switch a.Type {
	case "png", "jpg", "gif", "svg", "webp", "html":
		return true
	}
	return false
}

// previewArea is where the preview goes in a w x h detail pane: below
// the header, above the key hints. ok is false when there's no room.
func (t *Tab) previewArea(a store.Artifact, w, h int) (header []string, top, rows int, ok bool) {
	header = t.compactHeader(a, w)
	top = len(header)
	rows = h - top - 2 // a blank line and the key hints
	return header, top, rows, rows >= minPreviewRows && w > 0
}

// wantPreview is the key for what should be on screen now.
func (t *Tab) wantPreview() previewKey {
	if t.dialog != nil {
		return previewKey{}
	}
	a, ok := t.selected()
	if !ok || !t.previewable(a) {
		return previewKey{}
	}
	w, h := t.split.RightSize()
	_, top, rows, ok := t.previewArea(a, w, h)
	if !ok {
		return previewKey{}
	}
	return previewKey{
		path: a.Path, mod: a.ModifiedAt, size: a.Size, typ: a.Type,
		top: top, cols: w, rows: rows, caps: t.pv.caps,
	}
}

// syncPreview starts a new preview when what should be shown changed. It
// runs after every message the tab handles.
func (t *Tab) syncPreview() tea.Cmd {
	p := &t.pv
	want := t.wantPreview()
	if want == p.key {
		return nil
	}
	drop := t.dropPreview()
	p.key = want
	p.gen++
	if want.empty() {
		return drop
	}
	gen := p.gen
	return tea.Batch(drop, tea.Tick(previewDebounce, func(time.Time) tea.Msg { return previewTickMsg{gen: gen} }))
}

// dropPreview removes what is on screen: a sixel is cleared, a kitty
// image freed.
func (t *Tab) dropPreview() tea.Cmd {
	p := &t.pv
	var cmds []tea.Cmd
	if p.sixelOn {
		p.sixelOn = false
		cmds = append(cmds, tea.ClearScreen)
	}
	if p.kittyID != 0 {
		cmds = append(cmds, tea.Raw(p.kittyDel))
		p.kittyID, p.kittyDel = 0, ""
	}
	p.res, p.sixel = nil, ""
	return tea.Batch(cmds...)
}

// loadPreview builds the preview for key off the update loop, including
// the escape sequences, so Update only sends them.
func (t *Tab) loadPreview(gen int, k previewKey) tea.Cmd {
	p := &t.pv
	id := 0
	if k.caps.Protocol == termcaps.Kitty && k.typ != "html" && k.typ != "webp" {
		if p.nextID < firstKittyID || p.nextID > lastKittyID {
			p.nextID = firstKittyID
		}
		id = p.nextID
		p.nextID++
	}
	rows := k.rows
	if k.caps.Protocol == termcaps.Sixel {
		rows = max(rows-1, 1) // sixel heights round up to 6-pixel bands
	}
	opts := preview.Options{
		Box:      preview.Box{Cols: k.cols, Rows: rows, CellW: k.caps.CellW, CellH: k.caps.CellH},
		Graphics: k.caps.Graphics(),
		Chafa:    k.caps.Chafa,
		Reason:   k.caps.Reason,
	}
	deps := preview.Deps{Chafa: t.chafa, Rsvg: t.rsvg}
	x, y := t.sixelOrigin(k)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
		defer cancel()
		res := preview.Load(ctx, k.path, k.typ, opts, deps)
		msg := previewLoadedMsg{gen: gen, res: res}
		if res.Kind != preview.Image {
			return msg
		}
		switch k.caps.Protocol {
		case termcaps.Kitty:
			seq, err := preview.KittyTransmit(res.Image, id, res.Cols, res.Rows, k.caps.CellW, k.caps.CellH, k.caps.Wrap)
			if err != nil {
				msg.res = preview.Result{Width: res.Width, Height: res.Height, Note: "can't encode: " + err.Error()}
				return msg
			}
			msg.seq, msg.id, msg.del = seq, id, k.caps.Wrap(preview.KittyDelete(id))
		case termcaps.Sixel:
			msg.seq = k.caps.Wrap(preview.Place(preview.Sixel(res.Image), x, y))
		}
		msg.res.Image = nil // encoded; the pixels aren't needed any more
		return msg
	}
}

// sixelOrigin is the terminal cell where the preview area starts: the tmux
// pane's offset, the tab bar, the detail pane and the header.
func (t *Tab) sixelOrigin(k previewKey) (x, y int) {
	ox, oy, _ := t.split.RightOrigin()
	return k.caps.OffsetX + ox, k.caps.OffsetY + ui.TabBarHeight + oy + k.top
}

// updatePreview handles the preview's messages.
func (t *Tab) updatePreview(msg tea.Msg) (tea.Cmd, bool) {
	p := &t.pv
	switch msg := msg.(type) {
	case previewTickMsg:
		if msg.gen != p.gen || p.key.empty() {
			return nil, true
		}
		return t.loadPreview(msg.gen, p.key), true
	case previewLoadedMsg:
		if msg.gen != p.gen {
			return nil, true
		}
		res := msg.res
		p.res = &res
		switch {
		case msg.id != 0:
			p.kittyID, p.kittyDel = msg.id, msg.del
			return tea.Raw(msg.seq), true
		case msg.seq != "":
			p.sixel = msg.seq
			return t.drawSixelSoon(), true
		}
		return nil, true
	case sixelDrawMsg:
		if msg.gen != p.gen || p.sixel == "" || p.sixelOn || p.occluded || !p.visible || t.dialog != nil {
			return nil, true
		}
		p.sixelOn = true
		return tea.Raw(p.sixel), true
	}
	return nil, false
}

// drawSixelSoon schedules drawing the loaded sixel, if there is one.
func (t *Tab) drawSixelSoon() tea.Cmd {
	p := &t.pv
	if p.sixel == "" || p.sixelOn {
		return nil
	}
	gen := p.gen
	return tea.Tick(sixelDelay, func(time.Time) tea.Msg { return sixelDrawMsg{gen: gen} })
}

// --- view ------------------------------------------------------------------------

// compactHeader is the detail pane's header above a preview: the name,
// the folder, one summary line, tags and the sync line.
func (t *Tab) compactHeader(a store.Artifact, w int) []string {
	th := t.theme
	heading := th.ArtifactIcon.Render(icon(a.Type)) + " "
	if a.Pinned {
		heading = th.ArtifactPin.Render("★ ") + heading
	}
	headW := max(w-ansi.StringWidth(ansi.Strip(heading)), 1)
	lines := []string{heading + th.DetailTitle.Render(ansi.Truncate(displayName(a), headW, "…"))}
	if a.Title != "" {
		lines = append(lines, th.Body.Render(ansi.Truncate(filepath.Base(a.Path), w, "…")))
	}
	lines = append(lines, th.Hint.Render(truncateLeft(filepath.Dir(a.Path), w)))

	parts := []string{a.Type, ui.HumanBytes(a.Size)}
	if r := t.pv.res; r != nil && r.Width > 0 && t.pv.key.path == a.Path {
		parts = append(parts, fmt.Sprintf("%d×%d", r.Width, r.Height))
	}
	parts = append(parts, ago(t.now().Sub(a.ModifiedAt)), t.source(a))
	lines = append(lines, th.Body.Render(ansi.Truncate(strings.Join(parts, " · "), w, "…")))
	if len(a.Tags) > 0 {
		lines = append(lines, th.ArtifactTag.Render(ansi.Truncate(hashTags(a.Tags), w, "…")))
	}
	if l := a.Link; l != nil {
		from := l.SessionName
		if from == "" {
			from = filepath.Dir(l.SourcePath)
		}
		s := "Copied from " + from + " · final (" + l.DetachReason + ")"
		if l.Active() {
			s = "Synced from " + from + " · " + ago(t.now().Sub(l.SyncedAt))
		}
		lines = append(lines, th.Hint.Render(ansi.Truncate(s, w, "…")))
	}
	return append(lines, "")
}

// previewDetail renders the detail pane with a preview, or false when a
// doesn't get one (the full card is shown instead).
func (t *Tab) previewDetail(a store.Artifact, w, h int) (string, bool) {
	if !t.previewable(a) {
		return "", false
	}
	header, _, rows, ok := t.previewArea(a, w, h)
	if !ok {
		return "", false
	}
	area := t.previewBody(a, w, rows)
	header = append(header, ui.Box(area, w, rows), "", t.detailHints(a, w))
	return strings.Join(header, "\n"), true
}

// previewBody fills the preview area.
func (t *Tab) previewBody(a store.Artifact, w, rows int) string {
	th := t.theme
	p := &t.pv
	r := p.res
	if r == nil || p.key.path != a.Path {
		return th.Hint.Render("Loading preview…")
	}
	switch r.Kind {
	case preview.Image:
		if p.kittyID != 0 {
			return preview.Placeholders(p.kittyID, r.Cols, r.Rows)
		}
		return "" // sixel: drawn over the blank area
	case preview.Art:
		return r.Art
	case preview.HTML:
		return t.htmlBody(r, w, rows)
	}
	return t.cardBody(a, r, w)
}

// htmlBody is the page title, its first lines of text and how to open it.
func (t *Tab) htmlBody(r *preview.Result, w, rows int) string {
	th := t.theme
	var out []string
	if r.Title != "" {
		out = append(out, th.DetailTitle.Render(ansi.Truncate(r.Title, w, "…")))
	}
	open := th.Hint.Render("press " + t.keys.Artifacts.Open.Help().Key + " to open")
	room := rows - len(out) - 2 // a blank line and the open hint
	for _, l := range r.Lines {
		for _, wl := range strings.Split(ansi.Wrap(l, w, " "), "\n") {
			if room <= 0 {
				break
			}
			out = append(out, th.Body.Render(ansi.Truncate(wl, w, "…")))
			room--
		}
	}
	if len(r.Lines) == 0 && r.Title == "" {
		out = append(out, th.Hint.Render("No visible text."))
	}
	return strings.Join(append(out, "", open), "\n")
}

// cardBody is the metadata shown in place of a picture, with the reason.
func (t *Tab) cardBody(a store.Artifact, r *preview.Result, w int) string {
	th := t.theme
	field := func(label, value string) string {
		return th.DetailLabel.Render(padRight(label, 9)) + th.Body.Render(ansi.Truncate(value, max(w-9, 1), "…"))
	}
	project := a.Project
	if project == "" {
		project = "—"
	}
	lines := []string{
		field("Project", project),
		field("Modified", a.ModifiedAt.Format("2006-01-02 15:04")),
		field("Created", a.CreatedAt.Format("2006-01-02 15:04")),
	}
	if r.Width > 0 {
		lines = append(lines, field("Pixels", fmt.Sprintf("%d × %d", r.Width, r.Height)))
	}
	if r.Note != "" {
		lines = append(lines, "", th.Hint.Render(ansi.Wrap(r.Note, w, " ")))
	}
	return strings.Join(lines, "\n")
}
