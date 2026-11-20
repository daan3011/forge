package prs

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/cli/go-gh/v2/pkg/api"

	"github.com/daan3011/forge/internal/browser"
	"github.com/daan3011/forge/internal/checkout"
	"github.com/daan3011/forge/internal/clipboard"
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

// --- fakeGH's PRActor ----------------------------------------------------------

func (f *fakeGH) act(format string, args ...any) error {
	f.mu.Lock()
	f.acts = append(f.acts, fmt.Sprintf(format, args...))
	block, err := f.block, f.actErr
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	return err
}

func (f *fakeGH) actions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.acts)
}

func (f *fakeGH) PR(_ context.Context, repo string, n int) (github.PR, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.fresh[fmt.Sprintf("%s#%d", repo, n)]; ok {
		return p, nil
	}
	return github.PR{}, errors.New("no fresh copy")
}

func (f *fakeGH) UpdateBranch(_ context.Context, repo string, n int) error {
	return f.act("update %s#%d", repo, n)
}

func (f *fakeGH) Merge(_ context.Context, repo string, n int, method string) error {
	return f.act("merge %s#%d %s", repo, n, method)
}

func (f *fakeGH) EnableAutoMerge(_ context.Context, repo string, n int, method string) error {
	return f.act("automerge %s#%d %s", repo, n, method)
}

func (f *fakeGH) SetDraft(_ context.Context, repo string, n int, draft bool) error {
	return f.act("draft %s#%d %v", repo, n, draft)
}

// --- harness ---------------------------------------------------------------------

type actionRig struct {
	tb   *Tab
	gh   *fakeGH
	br   *browser.Fake
	clip *clipboard.Fake
	co   *checkout.Fake
}

func newActionRig(t *testing.T, setup ...func(*fakeGH)) *actionRig {
	t.Helper()
	r := &actionRig{gh: sampleGH(), br: &browser.Fake{}, clip: &clipboard.Fake{}, co: &checkout.Fake{}}
	for _, f := range setup {
		f(r.gh)
	}
	d := testDeps(r.gh, newKV())
	d.Browser, d.Clipboard, d.Checkout = r.br, r.clip, r.co
	d.Config.Projects = []config.Project{{Name: "tea", Repo: "acme/bubbletea", Path: "/src/bubbletea"}}
	r.tb = newTab(d)
	r.tb.now = func() time.Time { return now }
	r.tb.tick = immediateTick
	r.tb.SetSize(120, 30)
	if errs := drain(r.tb, r.tb.Init()); len(errs) > 0 {
		t.Fatal(errs)
	}
	return r
}

// toasts is what the root model would show.
type toasts struct{ info, success, errs []string }

// feed runs cmd, feeding messages back into the tab, and collects toasts
// and messages for other tabs (StartSessionMsg).
func (r *actionRig) feed(cmd tea.Cmd) (toasts, []tea.Msg) {
	var out toasts
	var other []tea.Msg
	queue := run(cmd)
	for len(queue) > 0 {
		msg := queue[0]
		queue = queue[1:]
		switch m := msg.(type) {
		case ui.ErrMsg:
			out.errs = append(out.errs, m.Err.Error())
			continue
		case ui.InfoMsg:
			out.info = append(out.info, m.Text)
			continue
		case ui.SuccessMsg:
			out.success = append(out.success, m.Text)
			continue
		case tabs.StartSessionMsg:
			other = append(other, m)
			continue
		}
		_, next := r.tb.Update(msg)
		queue = append(queue, run(next)...)
	}
	return out, other
}

func (r *actionRig) keys(ks ...string) (toasts, []tea.Msg) {
	var all toasts
	var other []tea.Msg
	for _, k := range ks {
		_, cmd := r.tb.Update(press(k))
		t, o := r.feed(cmd)
		all.info = append(all.info, t.info...)
		all.success = append(all.success, t.success...)
		all.errs = append(all.errs, t.errs...)
		other = append(other, o...)
	}
	return all, other
}

// selectPR moves the cursor to id in the current section.
func (r *actionRig) selectPR(t *testing.T, id string) {
	t.Helper()
	r.tb.selectID(id)
	if p, ok := r.tb.selected(); !ok || p.ID() != id {
		t.Fatalf("can't select %s", id)
	}
}

// --- tests -----------------------------------------------------------------------

func TestOpenAndCopy(t *testing.T) {
	r := newActionRig(t)
	if got, _ := r.keys("o"); len(got.errs) > 0 {
		t.Fatal(got.errs)
	}
	if want := []string{"https://github.com/acme/bubbletea/pull/1"}; !slices.Equal(r.br.Opened(), want) {
		t.Errorf("opened = %v", r.br.Opened())
	}
	got, _ := r.keys("y")
	if want := []string{"https://github.com/acme/bubbletea/pull/1"}; !slices.Equal(r.clip.Copied(), want) {
		t.Errorf("copied = %v", r.clip.Copied())
	}
	if len(got.info) != 1 || !strings.Contains(got.info[0], "copied") {
		t.Errorf("toasts = %+v", got)
	}

	r.br.Err = errors.New("no browser")
	if got, _ := r.keys("o"); len(got.errs) != 1 || !strings.Contains(got.errs[0], "open #1") {
		t.Errorf("open failure toasts = %+v", got)
	}
}

func TestCopyFallsBackToOSC52(t *testing.T) {
	r := newActionRig(t)
	r.clip.Err = clipboard.ErrNoTool
	got, _ := r.keys("y")
	if len(got.info) != 1 || got.info[0] != "copied https://github.com/acme/bubbletea/pull/1" || len(got.errs) > 0 {
		t.Errorf("toasts = %+v", got)
	}
}

func TestToggleDraftRefreshesRow(t *testing.T) {
	r := newActionRig(t)
	fresh := pr("acme/bubbletea", 1, "feat(examples): tree", func(p *github.PR) {
		p.Draft, p.State, p.UpdatedAt = true, github.StateDraft, now
	})
	r.gh.fresh = map[string]github.PR{"acme/bubbletea#1": fresh}
	got, _ := r.keys("d")
	if want := []string{"draft acme/bubbletea#1 true"}; !slices.Equal(r.gh.actions(), want) {
		t.Fatalf("actions = %v", r.gh.actions())
	}
	if !slices.Equal(got.success, []string{"#1 is a draft"}) || len(got.errs) > 0 {
		t.Errorf("toasts = %+v", got)
	}
	if p, _ := r.tb.selected(); p.State != github.StateDraft {
		t.Errorf("row not refreshed: %+v", p)
	}
	if r.tb.Busy() != "" {
		t.Errorf("still busy: %q", r.tb.Busy())
	}

	// A lagging search with the old copy doesn't undo the patch...
	drain(r.tb, r.tb.Refresh())
	if p, _ := r.tb.selected(); p.State != github.StateDraft {
		t.Errorf("stale search undid the patch: %+v", p)
	}
	// ...and a newer one replaces it.
	newer := fresh
	newer.Title, newer.UpdatedAt = "renamed", now.Add(time.Minute)
	res := r.gh.results["author:@me"]
	res.PRs = slices.Clone(res.PRs)
	res.PRs[0] = newer
	r.gh.results["author:@me"] = res
	drain(r.tb, r.tb.Refresh())
	if p, _ := r.tb.selected(); p.Title != "renamed" {
		t.Errorf("newer search ignored: %+v", p)
	}
	if len(r.tb.patched) != 0 {
		t.Errorf("patch kept: %v", r.tb.patched)
	}

	// Back to ready.
	r.keys("d")
	if got := r.gh.actions(); got[len(got)-1] != "draft acme/bubbletea#1 false" {
		t.Errorf("actions = %v", got)
	}
}

func TestActionErrorsAreReadable(t *testing.T) {
	r := newActionRig(t)
	u, _ := url.Parse("https://api.github.com/repos/acme/bubbletea/pulls/1/update-branch")
	r.gh.actErr = fmt.Errorf("github: acme/bubbletea#1: %w", &api.HTTPError{
		StatusCode: 422, Message: "merge conflict between base and head", RequestURL: u,
	})
	got, _ := r.keys("u")
	if want := []string{"update branch of #1 failed: merge conflict between base and head"}; !slices.Equal(got.errs, want) {
		t.Errorf("errs = %v, want %v", got.errs, want)
	}
	if r.tb.Busy() != "" {
		t.Errorf("still busy after failure: %q", r.tb.Busy())
	}
}

func TestBusyWhileRunning(t *testing.T) {
	r := newActionRig(t)
	r.gh.block = make(chan struct{})
	_, cmd := r.tb.Update(press("u"))
	if got := r.tb.Busy(); got != "updating #1" {
		t.Fatalf("Busy = %q", got)
	}
	// A second action on the same PR is refused while the first runs.
	_, again := r.tb.Update(press("d"))
	if got, _ := r.feed(again); !slices.Equal(got.info, []string{"still updating #1"}) {
		t.Errorf("second action toasts = %+v", got)
	}
	done := make(chan toasts)
	go func() { got, _ := r.feed(cmd); done <- got }()
	close(r.gh.block)
	if got := <-done; !slices.Equal(got.success, []string{"updated #1 with main"}) {
		t.Errorf("toasts = %+v", got)
	}
	if r.tb.Busy() != "" {
		t.Errorf("Busy = %q after done", r.tb.Busy())
	}
}

func TestGuards(t *testing.T) {
	r := newActionRig(t)
	merged := pr("acme/bubbles", 2, "feat: tree bubble", func(p *github.PR) { p.State = github.StateMerged })
	res := r.gh.results["author:@me"]
	res.PRs = []github.PR{res.PRs[0], merged}
	r.gh.results["author:@me"] = res
	drain(r.tb, r.tb.Refresh())
	r.selectPR(t, "acme/bubbles#2")
	for _, k := range []string{"u", "m", "M", "d"} {
		got, _ := r.keys(k)
		if !slices.Equal(got.info, []string{"#2 is merged"}) {
			t.Errorf("%s on merged PR: toasts = %+v", k, got)
		}
	}
	if len(r.gh.actions()) != 0 || r.tb.dialog != nil {
		t.Errorf("guarded actions ran: %v", r.gh.actions())
	}

	// Merging a draft asks to mark it ready first.
	r.tb.switchSection(2) // involves:@me, a draft first
	r.selectPR(t, "acme/bubbles#9")
	if got, _ := r.keys("m"); len(got.info) != 1 || !strings.Contains(got.info[0], "mark it ready first (d)") {
		t.Errorf("draft merge toasts = %+v", got)
	}
}

func TestMergeDialog(t *testing.T) {
	// The repo allows squash and rebase only.
	r := newActionRig(t, func(gh *fakeGH) {
		gh.details = map[string]github.PRDetail{"acme/bubbletea#1": {MergeMethods: []string{github.MergeMethodSquash, github.MergeMethodRebase}}}
	})

	r.keys("m")
	if r.tb.dialog == nil {
		t.Fatal("no merge dialog")
	}
	v := view(r.tb)
	mustContain(t, v, "Squash and merge", "Rebase and merge", "checks are failing")
	mustLack(t, v, "Create a merge commit")
	if !r.tb.CapturingInput() {
		t.Error("dialog doesn't capture input")
	}

	// Pick rebase, then decline: nothing merges.
	r.keys("down", "enter", "enter")
	if r.tb.dialog != nil || len(r.gh.actions()) != 0 {
		t.Fatalf("declined merge: dialog %v, actions %v", r.tb.dialog != nil, r.gh.actions())
	}

	// Again: rebase, confirm (left picks "Merge").
	r.tb.Update(press("m"))
	r.keys("down", "enter")
	got, _ := r.keys("left", "enter")
	if want := []string{"merge acme/bubbletea#1 rebase"}; !slices.Equal(r.gh.actions(), want) {
		t.Fatalf("actions = %v", r.gh.actions())
	}
	if !slices.Equal(got.success, []string{"merged #1 (rebase)"}) {
		t.Errorf("toasts = %+v", got)
	}
	// The method used is offered first next time.
	if m := r.tb.mergeMethods(r.gh.results["author:@me"].PRs[0]); m[0] != github.MergeMethodRebase {
		t.Errorf("methods = %v", m)
	}

	// Esc aborts.
	r.keys("m", "esc")
	if r.tb.dialog != nil || len(r.gh.actions()) != 1 {
		t.Errorf("esc: dialog %v, actions %v", r.tb.dialog != nil, r.gh.actions())
	}
}

func TestAutoMerge(t *testing.T) {
	r := newActionRig(t)
	r.keys("M")
	if r.tb.dialog == nil {
		t.Fatal("no method dialog")
	}
	got, _ := r.keys("down", "enter") // squash
	if want := []string{"automerge acme/bubbletea#1 squash"}; !slices.Equal(r.gh.actions(), want) {
		t.Fatalf("actions = %v", r.gh.actions())
	}
	if !slices.Equal(got.success, []string{"auto-merge on for #1 (squash)"}) {
		t.Errorf("toasts = %+v", got)
	}
}

func TestCheckout(t *testing.T) {
	r := newActionRig(t)
	r.co.Result = checkout.Result{Path: "/wt/bubbletea/pr-1", Branch: "feat-1", Worktree: true}
	got, _ := r.keys("c")
	calls := r.co.Calls()
	if len(calls) != 1 || calls[0].Project.Path != "/src/bubbletea" || calls[0].Branch != "feat-1" ||
		calls[0].Base != "main" || calls[0].Number != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	if len(got.success) != 1 || !strings.Contains(got.success[0], "worktree at /wt/bubbletea/pr-1") {
		t.Errorf("toasts = %+v", got)
	}

	// No project for the repo.
	r.selectPR(t, "acme/lipgloss#3")
	if got, _ := r.keys("c"); len(got.errs) != 1 || !strings.Contains(got.errs[0], "no project with a path for acme/lipgloss") {
		t.Errorf("toasts = %+v", got)
	}

	// git fails.
	r.selectPR(t, "acme/bubbletea#1")
	r.co.Err = errors.New("git checkout: your local changes would be overwritten")
	if got, _ := r.keys("c"); len(got.errs) != 1 || got.errs[0] != "check out #1 failed: git checkout: your local changes would be overwritten" {
		t.Errorf("toasts = %+v", got)
	}
}

func TestStartSession(t *testing.T) {
	r := newActionRig(t)
	_, other := r.keys("s")
	if len(other) != 1 {
		t.Fatalf("messages = %v", other)
	}
	src := other[0].(tabs.StartSessionMsg).Source
	want := tabs.Source{
		Kind: store.KindGitHubPR, ID: "acme/bubbletea#1", Number: 1, URL: "https://github.com/acme/bubbletea/pull/1",
		Title: "feat(examples): tree", State: github.StateOpen, Repo: "acme/bubbletea", Base: "main", Branch: "feat-1",
	}
	if fmt.Sprint(src) != fmt.Sprint(want) {
		t.Errorf("source = %+v\nwant %+v", src, want)
	}
}

func TestActionsArePaletteCommands(t *testing.T) {
	r := newActionRig(t)
	byID := map[string]tabs.Command{}
	for _, c := range r.tb.Commands() {
		byID[c.ID] = c
	}
	pk := r.tb.keys.PRs
	for id, want := range map[string]string{
		"pr_open": pk.Open.Help().Key, "pr_copy_url": pk.CopyURL.Help().Key, "pr_checkout": pk.Checkout.Help().Key,
		"pr_update_branch": pk.UpdateBranch.Help().Key, "pr_merge": pk.Merge.Help().Key, "pr_auto_merge": pk.AutoMerge.Help().Key,
		"pr_toggle_draft": pk.ToggleDraft.Help().Key, "pr_session": pk.Session.Help().Key,
	} {
		c, ok := byID[id]
		if !ok {
			t.Errorf("no palette command %s", id)
			continue
		}
		if c.Key.Help().Key != want {
			t.Errorf("%s key = %q, want %q", id, c.Key.Help().Key, want)
		}
	}

	// Running one does the same as the key.
	r.feed(byID["pr_update_branch"].Run())
	if want := []string{"update acme/bubbletea#1"}; !slices.Equal(r.gh.actions(), want) {
		t.Errorf("actions = %v", r.gh.actions())
	}
	r.feed(byID["pr_merge"].Run())
	if r.tb.dialog == nil {
		t.Error("palette merge opened no dialog")
	}
}
