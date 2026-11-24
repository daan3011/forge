package wizard

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/setup"
	"github.com/daan3011/forge/internal/ui/anim"
)

// --- 5. tmux ---------------------------------------------------------------

type keyTest int

const (
	keyNotRun keyTest = iota
	keyWaiting
	keyDone
	keySkipped
)

type tmuxStep struct {
	base
	busy      bool
	state     setup.ConfState
	version   int
	wrote     bool
	backup    string
	detachKey string
	err       error

	test   keyTest
	keyRes setup.KeyResult

	popup   tmux.Driver
	popupOK bool
	popupAt string // "", "running", "back", "failed: ..."
}

type tmuxConfMsg struct {
	state     setup.ConfState
	version   int
	wrote     bool
	backup    string
	detachKey string
	err       error
}

type popupReadyMsg struct {
	drv tmux.Driver
	a   tmux.Attachment
	err error
}

type popupBackMsg struct{ err error }

type popupStoppedMsg struct{}

func (s *tmuxStep) enter() tea.Cmd { return s.check(false) }

// check reads forge's tmux.conf, writing the default when it is missing
// (or, with reset, replacing an outdated one after a backup).
func (s *tmuxStep) check(reset bool) tea.Cmd {
	s.busy = true
	path, colors, now := s.m.d.TmuxConf, s.m.d.TmuxColors, s.m.d.Now()
	return func() tea.Msg {
		var msg tmuxConfMsg
		if path == "" {
			return tmuxConfMsg{err: fmt.Errorf("no tmux config path")}
		}
		if reset {
			msg.backup, msg.err = tmux.ResetConfig(path, colors, now)
		} else if st, _ := setup.TmuxConfState(path); st == setup.ConfMissing {
			msg.wrote, msg.err = tmux.EnsureConfig(path, colors)
		}
		msg.state, msg.version = setup.TmuxConfState(path)
		msg.detachKey = tmux.DetachKeyOf(path)
		return msg
	}
}

func (s *tmuxStep) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tmuxConfMsg:
		s.busy = false
		s.state, s.version, s.err = msg.state, msg.version, msg.err
		s.wrote = s.wrote || msg.wrote
		if msg.backup != "" {
			s.backup = msg.backup
		}
		s.detachKey = msg.detachKey
		if s.test == keyNotRun && msg.err == nil {
			s.test = keyWaiting // straight into the key test
		}
		if s.test == keyWaiting && msg.err == nil {
			return nil // the result follows the key test
		}
		return s.record()
	case popupReadyMsg:
		if msg.err != nil {
			s.popupAt = "failed: " + msg.err.Error()
			return s.stopPopup(msg.drv)
		}
		s.popup, s.popupAt = msg.drv, "running"
		return attachCmd(msg.a, func(err error) tea.Msg { return popupBackMsg{err} })
	case popupBackMsg:
		if msg.err != nil {
			s.popupAt = "failed: " + msg.err.Error()
		} else {
			s.popupAt, s.popupOK = "back", true
		}
		drv := s.popup
		s.popup = nil
		return tea.Batch(s.stopPopup(drv), s.record())
	}
	return nil
}

func (s *tmuxStep) stopPopup(d tmux.Driver) tea.Cmd {
	if d == nil {
		return nil
	}
	return func() tea.Msg {
		_ = setup.StopPopupTest(context.Background(), d)
		return popupStoppedMsg{}
	}
}

// result is the step's outcome so far.
func (s *tmuxStep) result() setup.Result {
	label := tmux.KeyLabel(s.detachKey)
	switch {
	case s.err != nil:
		return setup.Result{Status: setup.Failed, Detail: s.err.Error(), Fix: "forge doctor"}
	case s.test == keyDone && s.keyRes != setup.KeyOK:
		fix := "fix your terminal's keys (see `forge setup tmux`)"
		if setup.UsesAlt(s.detachKey) {
			fix = "turn on Option as Meta in " + setup.DetectTerminal(s.m.d.Getenv).String() + ", then forge setup tmux"
		}
		return setup.Result{Status: setup.Failed, Detail: label + " doesn't reach tmux", Fix: fix}
	case s.state == setup.ConfOutdated:
		return setup.Result{Status: setup.Failed, Detail: s.state.Describe(s.m.d.TmuxConf, s.version), Fix: setup.ResetHint}
	}
	detail := "tmux.conf ready; "
	switch {
	case s.test == keyDone && s.popupOK:
		detail += label + " works, also inside tmux"
	case s.test == keyDone:
		detail += label + " arrives correctly"
	default:
		detail += "key test skipped"
	}
	return setup.Result{Status: setup.Done, Detail: detail}
}

func (s *tmuxStep) record() tea.Cmd { return s.m.setResult(setup.StepTmux, s.result()) }

func (s *tmuxStep) grabs() bool { return s.test == keyWaiting }

func (s *tmuxStep) key(msg tea.KeyPressMsg) tea.Cmd {
	k := s.m.keys.Setup
	if s.test == keyWaiting {
		if key.Matches(msg, s.m.keys.Input.Cancel) && setup.ClassifyKey(s.detachKey, msg) != setup.KeyOK {
			s.test = keySkipped
			return s.record()
		}
		s.keyRes, s.test = setup.ClassifyKey(s.detachKey, msg), keyDone
		if s.keyRes == setup.KeyOther {
			s.test = keyWaiting // not an answer: ask again
			s.m.flash = strings.Join(setup.KeyAdvice(setup.KeyOther, s.detachKey, setup.TermUnknown), " ")
			return nil
		}
		s.m.flash = ""
		return s.record()
	}
	if s.busy {
		return nil
	}
	switch {
	case key.Matches(msg, k.Rerun):
		s.test, s.popupAt, s.popupOK = keyWaiting, "", false
		return s.check(false)
	case key.Matches(msg, k.Reset) && s.state == setup.ConfOutdated:
		return s.check(true)
	case key.Matches(msg, k.Test) && s.popupAt != "running" && s.m.d.NewTmux != nil:
		return s.startPopup()
	}
	return nil
}

// startPopup opens the throwaway window and attaches to it.
func (s *tmuxStep) startPopup() tea.Cmd {
	s.popupAt = "running"
	newTmux, conf, socket, detach := s.m.d.NewTmux, s.m.d.TmuxConf, setup.PopupSocket(s.m.d.PID), s.detachKey
	return func() tea.Msg {
		ctx, cancel := opCtx(10 * time.Second)
		defer cancel()
		d := newTmux(socket, conf)
		a, err := setup.StartPopupTest(ctx, d, detach)
		return popupReadyMsg{drv: d, a: a, err: err}
	}
}

func (s *tmuxStep) accept() (tea.Cmd, bool) {
	if s.busy || s.popupAt == "running" {
		return nil, false
	}
	return s.record(), true
}

func (s *tmuxStep) view(w, _ int) []string {
	th := s.m.theme
	if s.busy {
		return []string{s.m.spinner("checking forge's tmux config…")}
	}
	path := s.m.d.TmuxConf
	out := []string{th.Body.Render(wrapText(s.state.Describe(path, s.version), w))}
	if s.wrote {
		out = append(out, th.DoctorOK.Render("✓ wrote forge's tmux.conf"))
	}
	if s.backup != "" {
		out = append(out, th.DoctorOK.Render(wrapText("✓ reset; your old file is at "+s.backup, w)))
	}
	if s.state == setup.ConfOutdated {
		out = append(out, th.Hint.Render(wrapText("Press "+s.m.keys.Setup.Reset.Help().Key+" to reset it (the old file is backed up).", w)))
	}
	out = append(out, "")
	label := tmux.KeyLabel(s.detachKey)
	switch s.test {
	case keyWaiting:
		out = append(out,
			th.DetailLabel.Render("Key test"),
			th.Body.Render(wrapText("In an agent window, "+label+" takes you back to forge. Press "+label+" now.", w)),
			th.Hint.Render("(esc skips the test)"))
	case keyDone:
		term := setup.DetectTerminal(s.m.d.Getenv)
		style := th.DoctorOK
		if s.keyRes != setup.KeyOK {
			style = th.DoctorFail
		}
		for i, l := range setup.KeyAdvice(s.keyRes, s.detachKey, term) {
			if i == 0 {
				out = append(out, style.Render(wrapText(l, w)))
				continue
			}
			out = append(out, th.Body.Render(wrapText(l, w)))
		}
	case keySkipped:
		out = append(out, th.Hint.Render("Key test skipped ("+s.m.keys.Setup.Rerun.Help().Key+" runs it again)."))
	}
	if s.test == keyDone || s.test == keySkipped {
		out = append(out, "")
		switch {
		case s.popupAt == "running":
			out = append(out, th.Body.Render("In the test window: press "+label+" to come back."))
		case s.popupAt == "back":
			out = append(out, th.DoctorOK.Render("✓ popup test: "+label+" brought you back from tmux"))
		case strings.HasPrefix(s.popupAt, "failed"):
			out = append(out, th.DoctorFail.Render(wrapText("✗ popup test "+s.popupAt, w)))
		default:
			out = append(out, th.Hint.Render(wrapText("Press "+s.m.keys.Setup.Test.Help().Key+" to open a throwaway tmux window and try "+label+" for real.", w)))
		}
	}
	return out
}

func (s *tmuxStep) help() []key.Binding {
	k := s.m.keys.Setup
	bs := []key.Binding{k.Test, k.Rerun}
	if s.state == setup.ConfOutdated {
		bs = append(bs, k.Reset)
	}
	return bs
}
func (s *tmuxStep) ticking() bool { return s.busy && anim.Ticks(s.m.eng.Settings()) }

func (s *tmuxStep) leave() tea.Cmd { return s.stopPopup(s.popup) }

// --- 8. Smoke test ---------------------------------------------------------

type smokeStep struct {
	base
	run     setup.SmokeRun
	state   setup.SmokeState
	started bool
	ending  bool // cleanup in flight
	done    bool
	gen     int
	preview string
	lines   []string
	err     error
}

type (
	smokeStartedMsg struct {
		run setup.SmokeRun
		err error
	}
	smokePollMsg struct{ gen int }
	smokeObsMsg  struct {
		gen     int
		o       setup.Observation
		preview string
		err     error
	}
	smokeAttachMsg struct {
		a   tmux.Attachment
		err error
	}
	smokeBackMsg struct{ err error }
	smokeDoneMsg struct {
		preview string
		lines   []string
		err     error
	}
)

func (s *smokeStep) enter() tea.Cmd { return nil }

func (s *smokeStep) start() tea.Cmd {
	r := s.m.d.Smoke
	if r == nil {
		s.err = fmt.Errorf("the smoke test isn't available here")
		return nil
	}
	s.started, s.done, s.err, s.preview, s.lines = true, false, nil, "", nil
	s.state = setup.NewSmokeState(s.m.d.Now())
	s.gen++
	return func() tea.Msg {
		ctx, cancel := opCtx(time.Minute)
		defer cancel()
		run, err := r.Start(ctx)
		return smokeStartedMsg{run: run, err: err}
	}
}

func (s *smokeStep) poll() tea.Cmd {
	gen := s.gen
	return tea.Tick(s.m.d.SmokePoll, func(time.Time) tea.Msg { return smokePollMsg{gen} })
}

func (s *smokeStep) observe() tea.Cmd {
	run, gen, wantPreview := s.run, s.gen, s.state.Phase == setup.SmokeNeedsInput
	_, h := s.m.split.RightSize()
	lines := max(h-14, 6)
	return func() tea.Msg {
		ctx, cancel := opCtx(5 * time.Second)
		defer cancel()
		o, err := run.Observe(ctx)
		msg := smokeObsMsg{gen: gen, o: o, err: err}
		if wantPreview {
			msg.preview, _ = run.Preview(ctx, lines)
		}
		return msg
	}
}

// finish reports the registrations, keeps the last pane and cleans up.
func (s *smokeStep) finish() tea.Cmd {
	run := s.run
	s.ending = true
	_, h := s.m.split.RightSize()
	lines := max(h-14, 6)
	keepPreview := s.state.Phase != setup.SmokePassed
	return func() tea.Msg {
		ctx, cancel := opCtx(20 * time.Second)
		defer cancel()
		msg := smokeDoneMsg{lines: run.Checks(ctx)}
		if keepPreview {
			msg.preview, _ = run.Preview(ctx, lines)
		}
		msg.err = run.Cleanup(ctx)
		return msg
	}
}

func (s *smokeStep) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case smokeStartedMsg:
		if msg.err != nil {
			s.started, s.err = false, msg.err
			return s.m.setResult(setup.StepSmoke, setup.Result{Status: setup.Failed, Detail: msg.err.Error(), Fix: "forge doctor"})
		}
		s.run = msg.run
		return s.observe()
	case smokePollMsg:
		if msg.gen == s.gen && s.run != nil && !s.state.Done() {
			return s.observe()
		}
	case smokeObsMsg:
		if msg.gen != s.gen || s.run == nil || s.state.Done() {
			return nil
		}
		if msg.err == nil {
			s.state = s.state.Advance(msg.o, s.m.d.Now())
		} else {
			s.state = s.state.Advance(setup.Observation{}, s.m.d.Now())
		}
		if msg.preview != "" {
			s.preview = msg.preview
		}
		if s.state.Done() {
			return s.finish()
		}
		return s.poll()
	case smokeAttachMsg:
		if msg.err != nil {
			s.m.flash = "attach: " + msg.err.Error()
			return nil
		}
		return attachCmd(msg.a, func(err error) tea.Msg { return smokeBackMsg{err} })
	case smokeBackMsg:
		if msg.err != nil {
			s.m.flash = "attach: " + msg.err.Error()
		}
		s.gen++ // restart polling at once
		return s.observe()
	case smokeDoneMsg:
		s.ending, s.done, s.run = false, true, nil
		s.lines = msg.lines
		if msg.preview != "" {
			s.preview = msg.preview
		}
		r := s.state.Result(msg.lines)
		if r.Status == setup.Done && !setup.RegistrationsOK(msg.lines) {
			r.Status, r.Detail, r.Fix = setup.Failed, r.Detail+", but some registrations are missing", "forge doctor --fix"
		}
		if msg.err != nil {
			s.m.flash = "cleanup: " + msg.err.Error()
		}
		return s.m.setResult(setup.StepSmoke, r)
	}
	return nil
}

func (s *smokeStep) key(msg tea.KeyPressMsg) tea.Cmd {
	k := s.m.keys.Setup
	switch {
	case key.Matches(msg, k.Test), key.Matches(msg, k.Rerun):
		if !s.running() {
			return s.start()
		}
	case key.Matches(msg, k.Attach) && s.run != nil && !s.state.Done():
		run := s.run
		return func() tea.Msg {
			ctx, cancel := opCtx(5 * time.Second)
			defer cancel()
			a, err := run.Attach(ctx)
			return smokeAttachMsg{a: a, err: err}
		}
	}
	return nil
}

func (s *smokeStep) running() bool { return (s.started && !s.done) || s.ending }

func (s *smokeStep) accept() (tea.Cmd, bool) {
	switch {
	case s.running():
		return nil, false
	case !s.started && !s.done: // enter starts the test
		return s.start(), false
	}
	return nil, true
}

func (s *smokeStep) view(w, h int) []string {
	th := s.m.theme
	out := []string{}
	if !s.started && !s.done {
		out = append(out,
			th.Body.Render(wrapText("forge starts a throwaway Claude Code session in a temp git repo, on its own tmux server, with a one-line prompt. It passes once the hooks flip the session starting → working → idle. Everything is removed afterwards.", w)),
			"",
			th.Hint.Render(wrapText("This uses your real claude login and a few tokens. Press "+s.m.keys.Setup.Next.Help().Key+" (or "+s.m.keys.Setup.Test.Help().Key+") to start.", w)))
		if s.err != nil {
			out = append(out, "", th.DoctorFail.Render(wrapText("✗ "+s.err.Error(), w)))
		}
		return out
	}
	elapsed := s.m.d.Now().Sub(s.state.Started).Truncate(time.Second)
	switch {
	case s.done:
		out = append(out, th.Body.Render(s.state.LastSeen()))
	case s.ending:
		out = append(out, s.m.spinner("cleaning up…"))
	case s.run == nil:
		out = append(out, s.m.spinner("starting the throwaway session…"))
	default:
		out = append(out, s.m.spinner(fmt.Sprintf("waiting for hooks · %s · %s", elapsed, s.state.LastSeen())))
	}
	if s.state.Phase == setup.SmokeNeedsInput && !s.done {
		out = append(out, "",
			th.DoctorWarn.Render(wrapText("Claude is asking something (likely whether to trust the folder). Press "+s.m.keys.Setup.Attach.Help().Key+" to attach and answer; you come back here when you detach.", w)))
	}
	for _, l := range s.lines {
		style := th.DoctorOK
		if strings.HasPrefix(l, "✗") {
			style = th.DoctorFail
		}
		out = append(out, style.Render(l))
	}
	if s.preview != "" && (s.state.Phase == setup.SmokeNeedsInput || s.state.Phase == setup.SmokeTimedOut || s.state.Phase == setup.SmokeFailed) {
		room := max(h-len(out)-3, 3)
		pl := splitLines(s.preview)
		if len(pl) > room {
			pl = pl[len(pl)-room:]
		}
		out = append(out, "", th.PreviewRule.Render(strings.Repeat("─", max(w-2, 4))))
		for _, l := range pl {
			out = append(out, th.PreviewText.Render(truncate(l, w)))
		}
	}
	return out
}

func (s *smokeStep) help() []key.Binding {
	k := s.m.keys.Setup
	switch {
	case s.run != nil && s.state.Phase == setup.SmokeNeedsInput:
		return []key.Binding{k.Attach}
	case !s.running():
		return []key.Binding{k.Test}
	}
	return nil
}

func (s *smokeStep) ticking() bool { return s.running() && anim.Ticks(s.m.eng.Settings()) }

// leave removes the throwaway session when the wizard quits mid-test.
func (s *smokeStep) leave() tea.Cmd {
	if s.run == nil {
		return nil
	}
	run := s.run
	s.run = nil
	return func() tea.Msg {
		_ = run.Cleanup(context.Background())
		return nil
	}
}
