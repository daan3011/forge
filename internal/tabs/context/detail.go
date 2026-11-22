package context

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	glamouransi "charm.land/glamour/v2/ansi"
	"github.com/charmbracelet/x/ansi"

	docfs "github.com/daan3011/forge/internal/docs"
	"github.com/daan3011/forge/internal/memory"
	"github.com/daan3011/forge/internal/ui"
)

// The detail pane shows the selected file rendered with Glamour at the
// pane's width. Rendering is slow, so it runs in a tea.Cmd and is cached by
// path, content hash and width: an edit or a resize renders again.

// detailHeader is the title line, the info line and a blank line.
const detailHeader = 3

// renderCacheCap is how many rendered files stay in memory.
const renderCacheCap = 32

type rendered struct {
	lines []string
	err   error
}

type detailState struct {
	offset    int
	path      string // file the offset belongs to
	cache     map[string]rendered
	order     []string
	rendering string // key in flight
}

func newDetailState() detailState { return detailState{cache: map[string]rendered{}} }

func (d *detailState) remember(key string, r rendered) {
	if _, ok := d.cache[key]; !ok {
		d.order = append(d.order, key)
	}
	d.cache[key] = r
	for len(d.order) > renderCacheCap {
		delete(d.cache, d.order[0])
		d.order = d.order[1:]
	}
}

type renderedMsg struct {
	key   string
	lines []string
	err   error
}

// TargetTab implements tabs.TargetedMsg.
func (renderedMsg) TargetTab() string { return Title }

func renderKey(f memory.File, width int) string {
	return fmt.Sprintf("%s@%s@%d", f.Path, f.Hash, width)
}

// renderable reports whether f has content to render.
func renderable(f memory.File) bool { return f.Exists && !f.TooBig }

func (t *Tab) detailWidth() int {
	if t.split.Collapsed() {
		w, _ := t.split.LeftSize()
		return w
	}
	w, _ := t.split.RightSize()
	return w
}

func (t *Tab) detailBodyHeight() int {
	_, h := t.split.RightSize()
	if t.split.Collapsed() {
		_, h = t.split.LeftSize()
	}
	return max(h-detailHeader, 1)
}

// syncDetail resets the scroll for a new file and starts rendering the
// selected file if it isn't cached.
func (t *Tab) syncDetail() tea.Cmd {
	f, ok := t.selected()
	d := &t.det
	if !ok {
		t.focusDetail = false
		d.path, d.offset = "", 0
		return nil
	}
	if d.path != f.Path {
		d.path, d.offset = f.Path, 0
	}
	w := t.detailWidth()
	if w <= 0 || !renderable(f) || t.matrix {
		return nil
	}
	key := renderKey(f, w)
	if _, ok := d.cache[key]; ok || d.rendering == key {
		return nil
	}
	d.rendering = key
	path, auto, style := f.Path, f.Source == memory.AutoMemory, t.theme.GlamourStyle()
	return func() tea.Msg {
		b, err := os.ReadFile(path)
		if err != nil {
			return renderedMsg{key: key, err: err}
		}
		text := string(b)
		if auto {
			text, _ = memory.TruncateAutoMemory(text)
		}
		return renderedMsg{key: key, lines: renderDoc(style, w, text)}
	}
}

// renderDoc renders text, falling back to wrapped plain text when Glamour
// fails, and returns its lines (none for a blank file).
func renderDoc(style glamouransi.StyleConfig, w int, text string) []string {
	if meta, body := docfs.SplitFrontMatter([]byte(text)); meta != nil {
		// Show a rule's frontmatter as one emphasised line.
		text = string(body)
		if line := docfs.FormatMeta(meta); line != "" {
			text = "*" + line + "*\n\n" + text
		}
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	out := ui.RenderMarkdown(style, w, []string{text})[0]
	if out == "" {
		out = ansi.Wrap(text, w, "")
	}
	return strings.Split(out, "\n")
}

func (t *Tab) rendered(msg renderedMsg) {
	if t.det.rendering == msg.key {
		t.det.rendering = ""
	}
	t.det.remember(msg.key, rendered{lines: msg.lines, err: msg.err})
}

// current returns the rendered selected file, if it is ready.
func (t *Tab) current() (rendered, bool) {
	f, ok := t.selected()
	if !ok || !renderable(f) {
		return rendered{}, false
	}
	got, ok := t.det.cache[renderKey(f, t.detailWidth())]
	return got, ok
}

// bodyLines is what the detail pane scrolls: the rendered file plus the
// truncation note.
func (t *Tab) bodyLines(f memory.File, got rendered) []string {
	if !f.Truncated {
		return got.lines
	}
	note := t.theme.Hint.Render(fmt.Sprintf("── %d more lines aren't loaded at start (first %d lines / %dKB only) ──",
		max(f.Lines-memory.AutoMemoryLines, 0), memory.AutoMemoryLines, memory.AutoMemoryBytes>>10))
	return append(append([]string{}, got.lines...), "", note)
}

func (t *Tab) scrollDetail(delta int) {
	f, ok := t.selected()
	got, ready := t.current()
	if !ok || !ready {
		return
	}
	maxOff := max(len(t.bodyLines(f, got))-t.detailBodyHeight(), 0)
	t.det.offset = min(max(t.det.offset+delta, 0), maxOff)
}

// info is the line under the title: source, size and flags.
func (t *Tab) info(f memory.File) string {
	parts := []string{f.Source.String()}
	if f.Exists && !f.TooBig {
		parts = append(parts, fmt.Sprintf("≈%s tokens", ui.CompactCount(f.Tokens)), lines(f.Lines))
	}
	if f.Parent != "" {
		parts = append(parts, "imported by "+t.display(f.Parent))
	}
	if len(f.Globs) > 0 {
		parts = append(parts, "paths: "+strings.Join(f.Globs, ", "))
	}
	if f.External {
		parts = append(parts, "⚠ outside the project: Claude asks before loading it")
	}
	if f.OnDemand {
		parts = append(parts, "not counted at start")
	}
	return strings.Join(parts, " · ")
}

func lines(n int) string {
	if n == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", n)
}

// detailView renders the detail pane at w x h.
func (t *Tab) detailView(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	th := t.theme
	f, ok := t.selected()
	if !ok {
		if t.scanned {
			return th.Hint.Render("No memory files")
		}
		return ""
	}
	header := th.DetailTitle.Render(ansi.Truncate(t.display(f.Path), w, "…")) + "\n" +
		th.Hint.Render(ansi.Truncate(t.info(f), w, "…")) + "\n"
	edit := t.keys.Context.Edit.Help().Key
	switch {
	case !f.Exists && f.Source == memory.Import:
		return header + "\n" + th.TableEmpty.Render(ansi.Wrap("Imported but not found: "+f.Path, w, "/")) +
			"\n\n" + th.Hint.Render(edit+"  create it")
	case !f.Exists:
		return header + "\n" + th.Hint.Render(ansi.Wrap("Doesn't exist yet. "+edit+" to create it in $EDITOR.", w, ""))
	case f.TooBig:
		return header + "\n" + th.TableEmpty.Render(ansi.Wrap("Over 4 MiB: Claude Code skips this file entirely.", w, ""))
	}
	got, ok := t.current()
	switch {
	case !ok:
		return header + "\n" + th.Hint.Render("Rendering…")
	case got.err != nil:
		return header + "\n" + th.TableEmpty.Render(ansi.Wrap(got.err.Error(), w, ""))
	case len(got.lines) == 0:
		return header + "\n" + th.Hint.Render("(empty — "+edit+" to write it)")
	}
	all := t.bodyLines(f, got)
	body := max(h-detailHeader, 1)
	off := min(t.det.offset, max(len(all)-body, 0))
	end := min(off+body, len(all))
	return header + "\n" + strings.Join(all[off:end], "\n")
}
