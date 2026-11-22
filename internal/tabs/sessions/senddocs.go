package sessions

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/daan3011/forge/internal/config"
	docfs "github.com/daan3011/forge/internal/docs"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/templates"
)

// Send docs to an agent (P4.3). The Docs tab hands over a Source with Docs
// set; here the user picks a new session or a running one. A new session
// gets the docs as @path mentions through {docs}; a running one gets them
// in a short message pasted into its window. Either way the docs are put
// into the worktree first (copied when they live outside the repo).

// newSessionChoice is the picker value for "start a new session".
const newSessionChoice = ""

// docsPickMsg carries the running sessions the docs could go to.
type docsPickMsg struct {
	src  tabs.Source
	live []store.Session
	err  error
}

// docsSentMsg reports docs sent to a running session.
type docsSentMsg struct {
	name string
	n    int
	err  error
}

func (docsPickMsg) TargetTab() string { return Title }
func (docsSentMsg) TargetTab() string { return Title }

// startSendDocs lists the project's running sessions off the update loop;
// docsPicked asks where the docs go.
func (t *Tab) startSendDocs(src tabs.Source) tea.Cmd {
	if t.svc == nil {
		return errCmd(errUnavailable)
	}
	t.busy++
	svc, project := t.svc, t.docsProject(src).Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
		defer cancel()
		rows, err := svc.List(ctx, project, false)
		msg := docsPickMsg{src: src, err: err}
		for _, r := range rows {
			if running(r.Session) {
				msg.live = append(msg.live, r.Session)
			}
		}
		return msg
	}
}

// running reports whether x's agent may still be running: not archived,
// not dead, with a window. SendPrompt has the last word.
func running(x store.Session) bool {
	if x.Archived() || x.TmuxWindow == "" {
		return false
	}
	switch x.Status {
	case store.StatusDead, store.StatusArchived:
		return false
	}
	return true
}

// docsProject is the project the docs' session belongs to: the source's
// repo, else the active project.
func (t *Tab) docsProject(src tabs.Source) config.Project {
	if p, ok := projectForRepo(t.cfg.Projects, src.Repo); ok {
		return p
	}
	if t.project != nil {
		return *t.project
	}
	return config.Project{}
}

func (t *Tab) docsPicked(msg docsPickMsg) tea.Cmd {
	t.busy--
	var cmds []tea.Cmd
	if msg.err != nil {
		cmds = append(cmds, info("couldn't list sessions: "+msg.err.Error()))
	}
	if len(msg.live) == 0 {
		return tea.Batch(append(cmds, t.newDocsSession(msg.src))...)
	}
	if t.dialog != nil {
		return tea.Batch(append(cmds, info("finish the open dialog first"))...)
	}
	choice := newSessionChoice
	opts := []huh.Option[string]{huh.NewOption("New session…", newSessionChoice)}
	for _, x := range msg.live {
		opts = append(opts, huh.NewOption(x.Name+" · "+string(x.Status), x.Name))
	}
	f := styled(t.theme, t.keys, huh.NewGroup(
		huh.NewSelect[string]().Key("to").
			Title("Send "+docNoun(len(msg.src.Docs))+" to").
			Description(docsSummary(msg.src.Docs)).
			Options(opts...).Value(&choice),
	))
	open := t.openDialog("", f, func() tea.Cmd {
		if choice == newSessionChoice {
			return t.newDocsSession(msg.src)
		}
		i := slices.IndexFunc(msg.live, func(x store.Session) bool { return x.Name == choice })
		if i < 0 {
			return nil
		}
		return t.sendDocs(msg.live[i], msg.src.Docs)
	})
	return tea.Batch(append(cmds, open)...)
}

// newDocsSession opens the new-session form; {docs} becomes the docs'
// mentions once the project is chosen (resolveTemplate).
func (t *Tab) newDocsSession(src tabs.Source) tea.Cmd {
	o := agent.StartOpts{
		Name:     t.uniqueName(docsSessionName(src.Docs)),
		Project:  t.docsProject(src).Name,
		Template: t.cfg.Sessions.DefaultTemplate,
	}
	return t.openNewWith(NewRequest{Opts: o, Vars: maps.Clone(src.Vars), Docs: slices.Clone(src.Docs)})
}

// docsSessionName is "docs-<first doc's name>", made a valid session name.
func docsSessionName(paths []string) string {
	stem := "docs"
	if len(paths) > 0 {
		base := filepath.Base(paths[0])
		stem = "docs-" + strings.TrimSuffix(base, filepath.Ext(base))
	}
	return SourceName(tabs.Source{ID: stem})
}

// sendDocs puts the docs into x's worktree and pastes a message that
// mentions them, in a command; docsSent reports it.
func (t *Tab) sendDocs(x store.Session, paths []string) tea.Cmd {
	t.busy++
	svc := t.svc
	plan := docfs.Plan(t.projectPath(x.Project), paths)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		msg := docsSentMsg{name: x.Name, n: len(plan)}
		docs, err := svc.AttachDocs(ctx, x, plan)
		if err != nil {
			msg.err = err
			return msg
		}
		msg.err = svc.SendPrompt(ctx, x, DocsMessage(docs))
		return msg
	}
}

func (t *Tab) docsSent(msg docsSentMsg) tea.Cmd {
	t.busy--
	if msg.err != nil {
		return errCmd(fmt.Errorf("couldn't send docs to %s: %w", msg.name, msg.err))
	}
	t.pending = msg.name
	t.selectName(msg.name)
	return tea.Batch(success(fmt.Sprintf("sent %s to %s", docNoun(msg.n), msg.name)), t.liveRefresh())
}

// projectPath is the main checkout of the project called name; "" when it
// isn't configured (every doc is then copied).
func (t *Tab) projectPath(name string) string {
	for _, p := range t.cfg.Projects {
		if p.Name == name {
			return p.Path
		}
	}
	return ""
}

// docsPlan maps req's docs to the worktree of the project it starts in.
func (t *Tab) docsPlan(req NewRequest) []docfs.Attachment {
	if len(req.Docs) == 0 {
		return nil
	}
	return docfs.Plan(t.projectPath(req.Opts.Project), req.Docs)
}

// withDocsVar sets {docs} to the mentions of req's docs (they win over the
// project's docs folder).
func (t *Tab) withDocsVar(req NewRequest) NewRequest {
	plan := t.docsPlan(req)
	if len(plan) == 0 {
		return req
	}
	req.Vars = maps.Clone(req.Vars)
	if req.Vars == nil {
		req.Vars = map[string]string{}
	}
	req.Vars["docs"] = docfs.Mentions(plan)
	return req
}

// DocsMessage is the prompt sent to a running session for docs.
func DocsMessage(plan []docfs.Attachment) string {
	return "Read these docs for context: " + docfs.Mentions(plan) + " and keep them in mind."
}

// DocsPrompt is prompt with the docs' mentions appended when tmpl doesn't
// place them itself through {docs}.
func DocsPrompt(tmpl templates.Template, prompt string, plan []docfs.Attachment) string {
	if len(plan) == 0 || slices.Contains(templates.Vars(tmpl.Body), "docs") {
		return prompt
	}
	line := "Read these docs first: " + docfs.Mentions(plan) + "\n"
	if strings.TrimSpace(prompt) == "" {
		return line
	}
	return strings.TrimRight(prompt, "\n") + "\n\n" + line
}

func docNoun(n int) string {
	if n == 1 {
		return "1 doc"
	}
	return fmt.Sprintf("%d docs", n)
}

// docsSummary lists the docs' file names for the picker.
func docsSummary(paths []string) string {
	names := make([]string, len(paths))
	for i, p := range paths {
		names[i] = filepath.Base(p)
	}
	return strings.Join(names, ", ")
}
