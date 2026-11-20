package sessions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/store"
)

// withTranscript points x at a copy of the transcript parser's fixture.
func withTranscript(t *testing.T, e *env, x store.Session) {
	t.Helper()
	b, err := os.ReadFile("transcript/testdata/session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), x.Name+".jsonl")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.st.Sessions().SetTranscript(context.Background(), x.ID, path, 0); err != nil {
		t.Fatal(err)
	}
}

func prLink(t *testing.T, e *env, x store.Session, n int, state string) {
	t.Helper()
	err := e.st.Links().Add(context.Background(), store.SessionRef(x.ID), store.PRRef("daan/forge", n),
		store.LinkMeta{Title: "Change " + state, State: state})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAutoArchive(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name   string
		prs    []string // states of the session's PRs
		status store.Status
		off    bool
		issue  bool
		want   bool
	}{
		{name: "merged", prs: []string{github.StateMerged}, status: store.StatusIdle, want: true},
		{name: "closed", prs: []string{github.StateClosed}, status: store.StatusDead, want: true},
		{name: "merged and closed", prs: []string{github.StateMerged, github.StateClosed}, status: store.StatusIdle, want: true},
		{name: "one still open", prs: []string{github.StateMerged, github.StateOpen}, status: store.StatusIdle},
		{name: "draft", prs: []string{github.StateDraft}, status: store.StatusIdle},
		{name: "no PR", status: store.StatusIdle},
		{name: "issue only", issue: true, status: store.StatusIdle},
		{name: "working", prs: []string{github.StateMerged}, status: store.StatusWorking},
		{name: "waiting", prs: []string{github.StateMerged}, status: store.StatusWaiting},
		{name: "restored", prs: []string{github.StateMerged}, status: store.StatusIdle, off: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			x := e.start(t, "s")
			x.Status, x.AutoArchiveOff, x.Notes = tt.status, tt.off, "my notes"
			if err := e.st.Sessions().Update(ctx, x); err != nil {
				t.Fatal(err)
			}
			for i, st := range tt.prs {
				prLink(t, e, x, 10+i, st)
			}
			if tt.issue {
				if err := e.st.Links().Add(ctx, store.SessionRef(x.ID), store.IssueRef("PROJ-1"), store.LinkMeta{State: "done"}); err != nil {
					t.Fatal(err)
				}
			}
			got, err := e.svc.AutoArchive(ctx, "forge")
			if err != nil {
				t.Fatal(err)
			}
			if (len(got) == 1) != tt.want {
				t.Fatalf("archived %d sessions, want archived=%v", len(got), tt.want)
			}
			row, _ := e.st.Sessions().Get(ctx, x.ID)
			if row.Archived() != tt.want {
				t.Errorf("row archived = %v", row.Archived())
			}
			if row.Notes != "my notes" {
				t.Errorf("notes = %q", row.Notes)
			}
			if tt.want {
				if !strings.Contains(row.Summary, "PR daan/forge#10 ") || got[0].Summary != row.Summary {
					t.Errorf("summary = %q", row.Summary)
				}
				// The worktree and window are left for the cleanup step.
				if row.Worktree == "" || row.TmuxWindow == "" || row.Status != tt.status {
					t.Errorf("archive touched the worktree/window/status: %+v", row)
				}
				if again, _ := e.svc.AutoArchive(ctx, "forge"); len(again) != 0 {
					t.Errorf("archived again: %+v", again)
				}
			}
		})
	}
}

// TestMergedPRArchivesWithSummary is P1.10's done-when on the real service:
// a PR sync sees the session's PR merged, and AutoArchive moves the session
// to the archive with a summary from its transcript.
func TestMergedPRArchivesWithSummary(t *testing.T) {
	e := newEnv(t)
	uniqueIDs(e)
	ctx := context.Background()
	pulls := &fakePulls{pulls: map[string][]github.Pull{}}
	e.svc.c.Pulls = pulls
	x := e.start(t, "a")
	e.start(t, "b")
	withTranscript(t, e, x)
	if err := e.st.Sessions().SetStatus(ctx, x.ID, store.StatusIdle, t0); err != nil {
		t.Fatal(err)
	}

	pulls.pulls["agent/a"] = []github.Pull{{Repo: "daan/forge", Number: 42, Title: "Add README", State: github.StateOpen}}
	if _, err := e.svc.SyncPRs(ctx, "forge"); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.svc.AutoArchive(ctx, "forge"); len(got) != 0 {
		t.Fatalf("archived with an open PR: %+v", got)
	}

	pulls.pulls["agent/a"][0].State = github.StateMerged
	if _, err := e.svc.SyncPRs(ctx, "forge"); err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.AutoArchive(ctx, "forge")
	if err != nil || len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("AutoArchive = %+v, %v", got, err)
	}
	want := "PR daan/forge#42 merged — Add README\n" +
		"3 messages · 3 tool calls · 2 files: /repo/README.md, /repo/main.go\n" +
		"Last replies:\n- I'll **write** it.\n- Done."
	if got[0].Summary != want {
		t.Errorf("summary =\n%s\nwant\n%s", got[0].Summary, want)
	}
	active, _ := e.svc.List(ctx, "forge", false)
	archived, _ := e.svc.List(ctx, "forge", true)
	if len(active) != 1 || active[0].Name != "b" || len(archived) != 1 || archived[0].Name != "a" {
		t.Errorf("active=%v archived=%v", names(active), names(archived))
	}
	if r := ArchiveReason(archived[0].LinkList); r != "PR #42 merged" {
		t.Errorf("ArchiveReason = %q", r)
	}
}

func names(rows []Row) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}

func TestArchiveAndUnarchive(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	x := e.start(t, "m")

	got, err := e.svc.Archive(ctx, x)
	if err != nil || !got.Archived() || got.Summary != "no transcript" {
		t.Fatalf("Archive = %+v, %v", got, err)
	}
	if _, err := e.svc.Archive(ctx, x); !errors.Is(err, ErrArchived) {
		t.Errorf("archive twice err = %v", err)
	}

	// Cleaning up an archived session keeps its summary and archive time.
	if _, err := e.svc.Kill(ctx, got, KillOpts{RemoveWorktree: true}); err != nil {
		t.Fatal(err)
	}
	removed, _ := e.st.Sessions().Get(ctx, x.ID)
	if removed.Status != store.StatusArchived || removed.Summary != "no transcript" || !removed.ArchivedAt.Equal(got.ArchivedAt) {
		t.Errorf("after cleanup = %+v", removed)
	}

	if err := e.svc.Unarchive(ctx, removed); err != nil {
		t.Fatal(err)
	}
	back, _ := e.st.Sessions().Get(ctx, x.ID)
	if back.Archived() || back.Status != store.StatusDead || !back.AutoArchiveOff {
		t.Errorf("after unarchive = %+v", back)
	}
	if err := e.svc.Unarchive(ctx, back); err == nil {
		t.Error("unarchive of an active session: want error")
	}
	// Restored sessions can be resumed (P1.5 recreates the worktree).
	if _, err := e.svc.Inspect(ctx, back); err != nil {
		t.Errorf("Inspect after unarchive: %v", err)
	}
}

func TestKillWritesSummary(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	x := e.start(t, "k")
	withTranscript(t, e, x)
	prLink(t, e, x, 7, github.StateOpen)
	if _, err := e.svc.Kill(ctx, x, KillOpts{RemoveWorktree: true}); err != nil {
		t.Fatal(err)
	}
	got, _ := e.st.Sessions().Get(ctx, x.ID)
	if !strings.HasPrefix(got.Summary, "PR daan/forge#7 open — Change open\n3 messages") {
		t.Errorf("summary = %q", got.Summary)
	}
}

func TestArchiveReason(t *testing.T) {
	pr := func(n, state string) store.Link {
		return store.Link{Other: store.Ref{Kind: store.KindGitHubPR, ID: "o/r#" + n}, LinkMeta: store.LinkMeta{State: state}}
	}
	tests := []struct {
		links []store.Link
		want  string
	}{
		{nil, ""},
		{[]store.Link{{Other: store.IssueRef("P-1")}}, ""},
		{[]store.Link{pr("3", "closed")}, "PR #3 closed"},
		{[]store.Link{pr("3", "merged"), pr("4", "closed")}, "PRs #3 merged, #4 closed"},
	}
	for _, tt := range tests {
		got := ArchiveReason(tt.links)
		if len(tt.links) == 2 {
			// LinksOf orders by recency; accept either order.
			if got != tt.want && got != "PRs #4 closed, #3 merged" {
				t.Errorf("ArchiveReason = %q", got)
			}
			continue
		}
		if got != tt.want {
			t.Errorf("ArchiveReason(%v) = %q, want %q", tt.links, got, tt.want)
		}
	}
}
