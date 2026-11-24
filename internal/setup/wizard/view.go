package wizard

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/setup"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
)

// View implements tea.Model.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.BackgroundColor = m.theme.Bg
	return v
}

func (m *Model) render() string {
	if m.w == 0 || m.h == 0 {
		return ""
	}
	if m.splash != nil {
		return m.splash.View(m.eng.Frame(), m.w, m.h, m.theme)
	}
	if len(m.steps) == 0 {
		return m.theme.Body.Render("no setup steps")
	}
	th := m.theme
	title := th.Wordmark.Render("forge") + th.HelpTitle.Render("setup")
	pos := fmt.Sprintf("step %d of %d", m.cur+1, len(m.steps))
	if m.summary {
		pos = "summary"
	}
	head := ui.Box(title+"  "+th.Hint.Render(pos), m.w, 1)

	rw, rh := m.split.RightSize()
	lw, lh := m.split.LeftSize()
	var right []string
	if m.summary {
		right = m.summaryView(rw)
	} else {
		right = m.stepView(rw, rh)
	}
	body := m.split.View(strings.Join(m.listView(lw, lh), "\n"), strings.Join(right, "\n"), m.focusList)
	return lipgloss.JoinVertical(lipgloss.Left, head, body, ui.Box(m.helpView(), m.w, 2))
}

// glyph is a step's status mark in the list.
func (m *Model) glyph(id setup.StepID) string {
	th := m.theme
	r, ok := m.results[id]
	if !ok {
		return th.Hint.Render("·")
	}
	switch r.Status {
	case setup.Done:
		return th.DoctorOK.Render("✓")
	case setup.Skipped:
		return th.DoctorWarn.Render("↷")
	case setup.Failed:
		return th.DoctorFail.Render("✗")
	}
	return th.Hint.Render("·")
}

func (m *Model) listView(w, _ int) []string {
	th := m.theme
	lines := []string{th.DetailLabel.Render("Steps"), ""}
	for i, s := range m.steps {
		marker := "  "
		if i == m.cur && !m.summary {
			marker = th.DoctorWarn.Render("▸ ")
		}
		name := s.Title
		row := marker + m.glyph(s.ID) + " " + name
		if m.focusList && i == m.listSel {
			row = th.TableSelected.Render(ansi.Truncate(ansi.Strip(marker)+ansi.Strip(m.glyph(s.ID))+" "+name, w, "…"))
		}
		lines = append(lines, ansi.Truncate(row, w, "…"))
	}
	lines = append(lines, "", th.Hint.Render("✓ done  ↷ skipped  ✗ failed"))
	return lines
}

func (m *Model) stepView(w, h int) []string {
	th := m.theme
	s, sm := m.current()
	lines := []string{th.DetailTitle.Render(s.Title), th.Hint.Render(wrapText(s.Blurb, w)), ""}
	body := sm.view(w, h-len(lines)-2)
	lines = append(lines, body...)
	if r, ok := m.results[s.ID]; ok && !sm.grabs() {
		lines = append(lines, "", m.resultLine(r, w))
	}
	if m.flash != "" {
		lines = append(lines, "", th.DoctorWarn.Render(wrapText(m.flash, w)))
	}
	return lines
}

func (m *Model) resultLine(r setup.Result, w int) string {
	th := m.theme
	switch r.Status {
	case setup.Done:
		return th.DoctorOK.Render(wrapText("✓ "+r.Detail, w))
	case setup.Skipped:
		return th.DoctorWarn.Render("↷ skipped")
	case setup.Failed:
		out := th.DoctorFail.Render(wrapText("✗ "+r.Detail, w))
		if r.Fix != "" {
			out += "\n" + th.Hint.Render(wrapText("  fix: "+r.Fix, w))
		}
		return out
	}
	return ""
}

func (m *Model) summaryView(w int) []string {
	th := m.theme
	var only []setup.StepID
	if m.d.Only != "" {
		only = []setup.StepID{m.d.Only}
	}
	lines := setup.Summary(m.results, only)
	out := []string{th.DetailTitle.Render("Summary"), ""}
	if len(lines) == 0 {
		out = append(out, th.DoctorOK.Render("Everything is set up. ")+th.Body.Render("The forge is hot."), "")
	} else {
		out = append(out, th.Body.Render("These steps need another look:"), "")
		for _, l := range lines {
			status := "not run"
			if _, ok := m.results[l.Step.ID]; ok {
				status = l.Result.Status.String()
			}
			out = append(out, m.glyph(l.Step.ID)+" "+th.DetailLabel.Render(l.Step.Title)+th.Hint.Render(" ("+status+")"))
			if l.Result.Detail != "" && l.Result.Detail != "skipped" {
				out = append(out, th.Body.Render(wrapText("  "+l.Result.Detail, w)))
			}
			out = append(out, th.Hint.Render(wrapText("  → "+l.Fix, w)), "")
		}
	}
	if m.d.Only == "" {
		out = append(out, th.Hint.Render("Run `forge setup <step>` any time to redo one step, or `forge setup` for all."))
	}
	return out
}

func (m *Model) helpView() string {
	k := m.keys.Setup
	var bs []key.Binding
	switch {
	case m.summary:
		bs = []key.Binding{key.NewBinding(key.WithKeys(k.Next.Keys()...), key.WithHelp(k.Next.Help().Key, "finish")), k.Focus}
	case m.focusList:
		bs = []key.Binding{k.Up, k.Down, key.NewBinding(key.WithKeys(k.Next.Keys()...), key.WithHelp(k.Next.Help().Key, "open step")), k.Focus, k.Quit}
	default:
		_, sm := m.current()
		if sm.grabs() {
			return m.theme.Hint.Render("the step has the keyboard (esc leaves it)")
		}
		bs = append(sm.help(), k.Next, k.Skip, k.Focus, k.Quit)
	}
	return ui.HelpLine(m.theme, bs)
}

// spinner is the hammer, on the wizard's clock.
func (m *Model) spinner(label string) string {
	return anim.HammerFrame(m.eng.Frame(), m.eng.Settings(), m.theme) + " " + m.theme.Body.Render(label)
}

func splitLines(s string) []string { return strings.Split(strings.TrimRight(s, "\n"), "\n") }

// wrapText wraps s to w cells (at least 10).
func wrapText(s string, w int) string { return ansi.Wordwrap(s, max(w, 10), "") }

// code renders a line to copy, indented.
func (m *Model) code(s string) string { return "  " + m.theme.PreviewText.Render(s) }
