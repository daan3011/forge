package wizard

import (
	"context"
	"errors"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/doctor"
	"github.com/daan3011/forge/internal/setup"
	"github.com/daan3011/forge/internal/ui/anim"
)

const checkTimeout = 20 * time.Second

// checksMsg carries doctor checks for a step.
type checksMsg struct {
	step   setup.StepID
	checks []doctor.Check
}

func (m *Model) runChecks(step setup.StepID, names []string) tea.Cmd {
	run := m.d.Doctor
	if run == nil {
		return func() tea.Msg { return checksMsg{step: step} }
	}
	return func() tea.Msg {
		ctx, cancel := opCtx(checkTimeout)
		defer cancel()
		return checksMsg{step: step, checks: run(ctx, names...)}
	}
}

// checkLines renders doctor checks with their fixes.
func (m *Model) checkLines(cs []doctor.Check, w int) []string {
	th := m.theme
	var out []string
	for _, c := range cs {
		mark := th.DoctorOK.Render("✓")
		switch c.Status {
		case doctor.Warn:
			mark = th.DoctorWarn.Render("!")
		case doctor.Fail:
			mark = th.DoctorFail.Render("✗")
		case doctor.Skip:
			mark = th.Hint.Render("–")
		}
		out = append(out, mark+" "+th.DetailLabel.Render(padRight(c.Name, 13))+th.Body.Render(wrapText(c.Detail, max(w-16, 10))))
		if c.Fix != "" && c.Status != doctor.OK {
			out = append(out, th.Hint.Render(wrapText("                fix: "+c.Fix, w)))
		}
	}
	return out
}

func padRight(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	return s
}

// --- 1. Dependencies -------------------------------------------------------

type depsStep struct {
	base
	busy   bool
	checks []doctor.Check
}

func (s *depsStep) enter() tea.Cmd {
	s.busy = true
	return s.m.runChecks(setup.StepDeps, setup.DepChecks)
}

func (s *depsStep) update(msg tea.Msg) tea.Cmd {
	if c, ok := msg.(checksMsg); ok && c.step == setup.StepDeps {
		s.busy, s.checks = false, c.checks
		return s.m.setResult(setup.StepDeps, setup.DepsResult(c.checks))
	}
	return nil
}

func (s *depsStep) key(msg tea.KeyPressMsg) tea.Cmd {
	if key.Matches(msg, s.m.keys.Setup.Rerun) && !s.busy {
		return s.enter()
	}
	return nil
}

func (s *depsStep) accept() (tea.Cmd, bool) { return nil, !s.busy }

func (s *depsStep) view(w, _ int) []string {
	if s.busy {
		return []string{s.m.spinner("checking tools…")}
	}
	return s.m.checkLines(s.checks, w)
}

func (s *depsStep) help() []key.Binding { return []key.Binding{s.m.keys.Setup.Rerun} }
func (s *depsStep) ticking() bool       { return s.busy && anim.Ticks(s.m.eng.Settings()) }

// --- 2. GitHub -------------------------------------------------------------

type githubStep struct {
	base
	busy   bool
	checks []doctor.Check
}

// ghLoginMsg comes back after `gh auth login`.
type ghLoginMsg struct{ err error }

func (s *githubStep) enter() tea.Cmd {
	s.busy = true
	return s.m.runChecks(setup.StepGitHub, setup.GitHubChecks)
}

func (s *githubStep) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case checksMsg:
		if msg.step == setup.StepGitHub {
			s.busy, s.checks = false, msg.checks
			return s.m.setResult(setup.StepGitHub, setup.GitHubResult(msg.checks))
		}
	case ghLoginMsg:
		if msg.err != nil {
			s.m.flash = "gh auth login: " + msg.err.Error()
		}
		return s.enter()
	}
	return nil
}

func (s *githubStep) key(msg tea.KeyPressMsg) tea.Cmd {
	k := s.m.keys.Setup
	switch {
	case key.Matches(msg, k.Rerun) && !s.busy:
		return s.enter()
	case key.Matches(msg, k.Login) && !s.busy && setup.NeedsLogin(s.checks):
		return tea.ExecProcess(s.m.d.GHLogin(), func(err error) tea.Msg { return ghLoginMsg{err} })
	}
	return nil
}

func (s *githubStep) accept() (tea.Cmd, bool) { return nil, !s.busy }

func (s *githubStep) view(w, _ int) []string {
	if s.busy {
		return []string{s.m.spinner("asking GitHub who you are…")}
	}
	out := s.m.checkLines(s.checks, w)
	if setup.NeedsLogin(s.checks) {
		out = append(out, "", s.m.theme.Body.Render("Press "+s.m.keys.Setup.Login.Help().Key+" to run `gh auth login` here; forge checks again when it's done."))
	}
	return out
}

func (s *githubStep) help() []key.Binding {
	k := s.m.keys.Setup
	if setup.NeedsLogin(s.checks) {
		return []key.Binding{k.Login, k.Rerun}
	}
	return []key.Binding{k.Rerun}
}
func (s *githubStep) ticking() bool { return s.busy && anim.Ticks(s.m.eng.Settings()) }

// --- 6. Animations ---------------------------------------------------------

type animStep struct {
	base
	sel  int
	prev *anim.Engine
	// splashAt is the preview frame the looping splash started on.
	splashAt int
}

func newAnimStep(m *Model) *animStep {
	s := &animStep{base: base{m: m}}
	for i, l := range setup.AnimationLevels {
		if l == m.cfg.UI.Animations {
			s.sel = i
		}
	}
	s.prev = anim.NewEngine(s.settings())
	return s
}

func (s *animStep) settings() anim.Settings {
	st := anim.Resolve(setup.AnimationLevels[s.sel], nil)
	st.Color = s.m.d.Anim.Color // NO_COLOR still stops pulses
	return st
}

func (s *animStep) enter() tea.Cmd { return nil }

func (s *animStep) update(msg tea.Msg) tea.Cmd {
	if s.prev.Update(msg) && s.prev.Frame()-s.splashAt >= anim.SplashLength+anim.FPS {
		s.splashAt = s.prev.Frame() // loop the splash with a pause
	}
	return nil
}

// sync runs the preview clock only while the step is on screen.
func (s *animStep) sync(visible bool) tea.Cmd {
	return s.prev.Sync(visible && anim.Ticks(s.prev.Settings()))
}

func (s *animStep) key(msg tea.KeyPressMsg) tea.Cmd {
	k := s.m.keys.Setup
	old := s.sel
	switch {
	case key.Matches(msg, k.Up):
		s.sel = max(s.sel-1, 0)
	case key.Matches(msg, k.Down):
		s.sel = min(s.sel+1, len(setup.AnimationLevels)-1)
	}
	if s.sel != old {
		s.prev.SetSettings(s.settings())
		s.splashAt = s.prev.Frame()
	}
	return nil
}

func (s *animStep) accept() (tea.Cmd, bool) {
	level := setup.AnimationLevels[s.sel]
	sets, err := setup.AnimationSets(level)
	if err != nil {
		return nil, false
	}
	// The wizard itself follows the choice from now on.
	s.m.eng.SetSettings(anim.Resolve(level, s.m.d.Getenv))
	return tea.Batch(s.m.merge(sets...),
		s.m.setResult(setup.StepAnimations, setup.Result{Status: setup.Done, Detail: "animations: " + string(level)})), true
}

var animBlurbs = map[config.Animations]string{
	config.AnimationsFull:    "the splash, sparks on success, moving hammer",
	config.AnimationsReduced: "no motion; colours still glow and pulse",
	config.AnimationsOff:     "everything still; nothing ticks in the background",
}

func (s *animStep) view(w, h int) []string {
	th := s.m.theme
	var out []string
	for i, l := range setup.AnimationLevels {
		mark := "  "
		if i == s.sel {
			mark = th.DoctorWarn.Render("▸ ")
		}
		name := th.Body.Render(padRight(string(l), 8))
		if i == s.sel {
			name = th.DetailLabel.Render(padRight(string(l), 8))
		}
		out = append(out, mark+name+th.Hint.Render(animBlurbs[l]))
	}
	out = append(out, "", th.DetailLabel.Render("Preview"))
	st := s.prev.Settings()
	f := s.prev.Frame()
	out = append(out, anim.HammerFrame(f, st, th)+" "+th.Body.Render("loading pull requests…"))
	ph := h - len(out) - 1
	switch {
	case st.Motion() && anim.SplashFits(w, ph):
		sp := anim.Splash{Start: s.splashAt, Seed: 7}
		if !sp.Done(f) {
			out = append(out, splitLines(sp.View(f, w, ph, th))...)
		} else {
			out = append(out, splitLines(sp.View(s.splashAt+anim.SplashLength-1, w, ph, th))...)
		}
	case st.Motion():
		out = append(out, th.Hint.Render("(make the window bigger to see the splash)"))
	default:
		out = append(out, "")
		out = append(out, splitLines(anim.Loading("the splash only plays with full animations", f, st, th, w, 5))...)
	}
	return out
}

func (s *animStep) help() []key.Binding {
	k := s.m.keys.Setup
	return []key.Binding{k.Up, k.Down}
}
func (s *animStep) ticking() bool { return false }

// --- 7. Quick capture ------------------------------------------------------

type captureStep struct {
	base
	copied bool
}

type copiedMsg struct{ err error }

func (s *captureStep) enter() tea.Cmd { return nil }

func (s *captureStep) update(msg tea.Msg) tea.Cmd {
	if c, ok := msg.(copiedMsg); ok {
		switch {
		case errors.Is(c.err, setup.ErrNoClipboardTool):
			s.copied = true
			s.m.flash = "copied through the terminal (OSC 52)"
			return tea.SetClipboard(setup.CaptureSnippet)
		case c.err != nil:
			s.m.flash = "copy failed: " + c.err.Error()
		default:
			s.copied = true
			s.m.flash = "copied to the clipboard"
		}
	}
	return nil
}

func (s *captureStep) key(msg tea.KeyPressMsg) tea.Cmd {
	if key.Matches(msg, s.m.keys.Setup.Copy) {
		c := s.m.d.Clipboard
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return copiedMsg{setup.CopyCapture(ctx, c)}
		}
	}
	return nil
}

func (s *captureStep) accept() (tea.Cmd, bool) {
	detail := "snippet shown"
	if s.copied {
		detail = "snippet copied; paste it into ~/.tmux.conf"
	}
	return s.m.setResult(setup.StepCapture, setup.Result{Status: setup.Done, Detail: detail}), true
}

func (s *captureStep) view(w, _ int) []string {
	th := s.m.theme
	return []string{
		th.Body.Render(wrapText("Inside forge's own tmux, Alt+c already opens quick capture. To get it in your own tmux too, add this line to ~/.tmux.conf and reload it (tmux source ~/.tmux.conf):", w)),
		"",
		s.m.code(setup.CaptureSnippet),
		"",
		th.Hint.Render(wrapText("forge never edits ~/.tmux.conf; press "+s.m.keys.Setup.Copy.Help().Key+" to copy the line.", w)),
	}
}

func (s *captureStep) help() []key.Binding { return []key.Binding{s.m.keys.Setup.Copy} }
func (s *captureStep) ticking() bool       { return false }
