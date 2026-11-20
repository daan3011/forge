package sessions

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/browser"
	"github.com/daan3011/forge/internal/refresh"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
)

func TestSourceName(t *testing.T) {
	tests := []struct {
		src  tabs.Source
		want string
	}{
		{tabs.Source{Kind: store.KindPlaneIssue, ID: "PROJ-123"}, "proj-123"},
		{tabs.Source{Kind: store.KindPlaneIssue, ID: "Web Team/42"}, "web-team-42"},
		{tabs.Source{Kind: store.KindGitHubPR, ID: "daan/forge#12", Number: 12}, "pr-12"},
		{tabs.Source{Kind: store.KindGitHubPR, ID: "daan/forge#7"}, "pr-7"},
		{tabs.Source{Kind: store.KindPlaneIssue, ID: "!!!"}, "session"},
		{tabs.Source{Kind: store.KindPlaneIssue, ID: strings.Repeat("a", 80)}, strings.Repeat("a", 56)},
	}
	for _, tt := range tests {
		if got := SourceName(tt.src); got != tt.want {
			t.Errorf("SourceName(%+v) = %q, want %q", tt.src, got, tt.want)
		}
	}
}

func TestNormalizeRef(t *testing.T) {
	tests := []struct {
		kind store.LinkKind
		ref  string
		repo string
		want string
		ok   bool
	}{
		{store.KindGitHubPR, "#12", "daan/forge", "daan/forge#12", true},
		{store.KindGitHubPR, "12", "daan/forge", "daan/forge#12", true},
		{store.KindGitHubPR, "other/repo#3", "daan/forge", "other/repo#3", true},
		{store.KindGitHubPR, "https://github.com/x/y/pull/9", "", "x/y#9", true},
		{store.KindGitHubPR, "#12", "", "", false},
		{store.KindGitHubPR, "not a pr", "daan/forge", "", false},
		{store.KindPlaneIssue, " proj-5 ", "", "PROJ-5", true},
		{store.KindPlaneIssue, "", "", "", false},
	}
	for _, tt := range tests {
		got, ok := normalizeRef(tt.kind, tt.ref, tt.repo)
		if got != tt.want || ok != tt.ok {
			t.Errorf("normalizeRef(%s, %q, %q) = %q, %v; want %q, %v", tt.kind, tt.ref, tt.repo, got, ok, tt.want, tt.ok)
		}
	}
}

func TestWithRefLinks(t *testing.T) {
	cfg := testConfig()
	cfg.Plane.BaseURL, cfg.Plane.Workspace = "https://plane.example.com", "acme"
	o := agent.StartOpts{Project: "forge", Links: []agent.LinkSpec{{To: store.IssueRef("PROJ-1")}}}
	got := WithRefLinks(cfg, o, map[store.LinkKind]string{store.KindPlaneIssue: "PROJ-2", store.KindGitHubPR: "#4"})
	if len(got.Links) != 2 {
		t.Fatalf("links = %+v", got.Links)
	}
	if got.Links[0].To.ID != "PROJ-1" {
		t.Errorf("source link replaced: %+v", got.Links[0])
	}
	if pr := got.Links[1]; pr.To != store.PRRef("daan/forge", 4) || pr.Meta.URL != "https://github.com/daan/forge/pull/4" {
		t.Errorf("pr link = %+v", pr)
	}
	got = WithRefLinks(cfg, agent.StartOpts{Project: "forge"}, map[store.LinkKind]string{store.KindPlaneIssue: "PROJ-2"})
	if len(got.Links) != 1 || got.Links[0].Meta.URL != "https://plane.example.com/acme/browse/PROJ-2/" {
		t.Errorf("issue link = %+v", got.Links)
	}
}

func TestSourceRequest(t *testing.T) {
	svc := newFakeSvc(row("pr-12", store.StatusIdle))
	tab := attachTab(t, svc)
	tab.SetProject(nil)
	pump(tab, tab.Refresh())
	req := tab.sourceRequest(tabs.Source{
		Kind: store.KindGitHubPR, ID: "daan/other#12", Number: 12, Title: "Speed up", State: "open",
		Repo: "daan/other", Branch: "feature/fast", Vars: map[string]string{"x": "y"},
	})
	o := req.Opts
	if o.Name != "pr-12-2" || o.Project != "other" || o.Branch != "feature/fast" || o.Template != "review-pr" {
		t.Errorf("opts = %+v", o)
	}
	if req.Refs[store.KindGitHubPR] != "daan/other#12" || req.Vars["x"] != "y" || req.Vars["pr_title"] != "Speed up" {
		t.Errorf("req = %+v", req)
	}
	if len(o.Links) != 1 || o.Links[0].Meta.URL != "https://github.com/daan/other/pull/12" || o.Links[0].Meta.State != "open" {
		t.Errorf("links = %+v", o.Links)
	}
}

// linkTab is a template tab with a fake browser.
func linkTab(t *testing.T, svc *fakeSvc) (*Tab, *browser.Fake) {
	t.Helper()
	tab := newTemplateTab(t, svc, fakePlane(new([]string)))
	b := &browser.Fake{}
	tab.browser = b
	return tab, b
}

// TestStartFromIssueThenPR is P1.9's "done when": a session started for an
// issue shows that issue, and later its PR, in the detail pane.
func TestStartFromIssueThenPR(t *testing.T) {
	svc := newFakeSvc()
	svc.pulls = map[string]store.Link{}
	tab, b := linkTab(t, svc)
	sc := newScreen(t, &host{tab: tab})
	tm := sc.tm
	sc.wait(t, "no sessions yet")

	// The root model routes tabs.StartSessionFrom's message to StartSession
	// (tested in internal/app); the host calls it directly.
	tm.Send(runMsg(func() tea.Cmd {
		return tab.StartSession(tabs.Source{
			Kind: store.KindPlaneIssue, ID: "PROJ-12", Title: "Login button does nothing", State: "started",
			URL: "https://plane.example.com/acme/browse/PROJ-12/", Repo: "daan/forge",
		})
	}))
	sc.wait(t, "New session", "proj-12", "Implement an issue end to end", "branch agent/proj-12")
	for range 5 { // name, project, base, branch, template
		time.Sleep(50 * time.Millisecond)
		tm.Send(press("enter"))
	}
	// The issue is known, so no refs form. The fake provider can't fetch
	// PROJ-12, so only the body is asked for: the title came with the source.
	sc.wait(t, "Template implement-issue", "issue description")
	if strings.Contains(sc.h.lastFrame(), "{issue_title}") {
		t.Errorf("asked for the title the source carried:\n%s", sc.h.lastFrame())
	}
	time.Sleep(200 * time.Millisecond)
	tm.Type("Safari only")
	tm.Send(press("enter"))
	sc.wait(t, "First prompt", "Implement issue PROJ-12: Login button does nothing")
	time.Sleep(200 * time.Millisecond)
	tm.Send(press("enter"))
	sc.toast(t, "ok: session proj-12 started")
	sc.wait(t, "issue: PROJ-12 Login button does nothing · started", "pr: —")

	svc.mu.Lock()
	svc.pulls["agent/proj-12"] = store.Link{
		Other:    store.PRRef("daan/forge", 42),
		LinkMeta: store.LinkMeta{URL: "https://github.com/daan/forge/pull/42", Title: "Fix login", State: "open"},
	}
	svc.mu.Unlock()
	tm.Send(runMsg(func() tea.Cmd { return tab.RefreshSource(refresh.GitHub) }))
	sc.wait(t, "pr: #42 Fix login · open", "issue: PROJ-12")

	tm.Send(press("o"))
	sc.toast(t, "info: opened https://github.com/daan/forge/pull/42")
	tm.Send(press("O"))
	sc.toast(t, "info: opened https://plane.example.com/acme/browse/PROJ-12/")

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	starts, _, _ := svc.snapshot()
	if len(starts) != 1 || len(starts[0].Links) != 1 || starts[0].Links[0].To != store.IssueRef("PROJ-12") {
		t.Fatalf("starts = %+v", starts)
	}
	if got := b.Opened(); !slices.Equal(got, []string{"https://github.com/daan/forge/pull/42", "https://plane.example.com/acme/browse/PROJ-12/"}) {
		t.Errorf("opened %v", got)
	}
}

func linkedRow(name string, links ...store.Link) agent.Row {
	r := row(name, store.StatusIdle)
	r.LinkList = links
	return r
}

func TestOpenLinked(t *testing.T) {
	pr := func(n int, state string) store.Link {
		return store.Link{Other: store.PRRef("daan/forge", n), LinkMeta: store.LinkMeta{URL: "https://github.com/daan/forge/pull/" + string(rune('0'+n)), State: state}}
	}
	issue := store.Link{Other: store.IssueRef("PROJ-1"), LinkMeta: store.LinkMeta{URL: "https://plane.example.com/acme/browse/PROJ-1/"}}
	tests := []struct {
		name      string
		links     []store.Link
		kind      store.LinkKind
		wantOpen  []string
		wantToast string
		picker    bool
	}{
		{"pr", []store.Link{issue, pr(4, "open")}, store.KindGitHubPR, []string{"https://github.com/daan/forge/pull/4"}, "", false},
		{"pr falls back to issue", []store.Link{issue}, store.KindGitHubPR, []string{issue.URL}, "", false},
		{"nothing linked", nil, store.KindGitHubPR, nil, "info: s has no linked PR or issue", false},
		{"no issue", []store.Link{pr(4, "open")}, store.KindPlaneIssue, nil, "info: s has no linked issue", false},
		{"two PRs ask", []store.Link{pr(2, "merged"), pr(3, "open")}, store.KindGitHubPR, nil, "", true},
		{"no url", []store.Link{{Other: store.IssueRef("PROJ-9")}}, store.KindPlaneIssue, nil, "info: no URL known for PROJ-9", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newFakeSvc(linkedRow("s", tt.links...))
			tab := attachTab(t, svc)
			b := &browser.Fake{}
			tab.browser = b
			toasts, _ := pump(tab, tab.openLinked(tt.kind))
			if !slices.Equal(b.Opened(), tt.wantOpen) {
				t.Errorf("opened %v, want %v", b.Opened(), tt.wantOpen)
			}
			if tt.wantToast != "" && (len(toasts) == 0 || !strings.HasPrefix(toasts[0], tt.wantToast)) {
				t.Errorf("toasts = %v, want %q", toasts, tt.wantToast)
			}
			if (tab.dialog != nil) != tt.picker {
				t.Fatalf("picker open = %v", tab.dialog != nil)
			}
			if tt.picker {
				view := ansi.Strip(tab.View())
				if !strings.Contains(view, "#3") || !strings.Contains(view, "#2") || strings.Index(view, "#3") > strings.Index(view, "#2") {
					t.Errorf("picker should list the open PR first:\n%s", view)
				}
			}
		})
	}
}

// finish ends a teatest program and waits for it.
func finish(t *testing.T, sc *screen) {
	t.Helper()
	sc.tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	sc.tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

func TestLinkPickerOpensChoice(t *testing.T) {
	pr := func(n int, state string) store.Link {
		return store.Link{Other: store.PRRef("daan/forge", n), LinkMeta: store.LinkMeta{
			URL: "https://github.com/daan/forge/pull/" + string(rune('0'+n)), Title: "PR " + string(rune('0'+n)), State: state,
		}}
	}
	svc := newFakeSvc(linkedRow("s", pr(2, "merged"), pr(3, "open")))
	tab, b := linkTab(t, svc)
	sc := newScreen(t, &host{tab: tab})
	sc.wait(t, "pr: #3 PR 3 · open (+1 more)")
	sc.tm.Send(press("o"))
	sc.wait(t, "Open which link of s?", "#2  PR 2  · merged")
	sc.tm.Send(press("down"))
	time.Sleep(100 * time.Millisecond)
	sc.tm.Send(press("enter"))
	sc.toast(t, "info: opened https://github.com/daan/forge/pull/2")
	finish(t, sc)
	if got := b.Opened(); !slices.Equal(got, []string{"https://github.com/daan/forge/pull/2"}) {
		t.Errorf("opened %v", got)
	}
}

func TestBranchInUseDialog(t *testing.T) {
	t.Run("run in the other worktree", func(t *testing.T) {
		svc := newFakeSvc()
		svc.startErr = &agent.BranchInUseError{Branch: "main", Path: "/code/forge"}
		tab := newTab(svc)
		sc := newScreen(t, &host{tab: tab})
		sc.wait(t, "no sessions yet")
		sc.tm.Send(runMsg(func() tea.Cmd { return tab.start(agent.StartOpts{Name: "on-main", Project: "forge", Branch: "main"}) }))
		sc.wait(t, "Branch main is already checked out at /code/forge", "Start the session in /code/forge")
		sc.tm.Send(press("enter"))
		sc.toast(t, "ok: session on-main started")
		finish(t, sc)
		starts, _, _ := svc.snapshot()
		if len(starts) != 2 || starts[1].Worktree != "/code/forge" || starts[1].Branch != "main" {
			t.Fatalf("starts = %+v", starts)
		}
	})

	t.Run("go to the forge session", func(t *testing.T) {
		svc := newFakeSvc(row("first", store.StatusIdle))
		svc.startErr = &agent.BranchInUseError{Branch: "feature/a", Path: "/wt/first", Session: "first"}
		tab := newTab(svc)
		sc := newScreen(t, &host{tab: tab})
		sc.wait(t, "first")
		sc.tm.Send(runMsg(func() tea.Cmd {
			return tab.start(agent.StartOpts{Name: "second", Project: "forge", Branch: "feature/a"})
		}))
		sc.wait(t, "Go to session first")
		sc.tm.Send(press("enter"))
		sc.until(t, "attach", func(string, []string) bool {
			_, _, attaches := svc.attachCalls()
			return slices.Equal(attaches, []string{"first"})
		})
		finish(t, sc)
		if starts, _, _ := svc.snapshot(); len(starts) != 1 {
			t.Errorf("started again: %+v", starts)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		svc := newFakeSvc()
		svc.startErr = &agent.BranchInUseError{Branch: "main", Path: "/code/forge"}
		tab := newTab(svc)
		sc := newScreen(t, &host{tab: tab})
		sc.wait(t, "no sessions yet")
		sc.tm.Send(runMsg(func() tea.Cmd { return tab.start(agent.StartOpts{Name: "x", Project: "forge", Branch: "main"}) }))
		sc.wait(t, "Start the session in /code/forge")
		sc.tm.Send(press("down"))
		time.Sleep(100 * time.Millisecond)
		sc.tm.Send(press("enter"))
		sc.wait(t, "no sessions yet")
		finish(t, sc)
		if starts, _, _ := svc.snapshot(); len(starts) != 1 {
			t.Errorf("starts = %+v", starts)
		}
	})
}

func TestKillOffersOnlyOwnBranches(t *testing.T) {
	own := row("own", store.StatusIdle)
	own.BranchCreated = true
	foreign := row("pr-7", store.StatusIdle)
	foreign.Branch = "feature/pr"
	external := row("main-co", store.StatusIdle)
	external.Branch, external.WorktreeExternal, external.BranchCreated = "main", true, false
	tests := []struct {
		r          agent.Row
		wantDelete bool
		wantText   string
	}{
		{own, true, "Kill and remove worktree"},
		{foreign, false, "Kill and remove worktree"},
		{external, false, "Kill and archive (worktree left in place)"},
	}
	for _, tt := range tests {
		t.Run(tt.r.Name, func(t *testing.T) {
			tab := attachTab(t, newFakeSvc(tt.r))
			pump(tab, tab.openKill())
			view := ansi.Strip(tab.View())
			if got := strings.Contains(view, "delete branch"); got != tt.wantDelete {
				t.Errorf("delete offered = %v:\n%s", got, view)
			}
			if !strings.Contains(view, tt.wantText) {
				t.Errorf("view lacks %q:\n%s", tt.wantText, view)
			}
		})
	}

	t.Run("choosing delete passes DeleteBranch", func(t *testing.T) {
		svc := newFakeSvc(own)
		sc := newScreen(t, &host{tab: newTab(svc)})
		sc.wait(t, "own")
		sc.tm.Send(press("x"))
		sc.wait(t, "delete branch agent/own")
		sc.tm.Send(press("down"))
		sc.tm.Send(press("down"))
		time.Sleep(100 * time.Millisecond)
		sc.tm.Send(press("enter"))
		sc.toast(t, "ok: own archived")
		finish(t, sc)
		if _, kills, _ := svc.snapshot(); len(kills) != 1 || !kills[0].DeleteBranch || !kills[0].RemoveWorktree {
			t.Errorf("kills = %+v", kills)
		}
	})
}

func TestSyncRunsOnGitHubTicksOnly(t *testing.T) {
	svc := newFakeSvc(row("a", store.StatusIdle))
	tab := attachTab(t, svc)
	before := svc.syncCount()
	pump(tab, tab.RefreshSource(refresh.Sessions))
	if svc.syncCount() != before {
		t.Error("synced on a Sessions tick")
	}
	pump(tab, tab.RefreshSource(refresh.GitHub))
	if svc.syncCount() != before+1 {
		t.Errorf("syncs = %d, want %d", svc.syncCount(), before+1)
	}
	if !slices.Contains(tab.Sources(), refresh.GitHub) {
		t.Error("Sources lacks GitHub")
	}
}
