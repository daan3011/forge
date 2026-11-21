package prs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/daan3011/forge/internal/checkout"
	"github.com/daan3011/forge/internal/clipboard"
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

// actionTimeout bounds one quick action, including the row refresh after.
// A checkout may fetch a big branch, so it is generous.
const actionTimeout = 2 * time.Minute

// actionDoneMsg is the result of a quick action on a PR (P2.3).
type actionDoneMsg struct {
	id     string // PR id
	verb   string // for the failure toast: "merge #12 failed: …"
	number int
	text   string     // success toast
	fresh  *github.PR // the PR re-fetched after the action, to patch its row
	err    error
}

// clipboardFallbackMsg asks for the URL to be copied with OSC 52 because no
// copy tool was found.
type clipboardFallbackMsg struct{ text string }

// openedMsg reports a failed browser open; success needs no message.
type openedMsg struct{ err error }

func (actionDoneMsg) TargetTab() string        { return Title }
func (clipboardFallbackMsg) TargetTab() string { return Title }
func (openedMsg) TargetTab() string            { return Title }

func info(s string) tea.Cmd { return func() tea.Msg { return ui.InfoMsg{Text: s} } }

// updateActions handles the quick actions' messages.
func (t *Tab) updateActions(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case actionDoneMsg:
		return t.actionDone(msg), true
	case fixGatheredMsg:
		return t.fixGathered(msg), true
	case clipboardFallbackMsg:
		return tea.Batch(tea.SetClipboard(msg.text), info("copied "+msg.text)), true
	case openedMsg:
		if msg.err != nil {
			return errCmd(msg.err), true
		}
		return nil, true
	}
	return nil, false
}

// runAction runs do for p in the background, marking p busy with activity
// ("merging #12") until it is done. With refresh the PR is re-fetched after
// a success and its row patched.
func (t *Tab) runAction(p github.PR, activity, verb string, refresh bool, do func(ctx context.Context) (string, error)) tea.Cmd {
	id := p.ID()
	if b, ok := t.busy[id]; ok {
		return info("still " + b)
	}
	t.busy[id] = activity
	gh := t.gh
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		msg := actionDoneMsg{id: id, verb: verb, number: p.Number}
		msg.text, msg.err = do(ctx)
		if msg.err == nil && refresh && gh != nil {
			// A failed refresh only leaves the row as it was; the next
			// search catches up.
			if fresh, err := gh.PR(ctx, p.Repo, p.Number); err == nil {
				msg.fresh = &fresh
			}
		}
		return msg
	}
}

func (t *Tab) actionDone(msg actionDoneMsg) tea.Cmd {
	delete(t.busy, msg.id)
	if msg.err != nil {
		return errCmd(github.ActionError(msg.verb, msg.number, msg.err))
	}
	if msg.fresh != nil {
		t.patchPR(*msg.fresh)
	}
	text := msg.text
	return func() tea.Msg { return ui.SuccessMsg{Text: text} }
}

// patchPR replaces p's row in every section with the fresh copy. Searches
// lag behind changes, so the copy also overrides older search results
// until a search returns the PR at least as new.
func (t *Tab) patchPR(p github.PR) {
	t.patched[p.ID()] = p
	if t.diff != nil && t.diff.pr.ID() == p.ID() {
		t.diff.pr = p
	}
	for _, s := range t.sections {
		if t.applyPatches(&s.res) {
			t.rowsChanged(s)
		}
	}
}

// applyPatches swaps patched PRs into res and forgets patches the search
// has caught up with. It reports whether res changed.
func (t *Tab) applyPatches(res *github.SearchResult) bool {
	changed := false
	for i, p := range res.PRs {
		f, ok := t.patched[p.ID()]
		if !ok {
			continue
		}
		if !f.UpdatedAt.After(p.UpdatedAt) {
			delete(t.patched, p.ID())
			continue
		}
		if res.PRs[i] != f {
			res.PRs[i], changed = f, true
		}
	}
	return changed
}

// Busy implements tabs.Busier: the selected PR's running action, else the
// first one, with how many more run.
func (t *Tab) Busy() string {
	if len(t.busy) == 0 {
		return ""
	}
	ids := make([]string, 0, len(t.busy))
	for id := range t.busy {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	first := ids[0]
	if p, ok := t.selected(); ok {
		if _, busy := t.busy[p.ID()]; busy {
			first = p.ID()
		}
	}
	text := t.busy[first]
	if n := len(ids) - 1; n > 0 {
		text += fmt.Sprintf(" (+%d)", n)
	}
	return text
}

// --- guards ------------------------------------------------------------------

// openTarget is the selected PR when it is still open (or a draft); else
// it explains why not.
func (t *Tab) openTarget() (github.PR, tea.Cmd, bool) {
	p, ok := t.selected()
	if !ok {
		return github.PR{}, nil, false
	}
	if t.gh == nil {
		return github.PR{}, errCmd(errors.New("GitHub isn't available")), false
	}
	if p.State == github.StateMerged || p.State == github.StateClosed {
		return github.PR{}, info(fmt.Sprintf("#%d is %s", p.Number, p.State)), false
	}
	return p, nil, true
}

// --- the actions ---------------------------------------------------------------

func (t *Tab) openInBrowser() tea.Cmd {
	p, ok := t.selected()
	if !ok {
		return nil
	}
	if t.browser == nil {
		return errCmd(errors.New("can't open a browser here"))
	}
	b, url := t.browser, prURL(p)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := b.Open(ctx, url); err != nil {
			return openedMsg{err: fmt.Errorf("open #%d: %w", p.Number, err)}
		}
		return openedMsg{}
	}
}

func prURL(p github.PR) string {
	if p.URL != "" {
		return p.URL
	}
	return github.PullURL(p.Repo, p.Number)
}

func (t *Tab) copyURL() tea.Cmd {
	p, ok := t.selected()
	if !ok {
		return nil
	}
	url := prURL(p)
	if t.clip == nil {
		return func() tea.Msg { return clipboardFallbackMsg{text: url} }
	}
	c := t.clip
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := c.Copy(ctx, url)
		switch {
		case errors.Is(err, clipboard.ErrNoTool):
			return clipboardFallbackMsg{text: url}
		case err != nil:
			return ui.ErrMsg{Err: fmt.Errorf("copy URL of #%d: %w", p.Number, err)}
		}
		return ui.InfoMsg{Text: "copied " + url}
	}
}

// projectFor is the configured project with a checkout for repo, the
// active one first.
func (t *Tab) projectFor(repo string) (config.Project, bool) {
	if t.project != nil && strings.EqualFold(t.project.Repo, repo) && t.project.Path != "" {
		return *t.project, true
	}
	for _, p := range t.cfg.Projects {
		if strings.EqualFold(p.Repo, repo) && p.Path != "" {
			return p, true
		}
	}
	return config.Project{}, false
}

func (t *Tab) checkoutPR() tea.Cmd {
	p, ok := t.selected()
	if !ok {
		return nil
	}
	if t.checkout == nil {
		return errCmd(errors.New("checking out isn't available"))
	}
	proj, ok := t.projectFor(p.Repo)
	if !ok {
		return errCmd(fmt.Errorf("check out #%d: no project with a path for %s in the config", p.Number, p.Repo))
	}
	if p.HeadRef == "" && !p.CrossRepo {
		return errCmd(fmt.Errorf("check out #%d: its branch is unknown; refresh first", p.Number))
	}
	c := t.checkout
	opts := checkout.Opts{Project: proj, Number: p.Number, Branch: p.HeadRef, Base: p.BaseRef, CrossRepo: p.CrossRepo}
	return t.runAction(p, fmt.Sprintf("checking out #%d", p.Number), "check out", false, func(ctx context.Context) (string, error) {
		res, err := c.Checkout(ctx, opts)
		if err != nil {
			return "", err
		}
		var text string
		switch {
		case res.Existing:
			text = fmt.Sprintf("%s is already checked out at %s", res.Branch, res.Path)
		case res.Worktree:
			text = fmt.Sprintf("checkout has changes; %s is in a worktree at %s", res.Branch, res.Path)
		default:
			text = fmt.Sprintf("checked out %s in %s", res.Branch, res.Path)
		}
		if res.FetchErr != nil {
			text += " (couldn't fetch; local branch may be behind)"
		}
		return text, nil
	})
}

func (t *Tab) updateBranch() tea.Cmd {
	p, cmd, ok := t.openTarget()
	if !ok {
		return cmd
	}
	gh := t.gh
	return t.runAction(p, fmt.Sprintf("updating #%d", p.Number), "update branch of", true, func(ctx context.Context) (string, error) {
		if err := gh.UpdateBranch(ctx, p.Repo, p.Number); err != nil {
			return "", err
		}
		return fmt.Sprintf("updated #%d with %s", p.Number, orBase(p.BaseRef)), nil
	})
}

func orBase(base string) string {
	if base == "" {
		return "its base"
	}
	return base
}

func (t *Tab) toggleDraft() tea.Cmd {
	p, cmd, ok := t.openTarget()
	if !ok {
		return cmd
	}
	gh, draft := t.gh, !p.Draft
	activity, done := fmt.Sprintf("marking #%d ready", p.Number), fmt.Sprintf("#%d is ready for review", p.Number)
	if draft {
		activity, done = fmt.Sprintf("converting #%d to draft", p.Number), fmt.Sprintf("#%d is a draft", p.Number)
	}
	verb := "mark ready"
	if draft {
		verb = "convert to draft"
	}
	return t.runAction(p, activity, verb, true, func(ctx context.Context) (string, error) {
		return done, gh.SetDraft(ctx, p.Repo, p.Number, draft)
	})
}

// mergeMethods are the methods offered for p: the repo's allowed ones when
// its detail is loaded, else all; the last one used first.
func (t *Tab) mergeMethods(p github.PR) []string {
	methods := github.MergeMethods
	if e, _ := t.det.entry(p); e != nil && len(e.d.MergeMethods) > 0 {
		methods = e.d.MergeMethods
	}
	methods = slices.Clone(methods)
	if i := slices.Index(methods, t.lastMethod); i > 0 {
		methods = append([]string{t.lastMethod}, slices.Delete(methods, i, i+1)...)
	}
	return methods
}

var methodTitles = map[string]string{
	github.MergeMethodMerge:  "Create a merge commit",
	github.MergeMethodSquash: "Squash and merge",
	github.MergeMethodRebase: "Rebase and merge",
}

func methodOptions(methods []string) []huh.Option[string] {
	opts := make([]huh.Option[string], len(methods))
	for i, m := range methods {
		opts[i] = huh.NewOption(methodTitles[m], m)
	}
	return opts
}

// openMerge asks for the merge method (when there is a choice) and a
// confirmation, then merges.
func (t *Tab) openMerge() tea.Cmd {
	p, cmd, ok := t.openTarget()
	if !ok {
		return cmd
	}
	if p.Draft {
		return info(fmt.Sprintf("#%d is a draft; mark it ready first (%s)", p.Number, t.keys.PRs.ToggleDraft.Help().Key))
	}
	methods := t.mergeMethods(p)
	method, confirmed := methods[0], false
	var fields []huh.Field
	if len(methods) > 1 {
		fields = append(fields, huh.NewSelect[string]().Key("method").
			Title(fmt.Sprintf("Merge #%d %s", p.Number, p.Title)).
			Options(methodOptions(methods)...).Value(&method))
	}
	fields = append(fields, huh.NewConfirm().Key("confirm").
		// A static title: huh evaluates TitleFunc in a command goroutine,
		// which would race with the select writing method.
		Title(fmt.Sprintf("Merge #%d into %s?", p.Number, orBase(p.BaseRef))).
		Description(mergeWarning(p)).
		Affirmative("Merge").Negative("Cancel").Value(&confirmed))
	return t.openDialog(huh.NewForm(huh.NewGroup(fields...)), func() tea.Cmd {
		if !confirmed {
			return nil
		}
		return t.merge(p, method)
	})
}

// mergeWarning is what the row knows against merging now.
func mergeWarning(p github.PR) string {
	var w []string
	switch p.CI {
	case github.CIFailure, github.CIError:
		w = append(w, "checks are failing")
	case github.CIPending, github.CIExpected:
		w = append(w, "checks are still running")
	}
	if p.Mergeable == github.Conflicting {
		w = append(w, "it has conflicts")
	}
	if p.ReviewDecision == github.ReviewChangesRequested {
		w = append(w, "changes were requested")
	}
	if len(w) == 0 {
		return ""
	}
	return "note: " + strings.Join(w, ", ")
}

func (t *Tab) merge(p github.PR, method string) tea.Cmd {
	t.lastMethod = method
	gh := t.gh
	return t.runAction(p, fmt.Sprintf("merging #%d", p.Number), "merge", true, func(ctx context.Context) (string, error) {
		if err := gh.Merge(ctx, p.Repo, p.Number, method); err != nil {
			return "", err
		}
		return fmt.Sprintf("merged #%d (%s)", p.Number, method), nil
	})
}

// openAutoMerge asks for the merge method, then enables auto-merge.
func (t *Tab) openAutoMerge() tea.Cmd {
	p, cmd, ok := t.openTarget()
	if !ok {
		return cmd
	}
	methods := t.mergeMethods(p)
	if len(methods) == 1 {
		return t.autoMerge(p, methods[0])
	}
	method := methods[0]
	form := huh.NewForm(huh.NewGroup(huh.NewSelect[string]().Key("method").
		Title(fmt.Sprintf("Auto-merge #%d %s", p.Number, p.Title)).
		Description("merges once the required reviews and checks pass").
		Options(methodOptions(methods)...).Value(&method)))
	return t.openDialog(form, func() tea.Cmd { return t.autoMerge(p, method) })
}

func (t *Tab) autoMerge(p github.PR, method string) tea.Cmd {
	t.lastMethod = method
	gh := t.gh
	return t.runAction(p, fmt.Sprintf("enabling auto-merge for #%d", p.Number), "enable auto-merge for", true, func(ctx context.Context) (string, error) {
		if err := gh.EnableAutoMerge(ctx, p.Repo, p.Number, method); err != nil {
			return "", err
		}
		return fmt.Sprintf("auto-merge on for #%d (%s)", p.Number, method), nil
	})
}

// startSession opens the Sessions tab's new-session form for the PR (P1.9).
func (t *Tab) startSession() tea.Cmd {
	p, ok := t.selected()
	if !ok {
		return nil
	}
	return tabs.StartSessionFrom(tabs.Source{
		Kind: store.KindGitHubPR, ID: p.ID(), Number: p.Number, URL: prURL(p),
		Title: p.Title, State: p.State, Repo: p.Repo, Base: p.BaseRef, Branch: p.HeadRef,
	})
}

// showRuns jumps to the Pipelines tab with the PR head branch's workflow
// runs (P2.6). Runs of fork PRs live in the base repo under the fork's
// branch name, so the PR's own repo is the one to list.
func (t *Tab) showRuns() tea.Cmd {
	p, ok := t.selected()
	if !ok {
		return nil
	}
	if p.HeadRef == "" {
		return errCmd(fmt.Errorf("#%d has no head branch", p.Number))
	}
	return tabs.ShowRuns(p.Repo, p.HeadRef)
}
