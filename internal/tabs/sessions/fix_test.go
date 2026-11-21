package sessions

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/cifix"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/templates"
)

var fixVars = map[string]string{
	cifix.VarWorkflow:      "CI",
	cifix.VarJob:           "test",
	cifix.VarRun:           "https://github.com/daan/forge/actions/runs/77",
	cifix.VarCommitMessage: "Speed up the parser",
	cifix.VarFailingLog:    "── CI › test ──\n--- FAIL: TestParse",
}

func fixSource(fix tabs.FixRequest) tabs.Source {
	return tabs.Source{
		Kind: store.KindGitHubPR, ID: "daan/forge#12", Number: 12, Title: "Speed up", State: "open",
		Repo: "daan/forge", Base: "main", Branch: "feature/fast", Template: cifix.Template,
		Vars: fixVars, Fix: &fix,
	}
}

// fixTab is a loaded template tab (real default templates on disk).
func fixTab(t *testing.T, svc *fakeSvc) *Tab {
	t.Helper()
	tab := newTemplateTab(t, svc, templates.NewRegistry())
	pump(tab, tab.Refresh())
	return tab
}

func prLink(n int) store.Link {
	return store.Link{Other: store.PRRef("daan/forge", n), LinkMeta: store.LinkMeta{State: "open"}}
}

func TestFixImmediateStartsSession(t *testing.T) {
	svc := newFakeSvc()
	tab := fixTab(t, svc)
	toasts, _ := pump(tab, tab.StartSession(fixSource(tabs.FixRequest{Reuse: true, Immediate: true})))
	if !slices.Contains(toasts, "ok: fix session pr-12 started") {
		t.Errorf("toasts = %q", toasts)
	}
	if tab.dialog != nil {
		t.Error("a form opened for a complete fix request")
	}
	starts, _, _ := svc.snapshot()
	if len(starts) != 1 {
		t.Fatalf("starts = %+v", starts)
	}
	o := starts[0]
	if o.Name != "pr-12" || o.Branch != "feature/fast" || o.Template != "fix-ci" || o.Base != "main" {
		t.Errorf("opts = %+v", o)
	}
	for _, want := range []string{
		"CI is failing for daan/forge on branch feature/fast", "Workflow: CI", "Job: test",
		"Last commit: Speed up the parser", "--- FAIL: TestParse", "actions/runs/77",
	} {
		if !strings.Contains(o.Prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, o.Prompt)
		}
	}
	if len(o.Links) != 1 || o.Links[0].To != store.PRRef("daan/forge", 12) {
		t.Errorf("links = %+v", o.Links)
	}
}

func TestFixReusesLiveLinkedSession(t *testing.T) {
	old := linkedRow("pr-12", prLink(12))
	old.Status = store.StatusDead // an older, dead one is skipped
	live := linkedRow("pr-12-2", prLink(12))
	live.Status = store.StatusWaiting
	other := linkedRow("pr-13", prLink(13))
	svc := newFakeSvc(old, live, other)
	tab := fixTab(t, svc)

	toasts, _ := pump(tab, tab.StartSession(fixSource(tabs.FixRequest{Reuse: true, Immediate: true})))
	if !slices.Contains(toasts, "ok: sent the failure to pr-12-2") {
		t.Errorf("toasts = %q", toasts)
	}
	starts, _, _ := svc.snapshot()
	if len(starts) != 0 {
		t.Errorf("started %+v instead of reusing", starts)
	}
	svc.mu.Lock()
	sent := svc.sent["pr-12-2"]
	svc.mu.Unlock()
	if len(sent) != 1 || !strings.Contains(sent[0], "--- FAIL: TestParse") || !strings.Contains(sent[0], "on branch agent/pr-12-2") {
		t.Errorf("sent = %q", sent)
	}
	if r, ok := tab.current(); !ok || r.Name != "pr-12-2" {
		t.Errorf("selected = %v", r.Name)
	}
}

func TestFixFallsBackWhenSendFails(t *testing.T) {
	live := linkedRow("pr-12", prLink(12))
	svc := newFakeSvc(live)
	svc.sendErr = agent.ErrNoWindow
	tab := fixTab(t, svc)
	toasts, _ := pump(tab, tab.StartSession(fixSource(tabs.FixRequest{Reuse: true, Immediate: true})))
	if !slices.ContainsFunc(toasts, func(s string) bool { return strings.HasPrefix(s, "info: couldn't send to pr-12") }) ||
		!slices.Contains(toasts, "ok: fix session pr-12-2 started") {
		t.Errorf("toasts = %q", toasts)
	}
}

func TestFixDeadLinkedSessionStartsNew(t *testing.T) {
	dead := linkedRow("pr-12", prLink(12))
	dead.Status = store.StatusDead
	svc := newFakeSvc(dead)
	tab := fixTab(t, svc)
	toasts, _ := pump(tab, tab.StartSession(fixSource(tabs.FixRequest{Reuse: true, Immediate: true})))
	if !slices.Contains(toasts, "ok: fix session pr-12-2 started") {
		t.Errorf("toasts = %q", toasts)
	}
	if len(svc.sent) != 0 {
		t.Errorf("sent to a dead session: %v", svc.sent)
	}
}

func TestFixEditOpensForm(t *testing.T) {
	svc := newFakeSvc()
	tab := fixTab(t, svc)
	// alt+f: Reuse without Immediate, nothing linked → the pre-filled form.
	pump(tab, tab.StartSession(fixSource(tabs.FixRequest{Reuse: true})))
	if tab.dialog == nil {
		t.Fatal("no form")
	}
	if !strings.Contains(tab.View(), "pr-12") {
		t.Errorf("form not pre-filled:\n%s", tab.View())
	}
	starts, _, _ := svc.snapshot()
	if len(starts) != 0 {
		t.Errorf("started without the form: %+v", starts)
	}
}

func TestFixEditThenSendToLiveSession(t *testing.T) {
	live := linkedRow("pr-12", prLink(12))
	svc := newFakeSvc(live)
	tab := fixTab(t, svc)
	pumpForm(tab, tab.StartSession(fixSource(tabs.FixRequest{Reuse: true})))
	if tab.dialog == nil || !strings.Contains(ansi.Strip(tab.View()), "--- FAIL: TestParse") {
		t.Fatalf("no editable prompt:\n%s", ansi.Strip(tab.View()))
	}
	if len(svc.sent) != 0 {
		t.Fatal("sent before editing")
	}
	if !strings.Contains(ansi.Strip(tab.View()), "Send to pr-12") {
		t.Errorf("dialog title:\n%s", ansi.Strip(tab.View()))
	}
	// Submitting the form (huh's textarea is covered by the template flow
	// tests) runs done with the edited prompt.
	d := tab.dialog
	tab.dialog = nil
	toasts, _ := pump(tab, d.done())
	if !slices.Contains(toasts, "ok: sent the failure to pr-12") {
		t.Errorf("toasts = %q", toasts)
	}
	svc.mu.Lock()
	sent := svc.sent["pr-12"]
	svc.mu.Unlock()
	if len(sent) != 1 || !strings.Contains(sent[0], "--- FAIL: TestParse") {
		t.Errorf("sent = %q", sent)
	}
}

func TestFixMissingVarOpensForm(t *testing.T) {
	svc := newFakeSvc()
	tab := fixTab(t, svc)
	src := fixSource(tabs.FixRequest{Immediate: true})
	src.Vars = map[string]string{cifix.VarFailingLog: "boom"} // workflow etc. unknown
	pump(tab, tab.StartSession(src))
	if tab.dialog == nil {
		t.Fatal("no form for missing variables")
	}
	starts, _, _ := svc.snapshot()
	if len(starts) != 0 {
		t.Errorf("started: %+v", starts)
	}
}

func TestFixUnknownTemplateOpensForm(t *testing.T) {
	svc := newFakeSvc()
	tab := fixTab(t, svc)
	src := fixSource(tabs.FixRequest{Immediate: true})
	src.Template = "gone"
	toasts, _ := pump(tab, tab.StartSession(src))
	if !slices.Contains(toasts, "info: template gone not found; pick one") || tab.dialog == nil {
		t.Errorf("toasts = %q, dialog %v", toasts, tab.dialog != nil)
	}
}

// runSource is a run without a PR: a confirm, then a fix branch at the
// failing commit, linked to the run.
func runSource() tabs.Source {
	return tabs.Source{
		Kind: store.KindGitHubRun, ID: "daan/forge#77", URL: "https://github.com/daan/forge/actions/runs/77",
		Title: "CI #12", Repo: "daan/forge", Base: "main", Branch: "fix/ci-77", Template: cifix.Template,
		Vars: fixVars,
		Fix: &tabs.FixRequest{
			Immediate: true, StartCommit: "abc1234def",
			Confirm: "No open PR for main. Start a fix session on a new branch fix/ci-77 from abc1234?",
		},
	}
}

// pumpForm is pump that also feeds the form's own messages (focus, field
// init) back into the tab, for a bounded number of rounds.
func pumpForm(tab *Tab, cmd tea.Cmd) []string {
	var toasts []string
	for range 20 {
		got, other := pump(tab, cmd)
		toasts = append(toasts, got...)
		if len(other) == 0 {
			break
		}
		var next []tea.Cmd
		for _, m := range other {
			if _, ok := m.(tea.BatchMsg); ok {
				continue
			}
			_, c := tab.Update(m)
			next = append(next, c)
		}
		cmd = tea.Batch(next...)
	}
	return toasts
}

func answer(tab *Tab, keys ...tea.KeyPressMsg) []string {
	var toasts []string
	for _, k := range keys {
		_, cmd := tab.Update(k)
		toasts = append(toasts, pumpForm(tab, cmd)...)
	}
	return toasts
}

func TestFixRunWithoutPRConfirms(t *testing.T) {
	svc := newFakeSvc()
	tab := fixTab(t, svc)
	pumpForm(tab, tab.StartSession(runSource()))
	if tab.dialog == nil || !strings.Contains(tab.View(), "No open PR for main") {
		t.Fatalf("no confirm:\n%s", tab.View())
	}
	toasts := answer(tab, press("enter")) // default: Start
	if !slices.Contains(toasts, "ok: fix session fix-ci-77 started") {
		t.Errorf("toasts = %q", toasts)
	}
	starts, _, _ := svc.snapshot()
	if len(starts) != 1 {
		t.Fatalf("starts = %+v", starts)
	}
	o := starts[0]
	if o.Branch != "fix/ci-77" || o.StartCommit != "abc1234def" || o.Base != "main" {
		t.Errorf("opts = %+v", o)
	}
	if len(o.Links) != 1 || o.Links[0].To != store.RunRef("daan/forge", 77) ||
		o.Links[0].Meta.URL != "https://github.com/daan/forge/actions/runs/77" {
		t.Errorf("links = %+v", o.Links)
	}
}

func TestFixRunWithoutPRDeclined(t *testing.T) {
	svc := newFakeSvc()
	tab := fixTab(t, svc)
	pumpForm(tab, tab.StartSession(runSource()))
	answer(tab, press("left"), press("enter")) // Cancel
	if tab.dialog != nil {
		t.Error("dialog still open")
	}
	starts, _, _ := svc.snapshot()
	if len(starts) != 0 {
		t.Errorf("started after cancel: %+v", starts)
	}
}

func TestSourceNameRun(t *testing.T) {
	tests := []struct {
		src  tabs.Source
		want string
	}{
		{tabs.Source{Kind: store.KindGitHubRun, ID: "a/b#77", Branch: "fix/ci-77"}, "fix-ci-77"},
		{tabs.Source{Kind: store.KindGitHubRun, ID: "a/b#77"}, "run-77"},
	}
	for _, tt := range tests {
		if got := SourceName(tt.src); got != tt.want {
			t.Errorf("SourceName(%+v) = %q, want %q", tt.src, got, tt.want)
		}
	}
	if got := RefURL(testConfig(), store.KindGitHubRun, "a/b#77"); got != "https://github.com/a/b/actions/runs/77" {
		t.Errorf("RefURL = %q", got)
	}
	if got := agent.ShortRef(store.RunRef("a/b", 77)); got != "run 77" {
		t.Errorf("ShortRef = %q", got)
	}
}

func TestRunLinkInDetail(t *testing.T) {
	r := linkedRow("fix-ci-77", store.Link{Other: store.RunRef("daan/forge", 77), LinkMeta: store.LinkMeta{Title: "CI #12"}})
	tab := attachTab(t, newFakeSvc(r))
	if !strings.Contains(ansi.Strip(tab.View()), "run: run 77 CI #12") {
		t.Errorf("no run line:\n%s", tab.View())
	}
	// No run link: no run line.
	tab = attachTab(t, newFakeSvc(row("plain", store.StatusIdle)))
	if strings.Contains(tab.View(), "run:") {
		t.Errorf("run line without a run link:\n%s", tab.View())
	}
}

// busyRig has pr-12 linked to the PR and working on a turn.
func busyRig(t *testing.T) (*Tab, *fakeSvc) {
	t.Helper()
	busy := linkedRow("pr-12", prLink(12))
	busy.Status = store.StatusWorking
	svc := newFakeSvc(busy)
	return fixTab(t, svc), svc
}

func (f *fakeSvc) sentTo(name string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sent[name])
}

func TestFixBusySessionAsks(t *testing.T) {
	tab, svc := busyRig(t)
	pumpForm(tab, tab.StartSession(fixSource(tabs.FixRequest{Reuse: true, Immediate: true})))
	v := ansi.Strip(tab.View())
	for _, want := range []string{"pr-12 is busy", "Send now (queued)", "Start a new fix session (own branch off feature/fast)", "Cancel"} {
		if !strings.Contains(v, want) {
			t.Errorf("question missing %q:\n%s", want, v)
		}
	}
	if len(svc.sentTo("pr-12")) != 0 {
		t.Error("pasted into a working session without asking")
	}
	starts, _, _ := svc.snapshot()
	if len(starts) != 0 {
		t.Errorf("started without asking: %+v", starts)
	}
}

func TestFixBusySendNow(t *testing.T) {
	tab, svc := busyRig(t)
	pumpForm(tab, tab.StartSession(fixSource(tabs.FixRequest{Reuse: true, Immediate: true})))
	toasts := answer(tab, press("enter")) // the default: send now
	if !slices.Contains(toasts, "ok: sent the failure to pr-12") {
		t.Errorf("toasts = %q", toasts)
	}
	if sent := svc.sentTo("pr-12"); len(sent) != 1 || !strings.Contains(sent[0], "--- FAIL: TestParse") {
		t.Errorf("sent = %q", sent)
	}
}

func TestFixBusyStartNew(t *testing.T) {
	tab, svc := busyRig(t)
	pumpForm(tab, tab.StartSession(fixSource(tabs.FixRequest{Reuse: true, Immediate: true})))
	toasts := answer(tab, press("down"), press("enter"))
	if !slices.Contains(toasts, "ok: fix session pr-12-2 started") {
		t.Errorf("toasts = %q", toasts)
	}
	starts, _, _ := svc.snapshot()
	if len(starts) != 1 {
		t.Fatalf("starts = %+v", starts)
	}
	o := starts[0]
	// The busy session holds the PR branch: the new one branches off it.
	if o.Branch != "" || o.Base != "feature/fast" || o.Template != "fix-ci" ||
		len(o.Links) != 1 || o.Links[0].To != store.PRRef("daan/forge", 12) {
		t.Errorf("opts = %+v", o)
	}
	if !strings.Contains(o.Prompt, "--- FAIL: TestParse") || !strings.Contains(o.Prompt, "on branch agent/pr-12-2") {
		t.Errorf("prompt:\n%s", o.Prompt)
	}
	if len(svc.sentTo("pr-12")) != 0 {
		t.Error("also pasted into the busy session")
	}
}

func TestFixBusyCancel(t *testing.T) {
	tab, svc := busyRig(t)
	pumpForm(tab, tab.StartSession(fixSource(tabs.FixRequest{Reuse: true, Immediate: true})))
	toasts := answer(tab, press("down"), press("down"), press("enter"))
	starts, _, _ := svc.snapshot()
	if len(toasts) != 0 || len(starts) != 0 || len(svc.sentTo("pr-12")) != 0 || tab.dialog != nil {
		t.Errorf("cancel did something: toasts %q, starts %+v, dialog %v", toasts, starts, tab.dialog != nil)
	}
}

func TestFixBusyEditKeyAsksThenEdits(t *testing.T) {
	tab, svc := busyRig(t)
	pumpForm(tab, tab.StartSession(fixSource(tabs.FixRequest{Reuse: true})))
	if !strings.Contains(ansi.Strip(tab.View()), "pr-12 is busy") {
		t.Fatalf("no question:\n%s", ansi.Strip(tab.View()))
	}
	answer(tab, press("enter")) // send now → the prompt, editable
	if !strings.Contains(ansi.Strip(tab.View()), "Send to pr-12") {
		t.Fatalf("no editable prompt:\n%s", ansi.Strip(tab.View()))
	}
	d := tab.dialog
	tab.dialog = nil
	toasts, _ := pump(tab, d.done())
	if !slices.Contains(toasts, "ok: sent the failure to pr-12") || len(svc.sentTo("pr-12")) != 1 {
		t.Errorf("toasts = %q", toasts)
	}
}

func TestFixIdleSessionStillPastesDirectly(t *testing.T) {
	idle := linkedRow("pr-12", prLink(12)) // linkedRow is idle
	svc := newFakeSvc(idle)
	tab := fixTab(t, svc)
	toasts, _ := pump(tab, tab.StartSession(fixSource(tabs.FixRequest{Reuse: true, Immediate: true})))
	if !slices.Contains(toasts, "ok: sent the failure to pr-12") || tab.dialog != nil {
		t.Errorf("toasts = %q, dialog %v", toasts, tab.dialog != nil)
	}
}
