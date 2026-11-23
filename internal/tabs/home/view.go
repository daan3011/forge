package home

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
)

const (
	// maxAgents and maxPins cap their side panel sections, so today's
	// note keeps some room.
	maxAgents = 8
	maxPins   = 5
)

// View implements tabs.Tab.
func (t *Tab) View() string {
	if t.dialog != nil {
		dw, dh := t.dialogSize()
		return t.theme.PaneFocused.Render(ui.Box(t.dialog.View(), dw, dh))
	}
	w, _ := t.split.LeftSize()
	left := t.table.View()
	for _, p := range t.problems() {
		left += "\n" + t.theme.Hint.Render(ansi.Truncate(" "+p, w, "…"))
	}
	return t.split.View(left, t.side(), true)
}

// emptyView is the list body without rows: loading, or the cold forge.
func (t *Tab) emptyView(w, h int) string {
	if !t.settled() {
		return t.theme.Hint.Render(" heating up…")
	}
	if len(t.sources()) == 0 {
		return anim.EmptyForgeCaption("nothing to watch yet · configure GitHub, Plane or start an agent", w, h, t.theme)
	}
	if len(t.problems()) > 0 {
		return anim.EmptyForgeCaption(partCaption, w, h, t.theme)
	}
	return anim.EmptyForgeCaption(coldCaption, w, h, t.theme)
}

// running are the sessions on the side panel: every one that isn't dead.
func (t *Tab) running() []sessions.Row {
	var out []sessions.Row
	for _, r := range t.rows {
		if r.Status != store.StatusDead && r.Status != store.StatusArchived {
			out = append(out, r)
		}
	}
	return out
}

// side is the side panel: running agents, pinned artifacts, today's note.
func (t *Tab) side() string {
	w, h := t.split.RightSize()
	th := t.theme
	var lines []string
	heading := func(icon, title string, n int) {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		s := th.DetailTitle.Render(icon + " " + title)
		if n > 0 {
			s += th.Hint.Render(fmt.Sprintf(" %d", n))
		}
		lines = append(lines, s)
	}
	more := func(n int) {
		if n > 0 {
			lines = append(lines, th.Hint.Render(fmt.Sprintf("  +%d more", n)))
		}
	}

	icon, _ := th.SourceIcon("agent")
	run := t.running()
	heading(icon, "Agents", len(run))
	switch {
	case t.svc == nil:
		lines = append(lines, th.Hint.Render("  sessions unavailable"))
	case len(run) == 0 && t.agents.loaded:
		lines = append(lines, th.Hint.Render("  no agents running"))
	}
	f, s := t.anim.Frame(), t.anim.Settings()
	for i, r := range run {
		if i == maxAgents {
			more(len(run) - maxAgents)
			break
		}
		lines = append(lines, "  "+anim.Flame(flameStatus(r.Status), f, s, th)+" "+r.Name+" "+
			th.SessionStatus(string(r.Status)).Render(string(r.Status))+th.Hint.Render(t.age(r.StatusChangedAt)))
		if r.Progress != "" {
			// The agent's latest report_progress line (P6.5).
			lines = append(lines, "    "+th.Hint.Render(r.Progress+t.age(r.ProgressAt)))
		}
	}

	icon, _ = th.SourceIcon("artifact")
	heading(icon, "Pinned", len(t.pins))
	switch {
	case t.pinsErr != nil:
		lines = append(lines, th.Hint.Render("  "+t.pinsErr.Error()))
	case len(t.pins) == 0:
		lines = append(lines, th.Hint.Render("  nothing pinned"))
	}
	for i, a := range t.pins {
		if i == maxPins {
			more(len(t.pins) - maxPins)
			break
		}
		title := a.Title
		if title == "" {
			title = filepath.Base(a.Path)
		}
		lines = append(lines, "  "+th.Body.Render(title))
	}

	icon, _ = th.SourceIcon("note")
	heading(icon, "Today", 0)
	if len(t.note) == 0 {
		lines = append(lines, th.Hint.Render("  no note for today"))
	}
	for _, l := range t.note {
		lines = append(lines, "  "+th.Body.Render(l))
	}

	if len(lines) > h {
		lines = lines[:max(h, 0)]
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, w, "…")
	}
	return strings.Join(lines, "\n")
}
