package sessions

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/refresh"
	"github.com/daan3011/forge/internal/store"
)

func pr(n int, state string) store.Link {
	return store.Link{
		Other:    store.PRRef("daan/forge", n),
		LinkMeta: store.LinkMeta{Title: "Change", State: state},
	}
}

// mergePR makes the fake's next PR sync report the PR of name's branch as
// merged.
func mergePR(f *fakeSvc, name string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pulls == nil {
		f.pulls = map[string]store.Link{}
	}
	f.pulls["agent/"+name] = pr(n, "merged")
}

// TestMergedPRArchivesSession is P1.10's done-when in the tab: a GitHub tick
// sees the PRs merged, the sessions move to the archive with a summary, and
// the cleanup questions come one at a time (sessions.archive_cleanup: ask).
func TestMergedPRArchivesSession(t *testing.T) {
	svc := newFakeSvc(linkedRow("alpha", pr(42, "open")), linkedRow("bravo", pr(43, "open")), linkedRow("charlie", pr(44, "open")))
	tab := newTab(svc)
	sc := newScreen(t, &host{tab: tab})
	tm := sc.tm
	sc.wait(t, "alpha", "bravo", "charlie")

	mergePR(svc, "alpha", 42)
	mergePR(svc, "charlie", 44)
	tm.Send(runMsg(func() tea.Cmd { return tab.RefreshSource(refresh.GitHub) }))
	sc.toast(t, "ok: 2 sessions archived (PRs done): alpha, charlie")

	sc.wait(t, "alpha is archived (PR #42 merged). Clean up?", "Remove worktree and tmux window", "Keep them")
	tm.Send(press("enter")) // remove
	sc.toast(t, "ok: alpha archived — worktree removed")
	sc.wait(t, "charlie is archived (PR #44 merged). Clean up?")
	tm.Send(press("down"))
	tm.Send(press("enter")) // keep
	sc.until(t, "only bravo active", func(frame string, _ []string) bool {
		return strings.Contains(frame, "bravo") && !strings.Contains(frame, "alpha") && !strings.Contains(frame, "charlie")
	})

	tm.Send(press("a"))
	sc.wait(t, "alpha", "charlie", "archived: 2026-11-20 09:30 · PR #42 merged", "── summary", "summary of alpha")

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	_, kills, rows := svc.snapshot()
	if len(kills) != 1 || !kills[0].RemoveWorktree || kills[0].Force || kills[0].DeleteBranch {
		t.Errorf("kills = %+v, want one non-forced worktree removal", kills)
	}
	for _, r := range rows {
		switch r.Name {
		case "alpha":
			if !r.Archived() || r.Status != store.StatusArchived {
				t.Errorf("alpha = %s archived=%v", r.Status, r.Archived())
			}
		case "bravo":
			if r.Archived() {
				t.Error("bravo archived with an open PR")
			}
		case "charlie":
			if !r.Archived() || r.Worktree == "" || r.TmuxWindow == "" {
				t.Errorf("charlie = %+v, want archived with worktree and window kept", r.Session)
			}
		}
	}
}

func TestArchiveCleanupModes(t *testing.T) {
	tests := []struct {
		mode      config.ArchiveCleanup
		wantKills int
		wantAsk   bool
	}{
		{config.CleanupAlways, 1, false},
		{config.CleanupNever, 0, false},
		{config.CleanupAsk, 0, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			svc := newFakeSvc(linkedRow("a", pr(1, "open")))
			tab := attachTab(t, svc)
			tab.cfg.Sessions.ArchiveCleanup = tt.mode
			mergePR(svc, "a", 1)
			toasts, _ := pump(tab, tab.RefreshSource(refresh.GitHub))
			if !slices.Contains(toasts, "ok: a archived — PR #1 merged") {
				t.Errorf("toasts = %q", toasts)
			}
			if _, kills, _ := svc.snapshot(); len(kills) != tt.wantKills {
				t.Errorf("kills = %+v, want %d", kills, tt.wantKills)
			}
			if (tab.dialog != nil) != tt.wantAsk {
				t.Errorf("dialog open = %v, want %v", tab.dialog != nil, tt.wantAsk)
			}
		})
	}
}

func TestBusySessionNotAutoArchived(t *testing.T) {
	r := linkedRow("a", pr(1, "merged"))
	r.Status = store.StatusWorking
	svc := newFakeSvc(r)
	tab := attachTab(t, svc)
	pump(tab, tab.RefreshSource(refresh.GitHub))
	if _, _, rows := svc.snapshot(); rows[0].Archived() {
		t.Error("archived a working session")
	}
	svc.setStatus("a", store.StatusIdle)
	pump(tab, tab.RefreshSource(refresh.GitHub))
	if _, _, rows := svc.snapshot(); !rows[0].Archived() {
		t.Error("idle session with a merged PR not archived on the next tick")
	}
}

func TestManualArchiveAndRestore(t *testing.T) {
	svc := newFakeSvc(row("a", store.StatusIdle))
	tab := newTab(svc)
	tab.cfg.Sessions.ArchiveCleanup = config.CleanupNever
	sc := newScreen(t, &host{tab: tab})
	tm := sc.tm
	sc.wait(t, "agent/a")

	tm.Send(press("A"))
	sc.toast(t, "ok: a archived")
	sc.wait(t, "no sessions yet")
	tm.Send(press("a"))
	sc.wait(t, "archived: 2026-11-20 09:30 · worktree kept", "summary of a")

	tm.Send(press("D")) // the worktree is still there
	sc.toast(t, "info: a still has its worktree — remove it (x) before deleting")
	tm.Send(press("enter"))
	sc.toast(t, "info: a is archived — restore it (A")

	tm.Send(press("A"))
	sc.toast(t, "ok: a restored")
	sc.until(t, "active view with a", func(frame string, _ []string) bool {
		return strings.Contains(frame, "── output") && !strings.Contains(frame, "archived:")
	})

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	_, kills, rows := svc.snapshot()
	if len(kills) != 0 {
		t.Errorf("kills = %+v (archive_cleanup: never)", kills)
	}
	if rows[0].Archived() || !rows[0].AutoArchiveOff {
		t.Errorf("after restore: %+v", rows[0].Session)
	}
}

func TestCleanupFromArchivedView(t *testing.T) {
	kept := row("kept", store.StatusDead)
	kept.ArchivedAt = archiveAt
	svc := newFakeSvc(kept)
	tab := attachTab(t, svc)
	pump(tab, tab.toggleArchive())
	_, cmd := tab.Update(press("x"))
	pump(tab, cmd)
	if tab.dialog == nil || tab.dialog.subject != "kept" {
		t.Fatalf("x in the archived view didn't ask to clean up kept")
	}

	removed := row("gone", store.StatusArchived)
	tab = attachTab(t, newFakeSvc(removed))
	pump(tab, tab.toggleArchive())
	_, cmd = tab.Update(press("x"))
	if toasts, _ := pump(tab, cmd); len(toasts) != 1 || toasts[0] != "info: gone has no worktree or tmux window left to remove" {
		t.Errorf("toasts = %q", toasts)
	}
}

// TestPaletteFindsArchivedSessions: archived sessions are palette entities
// matched by summary and notes, from either view; picking one opens the
// archived view on it.
func TestPaletteFindsArchivedSessions(t *testing.T) {
	old := row("old", store.StatusArchived)
	old.Summary, old.Notes = "fixed the flaky login test", "ask Sam about retries"
	old.LinkList = []store.Link{pr(9, "merged")}
	svc := newFakeSvc(row("live", store.StatusIdle), old)
	tab := attachTab(t, svc)

	byKind := map[string]string{}
	for _, e := range tab.Entities() {
		byKind[e.Kind+":"+e.ID] = e.Search + " | " + e.Detail
	}
	if _, ok := byKind[EntitySession+":live"]; !ok {
		t.Errorf("no active entity: %v", byKind)
	}
	arch, ok := byKind[EntityArchivedSession+":old"]
	if !ok || !strings.Contains(arch, "flaky login") || !strings.Contains(arch, "ask Sam") || !strings.Contains(arch, "PR #9 merged") {
		t.Errorf("archived entity = %q", arch)
	}

	pump(tab, tab.Select("old"))
	if r, ok := tab.current(); !tab.archived || !ok || r.Name != "old" {
		t.Errorf("Select(old): archived view=%v current=%v", tab.archived, r.Name)
	}
	if n := len(tab.Entities()); n != 2 {
		t.Errorf("entities from the archived view = %d, want 2", n)
	}
	pump(tab, tab.Select("live"))
	if r, ok := tab.current(); tab.archived || !ok || r.Name != "live" {
		t.Errorf("Select(live): archived view=%v current=%v", tab.archived, r.Name)
	}
}
