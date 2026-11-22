package context

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/memory"
	"github.com/daan3011/forge/internal/ui"
)

// The worktree view lines up the files each checkout of the repo loads at
// start: one column per worktree, one row per file.

// cellWidth is one worktree column.
const cellWidth = 3

var cellMarks = map[memory.Cell]string{
	memory.Absent:  "·",
	memory.Shared:  "●",
	memory.Same:    "=",
	memory.Differs: "≠",
}

// matrixView renders the inheritance matrix at w x h.
func (t *Tab) matrixView(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	th := t.theme
	out := []string{
		th.DetailTitle.Render(ansi.Truncate("Worktree inheritance", w, "…")),
		th.Hint.Render(ansi.Truncate("● same file  = own copy, same  ≠ own copy, differs  · not loaded", w, "…")),
		"",
	}
	switch {
	case !t.scanned:
		return strings.Join(out, "\n") + th.Hint.Render("Reading…")
	case len(t.trees) == 0:
		return strings.Join(out, "\n") + th.Hint.Render(ansi.Wrap("No checkout for "+t.projectName()+", so only your user files load.", w, ""))
	}

	n := len(t.trees)
	nameW := max(w-n*cellWidth, 8)
	head := strings.Repeat(" ", nameW)
	for i := range n {
		head += fmt.Sprintf("%*d", cellWidth, i+1)
	}
	out = append(out, th.DetailLabel.Render(ansi.Truncate(head, w, "")))

	legend := t.matrixLegend(w)
	room := max(h-len(out)-len(legend)-1, 1)
	sel, _ := t.selected()
	for i, r := range t.inherit {
		if i == room-1 && len(t.inherit) > room {
			out = append(out, th.Hint.Render(fmt.Sprintf("… %d more", len(t.inherit)-i)))
			break
		}
		name := r.Rel
		switch {
		case r.Source == memory.AutoMemory:
			name = autoMemoryLabel
		case name == "":
			name = t.display(r.Path)
		}
		line := ansi.Truncate(name, nameW-1, "…")
		line += strings.Repeat(" ", max(nameW-ansi.StringWidth(line), 0))
		for _, c := range r.Cells {
			line += fmt.Sprintf("%*s", cellWidth, cellMarks[c])
		}
		if r.Path == sel.Path {
			out = append(out, th.TableSelected.Render(ansi.Truncate(line, w, "")))
			continue
		}
		out = append(out, th.TableRow.Render(ansi.Truncate(line, w, "")))
	}
	out = append(out, "")
	return strings.Join(append(out, legend...), "\n")
}

// matrixLegend names each column with its startup total.
func (t *Tab) matrixLegend(w int) []string {
	th := t.theme
	var out []string
	if t.wtErr != nil {
		out = append(out, th.TableEmpty.Render(ansi.Truncate("Can't list worktrees: "+t.wtErr.Error(), w, "…")))
	} else if len(t.trees) == 1 {
		out = append(out, th.Hint.Render(ansi.Truncate("No other worktrees.", w, "…")))
	}
	for i, tr := range t.trees {
		total := memory.Total(tr.Files)
		tok := "≈" + ui.CompactCount(total)
		style := th.Hint
		if b := t.budget(); b > 0 && total > b {
			tok, style = "⚠ "+tok, th.ContextOverBudget
		}
		label := fmt.Sprintf("%d %s ", i+1, tr.Label)
		path := " " + t.display(tr.Root)
		if i == 0 {
			path = " " + tr.Root
		}
		line := th.TableRow.Render(ansi.Truncate(label, w, "…"))
		rest := max(w-ansi.StringWidth(label), 0)
		line += style.Render(ansi.Truncate(tok, rest, ""))
		rest = max(rest-ansi.StringWidth(tok), 0)
		line += th.Hint.Render(ansi.Truncate(path, rest, "…"))
		out = append(out, line)
	}
	return out
}
