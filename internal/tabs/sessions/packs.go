package sessions

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/daan3011/forge/internal/config"
	docfs "github.com/daan3011/forge/internal/docs"
	"github.com/daan3011/forge/internal/packs"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/templates"
)

// Context packs (P5.2). The new-session form offers a multi-select of
// packs; the chosen ones are expanded against the project, their files go
// into the worktree with the P4.3 docs machinery (so mentions survive
// copies), and the first prompt starts with their preambles and one line of
// @path mentions. "Attach pack" sends the same block to a running session.

// LoadPacks reads the packs folder; "" is no packs.
func LoadPacks(dir string) ([]packs.Pack, []error) {
	if dir == "" {
		return nil, nil
	}
	return packs.Load(dir)
}

// ResolvePacks expands the packs called names (in that order) against p.
// It returns notes for entries that matched nothing, and an error naming
// packs that don't exist.
func ResolvePacks(all []packs.Pack, names []string, p config.Project) ([]packs.Resolved, []string, error) {
	docs := ""
	if p.Path != "" {
		docs = p.DocsPath()
	}
	var (
		out     []packs.Resolved
		notes   []string
		unknown []string
	)
	for _, name := range names {
		pk, ok := packs.Find(all, name)
		if !ok {
			unknown = append(unknown, name)
			continue
		}
		r := packs.Expand(pk, p.Path, docs)
		if len(r.Missing) > 0 {
			notes = append(notes, fmt.Sprintf("pack %s: nothing matches %s", name, strings.Join(r.Missing, ", ")))
		}
		out = append(out, r)
	}
	if len(unknown) > 0 {
		return out, notes, fmt.Errorf("no context pack called %s", strings.Join(unknown, ", "))
	}
	return out, notes, nil
}

// SplitPlan maps pack files and docs to the worktree of the checkout at
// repoRoot in one docfs.Plan (so copies never collide), and splits the
// result back into the packs' part and the docs' part.
func SplitPlan(repoRoot string, rs []packs.Resolved, docs []string) (packPlan, docPlan []docfs.Attachment) {
	pp := packs.Paths(rs)
	if len(pp)+len(docs) == 0 {
		return nil, nil
	}
	fromPack := map[string]bool{}
	for _, p := range pp {
		fromPack[p] = true
	}
	for _, a := range docfs.Plan(repoRoot, append(slices.Clone(pp), docs...)) {
		if fromPack[a.Src] {
			packPlan = append(packPlan, a)
		} else {
			docPlan = append(docPlan, a)
		}
	}
	return packPlan, docPlan
}

// WithAttachments sets o's Docs and Prompt from prompt (rendered from tmpl,
// or typed): the packs' block first, then prompt, with the docs' mentions
// added as P4.3 does.
func WithAttachments(o agent.StartOpts, repoRoot string, rs []packs.Resolved, docs []string, tmpl templates.Template, prompt string) agent.StartOpts {
	packPlan, docPlan := SplitPlan(repoRoot, rs, docs)
	o.Docs = append(slices.Clone(packPlan), docPlan...)
	o.Prompt = packs.Prompt(rs, packPlan, DocsPrompt(tmpl, prompt, docPlan))
	return o
}

// PackMessage is what a running session gets for packs.
func PackMessage(rs []packs.Resolved, plan []docfs.Attachment) string {
	return strings.TrimRight(packs.Prompt(rs, plan, ""), "\n")
}

// --- new session --------------------------------------------------------------------

// packsResolvedMsg continues the new-session flow once the chosen packs
// are expanded.
type packsResolvedMsg struct {
	req   NewRequest
	next  func(NewRequest) tea.Cmd
	notes []string
	err   error
}

// TargetTab implements tabs.TargetedMsg.
func (packsResolvedMsg) TargetTab() string { return Title }

// resolvePacks expands req's chosen packs in a command, then runs next.
func (t *Tab) resolvePacks(req NewRequest, all []packs.Pack, next func(NewRequest) tea.Cmd) tea.Cmd {
	if len(req.Opts.Packs) == 0 {
		req.Packs = nil
		return next(req)
	}
	t.busy++
	project := t.projectNamed(req.Opts.Project)
	names := slices.Clone(req.Opts.Packs)
	return func() tea.Msg {
		rs, notes, err := ResolvePacks(all, names, project)
		req.Packs = rs
		return packsResolvedMsg{req: req, next: next, notes: notes, err: err}
	}
}

func (t *Tab) packsResolved(msg packsResolvedMsg) tea.Cmd {
	t.busy--
	var cmds []tea.Cmd
	if msg.err != nil {
		cmds = append(cmds, errCmd(msg.err))
	}
	for _, n := range msg.notes {
		cmds = append(cmds, info(n))
	}
	return tea.Batch(append(cmds, msg.next(msg.req))...)
}

// projectNamed is the configured project called name.
func (t *Tab) projectNamed(name string) config.Project {
	for _, p := range t.cfg.Projects {
		if p.Name == name {
			return p
		}
	}
	return config.Project{Name: name}
}

// withPacks sets o's Docs and Prompt from req's packs and docs.
func (t *Tab) withPacks(o agent.StartOpts, req NewRequest, tmpl templates.Template, prompt string) agent.StartOpts {
	return WithAttachments(o, t.projectPath(req.Opts.Project), req.Packs, req.Docs, tmpl, prompt)
}

// --- attach to a running session -----------------------------------------------------

// packsForMsg carries the packs to choose from for a running session.
type packsForMsg struct {
	session store.Session
	packs   []packs.Pack
	errs    []error
}

// packSentMsg reports a pack sent to a running session.
type packSentMsg struct {
	name  string // session
	pack  string
	notes []string
	err   error
}

// TargetTab implements tabs.TargetedMsg.
func (packsForMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (packSentMsg) TargetTab() string { return Title }

// attachPack asks which pack to send to the selected running session.
func (t *Tab) attachPack() tea.Cmd {
	r, ok := t.current()
	if !ok {
		return nil
	}
	if !running(r.Session) {
		return info(r.Name + " isn't running; resume it first")
	}
	dir, x := t.packsDir, r.Session
	return func() tea.Msg {
		ps, errs := LoadPacks(dir)
		return packsForMsg{session: x, packs: ps, errs: errs}
	}
}

func (t *Tab) packsFor(msg packsForMsg) tea.Cmd {
	var cmds []tea.Cmd
	if err := errors.Join(msg.errs...); err != nil {
		cmds = append(cmds, errCmd(err))
	}
	if len(msg.packs) == 0 {
		return tea.Batch(append(cmds, info("no context packs yet; make one in the Context tab's Packs view"))...)
	}
	if t.dialog != nil {
		return tea.Batch(append(cmds, info("finish the open dialog first"))...)
	}
	choice := msg.packs[0].Name
	opts := make([]huh.Option[string], len(msg.packs))
	for i, p := range msg.packs {
		opts[i] = huh.NewOption(packLabel(p), p.Name)
	}
	f := styled(t.theme, t.keys, huh.NewGroup(
		huh.NewSelect[string]().Key("pack").Title("Attach context pack to "+msg.session.Name).
			Options(opts...).Value(&choice),
	))
	all, x := msg.packs, msg.session
	open := t.openDialog(x.Name, f, func() tea.Cmd { return t.sendPack(x, all, choice) })
	return tea.Batch(append(cmds, open)...)
}

func packLabel(p packs.Pack) string {
	if p.Description == "" {
		return p.Name
	}
	return p.Name + " — " + p.Description
}

// sendPack expands the pack called name (from all, or read from the packs
// folder when all is nil) for x's project, puts its files into x's
// worktree and pastes the pack's block, in a command.
func (t *Tab) sendPack(x store.Session, all []packs.Pack, name string) tea.Cmd {
	t.busy++
	svc, project, dir := t.svc, t.projectNamed(x.Project), t.packsDir
	return func() tea.Msg {
		msg := packSentMsg{name: x.Name, pack: name}
		if all == nil {
			var errs []error
			if all, errs = LoadPacks(dir); len(all) == 0 && len(errs) > 0 {
				msg.err = errors.Join(errs...)
				return msg
			}
		}
		rs, notes, err := ResolvePacks(all, []string{name}, project)
		msg.notes = notes
		if err != nil {
			msg.err = err
			return msg
		}
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		plan, _ := SplitPlan(project.Path, rs, nil)
		docs, err := svc.AttachDocs(ctx, x, plan)
		if err != nil {
			msg.err = err
			return msg
		}
		msg.err = svc.SendPrompt(ctx, x, PackMessage(rs, docs))
		return msg
	}
}

func (t *Tab) packSent(msg packSentMsg) tea.Cmd {
	t.busy--
	var cmds []tea.Cmd
	for _, n := range msg.notes {
		cmds = append(cmds, info(n))
	}
	if msg.err != nil {
		return tea.Batch(append(cmds, errCmd(fmt.Errorf("couldn't send pack %s to %s: %w", msg.pack, msg.name, msg.err)))...)
	}
	t.pending = msg.name
	t.selectName(msg.name)
	return tea.Batch(append(cmds, success(fmt.Sprintf("sent pack %s to %s", msg.pack, msg.name)), t.liveRefresh())...)
}

// --- from the Context tab --------------------------------------------------------------

// packsPicked asks where src's packs go: a new session or a running one
// (docsPicked's twin for packs).
func (t *Tab) packsPicked(msg docsPickMsg) tea.Cmd {
	var cmds []tea.Cmd
	if msg.err != nil {
		cmds = append(cmds, info("couldn't list sessions: "+msg.err.Error()))
	}
	if len(msg.live) == 0 {
		return tea.Batch(append(cmds, t.newPackSession(msg.src))...)
	}
	if t.dialog != nil {
		return tea.Batch(append(cmds, info("finish the open dialog first"))...)
	}
	choice := newSessionChoice
	opts := []huh.Option[string]{huh.NewOption("New session…", newSessionChoice)}
	for _, x := range msg.live {
		opts = append(opts, huh.NewOption(x.Name+" · "+string(x.Status), x.Name))
	}
	label := strings.Join(msg.src.Packs, ", ")
	f := styled(t.theme, t.keys, huh.NewGroup(
		huh.NewSelect[string]().Key("to").Title("Send pack "+label+" to").Options(opts...).Value(&choice),
	))
	open := t.openDialog("", f, func() tea.Cmd {
		if choice == newSessionChoice {
			return t.newPackSession(msg.src)
		}
		i := slices.IndexFunc(msg.live, func(x store.Session) bool { return x.Name == choice })
		if i < 0 {
			return nil
		}
		return t.sendPack(msg.live[i], nil, msg.src.Packs[0])
	})
	return tea.Batch(append(cmds, open)...)
}

// newPackSession opens the new-session form with src's packs chosen.
func (t *Tab) newPackSession(src tabs.Source) tea.Cmd {
	stem := "pack"
	if len(src.Packs) > 0 {
		stem = "pack-" + src.Packs[0]
	}
	o := agent.StartOpts{
		Name:     t.uniqueName(SourceName(tabs.Source{ID: stem})),
		Project:  t.docsProject(src).Name,
		Template: t.cfg.Sessions.DefaultTemplate,
		Packs:    slices.Clone(src.Packs),
	}
	return t.openNewWith(NewRequest{Opts: o, Vars: maps.Clone(src.Vars)})
}
