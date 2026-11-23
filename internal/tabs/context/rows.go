package context

import (
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/memory"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
)

// row is one line of the list: a group heading or a file.
type row struct {
	head string
	file int // index into Tab.files; -1 for a heading
}

// autoMemoryLabel stands in for MEMORY.md's long path (the detail header
// shows it in full).
const autoMemoryLabel = "MEMORY.md (auto memory)"

// footerLines is the blank line and the total under the list.
const footerLines = 2

// rebuild groups the files under "Loaded at start" and "On demand",
// keeping the selection.
func (t *Tab) rebuild() {
	t.rows = t.rows[:0]
	var startup, later []int
	for i, f := range t.files {
		if f.OnDemand {
			later = append(later, i)
		} else {
			startup = append(startup, i)
		}
	}
	if len(startup) > 0 {
		t.rows = append(t.rows, row{head: "Loaded at start", file: -1})
		for _, i := range startup {
			t.rows = append(t.rows, row{file: i})
		}
	}
	if len(later) > 0 {
		t.rows = append(t.rows, row{head: "On demand (when Claude touches matching files)", file: -1})
		for _, i := range later {
			t.rows = append(t.rows, row{file: i})
		}
	}
	idx := -1
	for i, r := range t.rows {
		if r.file >= 0 && t.files[r.file].Path == t.selPath {
			idx = i
			break
		}
	}
	if idx < 0 {
		idx = t.cursor
	}
	t.setCursor(idx, 1)
}

// selected returns the selected file.
func (t *Tab) selected() (memory.File, bool) {
	if t.cursor < 0 || t.cursor >= len(t.rows) || t.rows[t.cursor].file < 0 {
		return memory.File{}, false
	}
	return t.files[t.rows[t.cursor].file], true
}

func (t *Tab) move(delta int) {
	dir := 1
	if delta < 0 {
		dir = -1
	}
	t.setCursor(t.cursor+delta, dir)
}

// setCursor selects row i, stepping in dir past headings (and back the
// other way at the ends).
func (t *Tab) setCursor(i, dir int) {
	if len(t.rows) == 0 {
		t.cursor, t.top, t.selPath = 0, 0, ""
		return
	}
	i = min(max(i, 0), len(t.rows)-1)
	for _, d := range []int{dir, -dir} {
		for j := i; j >= 0 && j < len(t.rows); j += d {
			if t.rows[j].file >= 0 {
				t.cursor = j
				t.selPath = t.files[t.rows[j].file].Path
				t.clampTop()
				return
			}
		}
	}
	t.cursor, t.selPath = i, ""
	t.clampTop()
}

func (t *Tab) listHeight() int {
	_, h := t.split.LeftSize()
	return max(h-footerLines, 1)
}

// clampTop scrolls the list so the cursor is on screen, and the heading
// above the first file with it.
func (t *Tab) clampTop() {
	h := t.listHeight()
	if t.cursor < t.top {
		t.top = t.cursor
	}
	if t.cursor >= t.top+h {
		t.top = t.cursor - h + 1
	}
	if t.top == 1 && len(t.rows) > 0 && t.rows[0].file < 0 && t.cursor < h {
		t.top = 0
	}
	t.top = max(min(t.top, len(t.rows)-h), 0)
}

// projectName names the active project, or "all projects".
func (t *Tab) projectName() string {
	if t.project == nil {
		return "all projects"
	}
	return t.project.Name
}

// display shortens a path: relative to the checkout, ~ for home.
func (t *Tab) display(path string) string {
	if root := t.root(); root != "" {
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	if t.home != "" {
		if rel, err := filepath.Rel(t.home, path); err == nil && !strings.HasPrefix(rel, "..") {
			return "~/" + rel
		}
	}
	return path
}

// listView renders the list and the total at w x h.
func (t *Tab) listView(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	th := t.theme
	if !t.scanned {
		return t.loadingBody("reading memory files for "+t.projectName()+"…", w, h)
	}
	if len(t.rows) == 0 {
		return anim.Notice("No memory files for "+t.projectName(),
			"CLAUDE.md, CLAUDE.local.md and ~/.claude/CLAUDE.md show up here", th, w, h)
	}
	body := max(h-footerLines, 1)
	var lines []string
	end := min(t.top+body, len(t.rows))
	for i := t.top; i < end; i++ {
		lines = append(lines, t.rowView(t.rows[i], i == t.cursor, w))
	}
	for len(lines) < body {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n") + "\n\n" + t.totalView(w)
}

// totalView is the footer: "Total ≈ 6.2k / 8k tokens · 7 files", amber
// over budget.
func (t *Tab) totalView(w int) string {
	th := t.theme
	n := 0
	for _, f := range t.files {
		if f.Counts() {
			n++
		}
	}
	total := "Total ≈ " + ui.CompactCount(memory.Total(t.files))
	if b := t.budget(); b > 0 {
		total += " / " + ui.CompactCount(b)
	}
	total += " tokens"
	rest := " · " + fileCount(n)
	if t.overBudget() {
		return th.ContextOverBudget.Render(ansi.Truncate("⚠ "+total+" (over budget)", w, "…")) +
			th.Hint.Render(ansi.Truncate(rest, max(w-ansi.StringWidth("⚠ "+total+" (over budget)"), 0), ""))
	}
	return th.DetailTitle.Render(ansi.Truncate(total, w, "…")) +
		th.Hint.Render(ansi.Truncate(rest, max(w-ansi.StringWidth(total), 0), ""))
}

func fileCount(n int) string {
	if n == 1 {
		return "1 file"
	}
	return ui.CompactCount(n) + " files"
}

// rightText is the right column of a file row: its tokens or its state.
func rightText(f memory.File) string {
	switch {
	case !f.Exists && f.Source == memory.Import:
		return "missing"
	case !f.Exists:
		return "+ create"
	case f.TooBig:
		return ">4MiB skip"
	}
	s := ui.CompactCount(f.Tokens)
	if f.Truncated {
		s = "✂ " + s
	}
	return s
}

func (t *Tab) rowView(r row, selected bool, w int) string {
	th := t.theme
	if r.file < 0 {
		return th.DetailLabel.Render(ansi.Truncate(r.head, w, "…"))
	}
	f := t.files[r.file]
	label := "  " + strings.Repeat("  ", f.Depth)
	if f.Depth > 0 {
		label += "↳ "
	}
	if f.Source == memory.AutoMemory {
		label += autoMemoryLabel
	} else {
		label += t.display(f.Path)
	}
	if f.External {
		label += " ⚠"
	}
	right := rightText(f)
	space := w - ansi.StringWidth(right) - 1
	if space < 4 {
		right, space = "", w
	}
	label = ansi.Truncate(label, space, "…")
	pad := max(w-ansi.StringWidth(label)-ansi.StringWidth(right), 0)
	if selected {
		return th.TableSelected.Render(label + strings.Repeat(" ", pad) + right)
	}
	style, rstyle := th.TableRow, th.Hint
	switch {
	case !f.Exists && f.Source == memory.Import:
		style, rstyle = th.Hint, th.TableEmpty
	case !f.Exists || f.OnDemand:
		style = th.Hint
	case f.TooBig:
		rstyle = th.TableEmpty
	}
	return style.Render(label+strings.Repeat(" ", pad)) + rstyle.Render(right)
}
