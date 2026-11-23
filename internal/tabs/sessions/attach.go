package sessions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/store"
)

// Attach & resume (P1.5): enter attaches to a session, resuming it first
// when its agent is gone; b resumes it in the background. Before resuming,
// the service is asked what that involves (Inspect) so the user can be
// asked about a missing worktree or a first prompt to send again.

// resumeAction is what the user asked for.
type resumeAction int

const (
	actAttach     resumeAction = iota // resume if needed, then attach
	actBackground                     // resume, don't attach
)

type (
	inspectedMsg struct {
		act     resumeAction
		session store.Session
		plan    agent.ResumePlan
		err     error
	}
	resumedMsg struct {
		act     resumeAction
		session store.Session
		plan    agent.ResumePlan
		opts    agent.ResumeOpts
		res     agent.Resumed
		err     error
	}
	attachMsg struct {
		name string
		a    tmux.Attachment
		err  error
	}
	// detachedMsg arrives when the user is back in forge after attaching.
	detachedMsg struct {
		name string
		err  error
	}
)

func (inspectedMsg) TargetTab() string { return Title }
func (resumedMsg) TargetTab() string   { return Title }
func (attachMsg) TargetTab() string    { return Title }
func (detachedMsg) TargetTab() string  { return Title }

// updateAttach handles the attach/resume messages; ok is false for others.
func (t *Tab) updateAttach(msg tea.Msg) (cmd tea.Cmd, ok bool) {
	switch msg := msg.(type) {
	case inspectedMsg:
		return t.inspected(msg), true
	case resumedMsg:
		return t.resumed(msg), true
	case attachMsg:
		return t.attached(msg), true
	case detachedMsg:
		return t.detached(msg), true
	}
	return nil, false
}

// attachSelected attaches to the selected session.
func (t *Tab) attachSelected() tea.Cmd {
	r, ok := t.current()
	if !ok {
		return nil
	}
	return t.begin(r, actAttach)
}

// resumeSelected resumes the selected session in the background.
func (t *Tab) resumeSelected() tea.Cmd {
	r, ok := t.current()
	if !ok {
		return nil
	}
	return t.begin(r, actBackground)
}

// AttachSession implements tabs.SessionGoer: select the session and attach
// to it, resuming it first if needed.
func (t *Tab) AttachSession(name string) tea.Cmd {
	r, ok := t.rows[name]
	if !ok {
		return info("session " + name + " isn't in the list (another project?)")
	}
	return tea.Batch(t.Select(name), t.begin(r, actAttach))
}

// begin starts an attach or resume of r: first ask the service what it
// involves. A second request for the same session while one runs is
// ignored.
func (t *Tab) begin(r agent.Row, act resumeAction) tea.Cmd {
	if t.svc == nil {
		return errCmd(errUnavailable)
	}
	if r.Archived() {
		return info(r.Name + " is archived — restore it (" + t.keys.Sessions.Archive.Help().Key + " in the archived view) to work on it again")
	}
	if t.resuming[r.Name] {
		return nil
	}
	t.resuming[r.Name] = true
	t.busy++
	svc, x := t.svc, r.Session
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
		defer cancel()
		plan, err := svc.Inspect(ctx, x)
		return inspectedMsg{act: act, session: x, plan: plan, err: err}
	}
}

// finish ends the attach/resume of name.
func (t *Tab) finish(name string) {
	if t.resuming[name] {
		delete(t.resuming, name)
		t.busy--
	}
}

func (t *Tab) inspected(msg inspectedMsg) tea.Cmd {
	x := msg.session
	if msg.err != nil {
		t.finish(x.Name)
		return errCmd(fmt.Errorf("resume %s: %w", x.Name, msg.err))
	}
	if msg.plan.Alive {
		if msg.act == actBackground {
			t.finish(x.Name)
			return info(x.Name + " is already running")
		}
		return t.attach(x)
	}
	return t.askWorktree(msg.act, x, msg.plan, agent.ResumeOpts{})
}

// askWorktree asks to recreate a missing worktree, then goes on to
// askPrompt.
func (t *Tab) askWorktree(act resumeAction, x store.Session, plan agent.ResumePlan, o agent.ResumeOpts) tea.Cmd {
	if !plan.WorktreeMissing || o.RecreateWorktree {
		return t.askPrompt(act, x, plan, o)
	}
	yes := true
	branch := dash(x.Branch)
	f := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Key("recreate").
			Title("The worktree of " + x.Name + " is gone. Recreate it from " + branch + "?").
			Description(dash(x.Worktree) + " — if the branch is gone too, it starts again from " + dash(x.BaseBranch)).
			Affirmative("Recreate").Negative("Cancel").Value(&yes),
	)).WithTheme(t.theme.Huh()).WithKeyMap(t.keys.HuhKeyMap()).WithShowHelp(false)
	return t.openDialogOr(x.Name, f, func() tea.Cmd {
		if !yes {
			t.finish(x.Name)
			return nil
		}
		o.RecreateWorktree = true
		return t.askPrompt(act, x, plan, o)
	}, func() { t.finish(x.Name) })
}

// askPrompt offers to send the first prompt again when there is no
// conversation to resume, then resumes.
func (t *Tab) askPrompt(act resumeAction, x store.Session, plan agent.ResumePlan, o agent.ResumeOpts) tea.Cmd {
	if plan.HasTranscript || plan.PromptFile == "" {
		return t.resume(act, x, plan, o)
	}
	yes := true
	f := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Key("resend").
			Title(x.Name + " has no conversation to resume. Send its first prompt again?").
			Description("claude starts afresh with the same session id").
			Affirmative("Send it").Negative("Start empty").Value(&yes),
	)).WithTheme(t.theme.Huh()).WithKeyMap(t.keys.HuhKeyMap()).WithShowHelp(false)
	return t.openDialogOr(x.Name, f, func() tea.Cmd {
		o.ResendPrompt = yes
		return t.resume(act, x, plan, o)
	}, func() { t.finish(x.Name) })
}

func (t *Tab) resume(act resumeAction, x store.Session, plan agent.ResumePlan, o agent.ResumeOpts) tea.Cmd {
	svc := t.svc
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		res, err := svc.Resume(ctx, x, o)
		return resumedMsg{act: act, session: x, plan: plan, opts: o, res: res, err: err}
	}
}

func (t *Tab) resumed(msg resumedMsg) tea.Cmd {
	x := msg.session
	var missing *agent.WorktreeMissingError
	switch {
	case errors.As(msg.err, &missing):
		// Gone since Inspect looked: ask now.
		msg.plan.WorktreeMissing = true
		return t.askWorktree(msg.act, x, msg.plan, msg.opts)
	case msg.err != nil:
		t.finish(x.Name)
		return tea.Batch(t.forceRefresh(), errCmd(fmt.Errorf("resume %s: %w", x.Name, msg.err)))
	}
	cmds := []tea.Cmd{t.forceRefresh()}
	if note := resumeNote(msg); note != "" {
		cmds = append(cmds, info(note))
	}
	if msg.act == actBackground {
		t.finish(x.Name)
		text := "resumed " + x.Name + " in the background"
		if msg.res.AlreadyRunning {
			text = x.Name + " is already running"
		}
		return tea.Batch(append(cmds, success(text))...)
	}
	return tea.Batch(append(cmds, t.attach(msg.res.Session))...)
}

// resumeNote explains anything unusual a resume did.
func resumeNote(msg resumedMsg) string {
	r, name := msg.res, msg.session.Name
	var notes []string
	switch {
	case r.BranchRecreated:
		notes = append(notes, "its branch was gone too: recreated from "+dash(msg.session.BaseBranch))
	case r.WorktreeRecreated:
		notes = append(notes, "worktree recreated from "+dash(r.Session.Branch))
	}
	if r.FetchErr != nil {
		notes = append(notes, "couldn't fetch origin; branched from the local base")
	}
	if r.SkillsErr != nil {
		notes = append(notes, "some forge slash commands couldn't be written ("+r.SkillsErr.Error()+")")
	}
	if r.Fresh {
		s := "no conversation to resume: started afresh"
		if msg.opts.ResendPrompt {
			s += " with its first prompt"
		}
		notes = append(notes, s)
	}
	if r.NewID {
		notes = append(notes, "claude refused the old session id, so it got a new one (links kept)")
	}
	if len(notes) == 0 {
		return ""
	}
	return name + ": " + strings.Join(notes, "; ")
}

// attach asks the service for an attachment to x's window. The resume
// guard ends when it arrives.
func (t *Tab) attach(x store.Session) tea.Cmd {
	svc := t.svc
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
		defer cancel()
		a, err := svc.Attach(ctx, x)
		return attachMsg{name: x.Name, a: a, err: err}
	}
}

// attached shows the window: forge's own tmux client was already switched;
// a popup of the user's tmux runs in the background while forge stays on
// screen; otherwise tmux attaches in this terminal until the user detaches.
func (t *Tab) attached(msg attachMsg) tea.Cmd {
	t.finish(msg.name)
	if msg.err != nil {
		return tea.Batch(t.forceRefresh(), errCmd(fmt.Errorf("attach %s: %w", msg.name, msg.err)))
	}
	var cmds []tea.Cmd
	if msg.a.Warning != "" {
		cmds = append(cmds, info(msg.a.Warning))
	}
	name := msg.name
	switch {
	case msg.a.Mode == tmux.AttachSwitched || msg.a.Cmd == nil:
		cmds = append(cmds, t.forceRefresh())
	case msg.a.Mode == tmux.AttachPopup:
		c := msg.a.Cmd
		cmds = append(cmds, func() tea.Msg { return detachedMsg{name: name, err: c.Run()} })
	default:
		cmds = append(cmds, tea.ExecProcess(msg.a.Cmd, func(err error) tea.Msg { return detachedMsg{name: name, err: err} }))
	}
	return tea.Batch(cmds...)
}

// detached refreshes the session's status and restarts its preview.
func (t *Tab) detached(msg detachedMsg) tea.Cmd {
	t.previewGen++
	t.previewBusy = false
	t.promptMiss = 0
	cmds := []tea.Cmd{t.forceRefresh(), t.capture()}
	if msg.err != nil {
		cmds = append(cmds, errCmd(fmt.Errorf("attach %s: %w", msg.name, msg.err)))
	}
	return tea.Batch(cmds...)
}
