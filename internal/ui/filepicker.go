package ui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/sahilm/fuzzy"
)

// FilePicker is a fuzzy multi-select over a list of paths (P5.2's pack
// file picker). Typing filters, Next / Prev move, FilePicker.Toggle marks
// the file under the cursor, ToggleAll the visible matches, Accept
// finishes and Cancel abandons. The owner reads Done / Aborted and
// Selected after each key.
type FilePicker struct {
	theme *Theme
	keys  *KeyMap
	title string
	input Filter

	paths   []string
	marked  map[string]bool
	order   []string // marked paths in the order they were marked
	matches []fuzzy.Match
	cursor  int
	offset  int

	done, aborted bool
}

// NewFilePicker returns a picker over paths with preselected marked.
// Preselected paths not in paths are kept (and returned) as they are.
func NewFilePicker(theme *Theme, keys *KeyMap, title string, paths, preselected []string) *FilePicker {
	p := &FilePicker{theme: theme, keys: keys, title: title, input: NewFilter(theme, keys), paths: paths, marked: map[string]bool{}}
	p.input.Start()
	for _, s := range preselected {
		p.mark(s, true)
	}
	p.refilter()
	return p
}

// Done reports whether the user accepted the selection.
func (p *FilePicker) Done() bool { return p.done }

// Aborted reports whether the user cancelled.
func (p *FilePicker) Aborted() bool { return p.aborted }

// Selected returns the marked paths in the order they were marked.
func (p *FilePicker) Selected() []string { return slices.Clone(p.order) }

// Query returns the filter text.
func (p *FilePicker) Query() string { return p.input.Value() }

// Visible returns the paths matching the query, best first.
func (p *FilePicker) Visible() []string {
	out := make([]string, len(p.matches))
	for i, m := range p.matches {
		out[i] = m.Str
	}
	return out
}

func (p *FilePicker) mark(path string, on bool) {
	if on == p.marked[path] {
		return
	}
	if on {
		p.marked[path] = true
		p.order = append(p.order, path)
		return
	}
	delete(p.marked, path)
	p.order = slices.DeleteFunc(p.order, func(s string) bool { return s == path })
}

// refilter ranks the paths against the query; an empty query keeps them
// all in their order.
func (p *FilePicker) refilter() {
	q := strings.TrimSpace(p.input.Value())
	if q == "" {
		p.matches = make([]fuzzy.Match, len(p.paths))
		for i, s := range p.paths {
			p.matches[i] = fuzzy.Match{Str: s, Index: i}
		}
	} else {
		p.matches = fuzzy.Find(q, p.paths)
	}
	p.cursor, p.offset = 0, 0
}

// Update handles one key press.
func (p *FilePicker) Update(msg tea.KeyPressMsg) {
	in, fk := p.keys.Input, p.keys.FilePicker
	switch {
	case key.Matches(msg, in.Cancel):
		p.aborted = true
	case key.Matches(msg, in.Accept):
		p.done = true
	case key.Matches(msg, in.Next):
		if n := len(p.matches); n > 0 {
			p.cursor = (p.cursor + 1) % n
		}
	case key.Matches(msg, in.Prev):
		if n := len(p.matches); n > 0 {
			p.cursor = (p.cursor - 1 + n) % n
		}
	case key.Matches(msg, fk.Toggle):
		if len(p.matches) > 0 {
			s := p.matches[p.cursor].Str
			p.mark(s, !p.marked[s])
			p.cursor = min(p.cursor+1, len(p.matches)-1)
		}
	case key.Matches(msg, fk.ToggleAll):
		all := true
		for _, m := range p.matches {
			all = all && p.marked[m.Str]
		}
		for _, m := range p.matches {
			p.mark(m.Str, !all)
		}
	default:
		if p.input.Update(msg) {
			p.refilter()
		}
	}
}

// Help returns the picker's keys for the status bar.
func (p *FilePicker) Help() []key.Binding {
	in, fk := p.keys.Input, p.keys.FilePicker
	return []key.Binding{fk.Toggle, fk.ToggleAll, in.Next, in.Accept, in.Cancel}
}

// filePickerChrome is the lines above the list: title, prompt, rule.
const filePickerChrome = 3

// View renders the picker at exactly width x height.
func (p *FilePicker) View(width, height int) string {
	t := p.theme
	if width < 4 || height < filePickerChrome+1 {
		return ""
	}
	count := fmt.Sprintf("%d selected · %d/%d", len(p.order), len(p.matches), len(p.paths))
	lines := []string{
		fit(t.DetailTitle.Render(p.title)+"  "+t.Hint.Render(count), width),
		fit(t.PalettePrompt.Render("› ")+t.FilterText.Render(p.input.Value()+"█"), width),
		t.TabRule.Render(strings.Repeat("─", width)),
	}
	bodyH := height - filePickerChrome
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+bodyH {
		p.offset = p.cursor - bodyH + 1
	}
	if len(p.matches) == 0 {
		empty := "no files"
		if p.input.Value() != "" {
			empty = fmt.Sprintf("no matches for %q", p.input.Value())
		}
		lines = append(lines, t.TableEmpty.Render(fit(empty, width)))
	}
	for i := p.offset; i < len(p.matches) && i < p.offset+bodyH; i++ {
		lines = append(lines, p.row(p.matches[i], width, i == p.cursor))
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

// row renders "● path" with the matched characters highlighted.
func (p *FilePicker) row(m fuzzy.Match, width int, selected bool) string {
	t := p.theme
	base := t.TableRow
	if selected {
		base = t.TableSelected
	}
	box := "○ "
	if p.marked[m.Str] {
		box = t.DocMarked.Render("● ")
	}
	var b strings.Builder
	hit := map[int]bool{}
	for _, i := range m.MatchedIndexes {
		hit[i] = true
	}
	for i, r := range m.Str {
		if hit[i] {
			b.WriteString(t.SearchMatch.Render(string(r)))
		} else {
			b.WriteString(base.Render(string(r)))
		}
	}
	return base.Render(fit(box+b.String(), width))
}
