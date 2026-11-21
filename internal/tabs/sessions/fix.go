package sessions

import (
	"context"
	"fmt"
	"slices"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/templates"
)

// "Fix this" from CI (P2.8). The Pipelines and PRs tabs gather the failure
// into a tabs.Source with Fix set; here it either goes into a live session
// linked to the PR, or starts a new session from the fix-ci template, with
// or without the form.

// fixReadyMsg is the result of the fix command: either the prompt was sent
// to a live session, or the template was resolved for a new one.
type fixReadyMsg struct {
	req       NewRequest
	fix       tabs.FixRequest
	templates []templates.Template
	loadErrs  []error

	sent    string // the live session the prompt went to
	sendErr error  // sending to a live session failed; a new one starts
	// target and prompt: a live session to send prompt to once it has
	// been edited (alt+f).
	target *store.Session
	prompt string

	tmpl    templates.Template
	found   bool // the request's template exists
	vals    map[string]string
	missing []string
	errs    []error
}

func (fixReadyMsg) TargetTab() string { return Title }

// startFix handles a Source with Fix set.
func (t *Tab) startFix(src tabs.Source, req NewRequest) tea.Cmd {
	if t.svc == nil {
		return errCmd(errUnavailable)
	}
	fix := *src.Fix
	if fix.Confirm == "" {
		return t.runFix(src, fix, req)
	}
	ok := true
	f := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Key("fix").Title(fix.Confirm).
			Affirmative("Start").Negative("Cancel").Value(&ok),
	)).WithTheme(t.theme.Huh()).WithKeyMap(t.keys.HuhKeyMap()).WithShowHelp(false)
	return t.openDialog(req.Opts.Name, f, func() tea.Cmd {
		if !ok {
			return nil
		}
		return t.runFix(src, fix, req)
	})
}

// runFix looks for a live linked session (Reuse) and resolves the template
// (Immediate) in a command; fixReady continues.
func (t *Tab) runFix(src tabs.Source, fix tabs.FixRequest, req NewRequest) tea.Cmd {
	if !fix.Reuse && !fix.Immediate {
		return t.openNewWith(req)
	}
	t.busy++
	svc, dir, reg, projects := t.svc, t.tmplDir, t.providers, t.cfg.Projects
	ref := store.Ref{Kind: src.Kind, ID: src.ID}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		ts, errs := loadTemplates(dir)
		tmpl, found := templates.Find(ts, req.Opts.Template)
		msg := fixReadyMsg{req: req, fix: fix, templates: ts, loadErrs: errs, tmpl: tmpl, found: found}
		if fix.Reuse && found {
			if x, ok := liveLinked(ctx, svc, ref); ok {
				// The prompt is rendered for the session it goes to.
				o := req.Opts
				o.Name, o.Project, o.Branch = x.Name, x.Project, x.Branch
				vals, _, _ := templates.Resolve(ctx, reg, tmpl, TemplateInput(projects, o, req.Refs, req.Vars))
				prompt := RenderPrompt(tmpl, vals)
				if !fix.Immediate {
					msg.target, msg.prompt = &x, prompt
					return msg
				}
				err := svc.SendPrompt(ctx, x, prompt)
				if err == nil {
					msg.sent = x.Name
					return msg
				}
				msg.sendErr = fmt.Errorf("couldn't send to %s: %w", x.Name, err)
			}
		}
		if fix.Immediate && found {
			msg.vals, msg.missing, msg.errs = templates.Resolve(ctx, reg, tmpl,
				TemplateInput(projects, req.Opts, req.Refs, req.Vars))
		}
		return msg
	}
}

// liveLinked is the newest session linked to ref whose agent may still be
// running: not archived, not dead, with a window. SendPrompt has the last
// word on whether the window is alive.
func liveLinked(ctx context.Context, svc agent.API, ref store.Ref) (store.Session, bool) {
	xs, err := svc.LinkedSessions(ctx, ref.Kind, ref.ID)
	if err != nil {
		return store.Session{}, false
	}
	for _, x := range slices.Backward(xs) {
		if x.Archived() || x.TmuxWindow == "" {
			continue
		}
		switch x.Status {
		case store.StatusDead, store.StatusArchived:
			continue
		}
		return x, true
	}
	return store.Session{}, false
}

func (t *Tab) fixReady(msg fixReadyMsg) tea.Cmd {
	t.busy--
	var cmds []tea.Cmd
	for _, err := range msg.loadErrs {
		cmds = append(cmds, errCmd(err))
	}
	if msg.sent != "" {
		t.pending = msg.sent
		t.selectName(msg.sent)
		cmds = append(cmds, success("sent the failure to "+msg.sent), t.liveRefresh())
		return tea.Batch(cmds...)
	}
	if msg.target != nil {
		return tea.Batch(append(cmds, t.editAndSend(msg))...)
	}
	if msg.sendErr != nil {
		cmds = append(cmds, info(msg.sendErr.Error()+"; starting a new session"))
	}
	form := func() tea.Cmd {
		return t.templatesLoaded(templatesLoadedMsg{req: msg.req, templates: msg.templates})
	}
	switch {
	case !msg.found:
		cmds = append(cmds, info("template "+msg.req.Opts.Template+" not found; pick one"), form())
	case msg.fix.Immediate && len(msg.missing) == 0 && len(msg.errs) == 0:
		o := t.withRefLinks(msg.req.Opts, msg.req.Refs)
		o.Prompt = RenderPrompt(msg.tmpl, msg.vals)
		cmds = append(cmds, t.startAs(o, true))
	default:
		cmds = append(cmds, form())
	}
	return tea.Batch(cmds...)
}

// editAndSend shows the prompt for a live session, editable, and sends it.
// A failed send continues as if no session were live (a new one, with the
// form).
func (t *Tab) editAndSend(msg fixReadyMsg) tea.Cmd {
	x, prompt := *msg.target, msg.prompt
	f := styled(t.theme, t.keys, huh.NewGroup(promptField(&prompt).Lines(12)).Title("Send to "+x.Name))
	return t.openDialog(x.Name, f, func() tea.Cmd {
		t.busy++
		svc := t.svc
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
			defer cancel()
			next := msg
			next.target, next.prompt = nil, ""
			if err := svc.SendPrompt(ctx, x, prompt); err != nil {
				next.sendErr = fmt.Errorf("couldn't send to %s: %w", x.Name, err)
			} else {
				next.sent = x.Name
			}
			return next
		}
	})
}

// loadTemplates writes the default templates on first use and loads dir.
func loadTemplates(dir string) ([]templates.Template, []error) {
	if dir == "" {
		return nil, nil
	}
	if _, err := templates.EnsureDefaults(dir); err != nil {
		return nil, []error{err}
	}
	return templates.Load(dir)
}
