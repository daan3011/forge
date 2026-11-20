package sessions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/store"
)

// uniqueIDs gives every Start its own Claude session id.
func uniqueIDs(e *env) {
	n := 0
	e.svc.c.NewID = func() (string, error) {
		n++
		return fmt.Sprintf("0b5f3c3e-8a1d-4c2b-9f00-%012d", n), nil
	}
}

func TestStartSavesLinks(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	issue := store.IssueRef("PROJ-12")
	res, err := e.svc.Start(ctx, StartOpts{
		Name: "proj-12", Project: "forge",
		Links: []LinkSpec{{To: issue, Meta: store.LinkMeta{URL: "https://plane/PROJ-12", Title: "Fix login", State: "started"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Session.BranchCreated || res.Session.WorktreeExternal {
		t.Errorf("session = %+v", res.Session)
	}
	rows, err := e.svc.List(ctx, "forge", false)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
	r := rows[0]
	if r.Link != "PROJ-12" || len(r.LinkList) != 1 || r.LinkList[0].Title != "Fix login" || r.LinkList[0].State != "started" {
		t.Errorf("row = %+v", r)
	}
	got, err := e.svc.LinkedSessions(ctx, store.KindPlaneIssue, "PROJ-12")
	if err != nil || len(got) != 1 || got[0].Name != "proj-12" {
		t.Errorf("LinkedSessions = %+v, %v", got, err)
	}
	if got, _ := e.svc.LinkedSessions(ctx, store.KindPlaneIssue, "PROJ-99"); len(got) != 0 {
		t.Errorf("unrelated issue has sessions %+v", got)
	}
}

func TestStartCustomBranch(t *testing.T) {
	tests := []struct {
		name        string
		local       bool
		remote      bool
		wantCreated bool
	}{
		{"new branch", false, false, true},
		{"existing local branch", true, false, false},
		{"PR head on origin", false, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.wt.branches["feature/x"], e.wt.remote["feature/x"] = tt.local, tt.remote
			res, err := e.svc.Start(context.Background(), StartOpts{Name: "s", Project: "forge", Branch: "feature/x"})
			if err != nil {
				t.Fatal(err)
			}
			if res.Session.Branch != "feature/x" || res.Session.BranchCreated != tt.wantCreated {
				t.Errorf("session = %+v", res.Session)
			}
			if c := e.wt.created[0]; c.Branch != "feature/x" {
				t.Errorf("create opts = %+v", c)
			}
		})
	}
}

func TestStartRollbackKeepsForeignBranch(t *testing.T) {
	e := newEnv(t)
	e.wt.remote["feature/pr"] = true
	e.svc.c.Store = failingStore{StoreFrom(e.st)}
	if _, err := e.svc.Start(context.Background(), StartOpts{Name: "pr-7", Project: "forge", Branch: "feature/pr"}); err == nil {
		t.Fatal("want error")
	}
	if len(e.wt.deleted) != 0 {
		t.Errorf("rollback deleted %v; a PR head branch is never deleted", e.wt.deleted)
	}
}

func TestStartBranchInUse(t *testing.T) {
	e := newEnv(t)
	uniqueIDs(e)
	ctx := context.Background()

	t.Run("main checkout", func(t *testing.T) {
		e.wt.checkedOut["main"] = e.proj.Path
		_, err := e.svc.Start(ctx, StartOpts{Name: "on-main", Project: "forge", Branch: "main"})
		var bu *BranchInUseError
		if !errors.As(err, &bu) || !errors.Is(err, ErrBranchInUse) || bu.Path != e.proj.Path || bu.Session != "" {
			t.Fatalf("err = %v (%+v)", err, bu)
		}
	})

	t.Run("another session", func(t *testing.T) {
		first, err := e.svc.Start(ctx, StartOpts{Name: "first", Project: "forge", Branch: "feature/a"})
		if err != nil {
			t.Fatal(err)
		}
		e.wt.checkedOut["feature/a"] = first.Session.Worktree
		_, err = e.svc.Start(ctx, StartOpts{Name: "second", Project: "forge", Branch: "feature/a"})
		var bu *BranchInUseError
		if !errors.As(err, &bu) || bu.Session != "first" {
			t.Fatalf("err = %v (%+v)", err, bu)
		}
	})

	t.Run("run in the external worktree", func(t *testing.T) {
		if err := os.MkdirAll(e.proj.Path, 0o755); err != nil {
			t.Fatal(err)
		}
		n := len(e.wt.created)
		res, err := e.svc.Start(ctx, StartOpts{Name: "on-main", Project: "forge", Branch: "main", Worktree: e.proj.Path})
		if err != nil {
			t.Fatal(err)
		}
		x := res.Session
		if !x.WorktreeExternal || x.BranchCreated || x.Worktree != e.proj.Path || x.Branch != "main" {
			t.Errorf("session = %+v", x)
		}
		if len(e.wt.created) != n {
			t.Error("created a worktree for an external one")
		}
		if dir, _, _, _ := e.tmux.Started(tmux.Target{Session: "forge", Window: "on-main"}); dir != e.proj.Path {
			t.Errorf("window dir = %q", dir)
		}
		if _, err := os.Stat(filepath.Join(e.proj.Path, ".claude", "settings.local.json")); e.svc.c.ForgeBin != "" && err != nil {
			t.Errorf("hooks not installed: %v", err)
		}
	})
}

func TestKillDeletesOnlyOwnBranches(t *testing.T) {
	tests := []struct {
		name       string
		opts       StartOpts
		setup      func(e *env)
		kill       KillOpts
		wantErr    error
		wantDelete []string
		wantRemove bool
	}{
		{
			name: "forge-made agent branch", opts: StartOpts{Name: "a"},
			kill: KillOpts{RemoveWorktree: true, DeleteBranch: true}, wantDelete: []string{"agent/a"}, wantRemove: true,
		},
		{
			name: "forge-made custom branch", opts: StartOpts{Name: "b", Branch: "PROJ-1-x"},
			kill: KillOpts{RemoveWorktree: true, DeleteBranch: true}, wantDelete: []string{"PROJ-1-x"}, wantRemove: true,
		},
		{
			name: "pre-existing local branch", opts: StartOpts{Name: "c", Branch: "mine"},
			setup: func(e *env) { e.wt.branches["mine"] = true },
			kill:  KillOpts{RemoveWorktree: true, DeleteBranch: true}, wantErr: ErrNotOwnBranch,
		},
		{
			name: "PR head branch", opts: StartOpts{Name: "d", Branch: "feature/pr"},
			setup: func(e *env) { e.wt.remote["feature/pr"] = true },
			kill:  KillOpts{RemoveWorktree: true, DeleteBranch: true}, wantErr: ErrNotOwnBranch,
		},
		{
			name: "external worktree", opts: StartOpts{Name: "e", Branch: "main"},
			setup: func(e *env) { _ = os.MkdirAll(e.proj.Path, 0o755) },
			kill:  KillOpts{RemoveWorktree: true, DeleteBranch: true}, wantErr: ErrNotOwnBranch,
		},
		{
			name: "external worktree is archived, never removed", opts: StartOpts{Name: "f", Branch: "main"},
			setup: func(e *env) { _ = os.MkdirAll(e.proj.Path, 0o755) },
			kill:  KillOpts{RemoveWorktree: true},
		},
		{
			name: "delete needs remove", opts: StartOpts{Name: "g"},
			kill: KillOpts{DeleteBranch: true}, wantErr: ErrNotOwnBranch,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			ctx := context.Background()
			if tt.setup != nil {
				tt.setup(e)
			}
			o := tt.opts
			o.Project = "forge"
			if o.Branch == "main" {
				o.Worktree = e.proj.Path
			}
			res, err := e.svc.Start(ctx, o)
			if err != nil {
				t.Fatal(err)
			}
			own := !errors.Is(tt.wantErr, ErrNotOwnBranch) || !tt.kill.RemoveWorktree
			if tt.kill.DeleteBranch && CanDeleteBranch(res.Session) != own {
				t.Errorf("CanDeleteBranch = %v, want %v", CanDeleteBranch(res.Session), own)
			}
			killed, err := e.svc.Kill(ctx, res.Session, tt.kill)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Kill err = %v, want %v", err, tt.wantErr)
			}
			if fmt.Sprint(e.wt.deleted) != fmt.Sprint(tt.wantDelete) {
				t.Errorf("deleted %v, want %v", e.wt.deleted, tt.wantDelete)
			}
			if removed := len(e.wt.removed) > 0; removed != tt.wantRemove {
				t.Errorf("worktree removed = %v, want %v", removed, tt.wantRemove)
			}
			if tt.wantErr == nil && killed.BranchDeleted != (tt.wantDelete != nil) {
				t.Errorf("killed = %+v", killed)
			}
			if o.Worktree != "" && tt.wantErr == nil {
				if _, err := os.Stat(e.proj.Path); err != nil {
					t.Errorf("external worktree touched: %v", err)
				}
				got, _ := e.st.Sessions().Get(ctx, res.Session.ID)
				if got.Status != store.StatusArchived {
					t.Errorf("status = %s, want archived", got.Status)
				}
			}
		})
	}
}

type fakePulls struct {
	pulls map[string][]github.Pull // branch -> pulls
	err   map[string]error
	calls []string
}

func (f *fakePulls) PullsForBranch(_ context.Context, repo, branch string) ([]github.Pull, error) {
	f.calls = append(f.calls, repo+":"+branch)
	return f.pulls[branch], f.err[branch]
}

func TestSyncPRs(t *testing.T) {
	e := newEnv(t)
	uniqueIDs(e)
	ctx := context.Background()
	pulls := &fakePulls{pulls: map[string][]github.Pull{}, err: map[string]error{}}
	e.svc.c.Pulls = pulls

	a := e.start(t, "a")
	b := e.start(t, "b")
	e.start(t, "c")
	if _, err := e.st.Sessions().Create(ctx, store.Session{Name: "on-base", ClaudeSessionID: "u-base", Project: "forge", Repo: "daan/forge", Branch: "main", BaseBranch: "main"}); err != nil {
		t.Fatal(err)
	}

	if changed, err := e.svc.SyncPRs(ctx, "forge"); err != nil || changed {
		t.Fatalf("no PRs: changed=%v err=%v", changed, err)
	}
	for _, c := range pulls.calls {
		if c == "daan/forge:main" {
			t.Error("synced a session sitting on its base branch")
		}
	}

	pulls.pulls["agent/a"] = []github.Pull{{Repo: "daan/forge", Number: 42, Title: "Fix login", State: github.StateOpen, URL: "https://github.com/daan/forge/pull/42"}}
	pulls.err["agent/c"] = errors.New("boom")
	changed, err := e.svc.SyncPRs(ctx, "forge")
	if !changed || err == nil {
		t.Fatalf("changed=%v err=%v; want a link and c's error", changed, err)
	}
	rows, _ := e.svc.List(ctx, "forge", false)
	byName := map[string]Row{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	if r := byName["a"]; r.Link != "#42" || len(r.LinkList) != 1 || r.LinkList[0].State != "open" {
		t.Errorf("a = %+v", r)
	}
	if len(byName["b"].LinkList) != 0 {
		t.Errorf("b got links %+v", byName["b"].LinkList)
	}

	delete(pulls.err, "agent/c")
	if changed, err := e.svc.SyncPRs(ctx, "forge"); err != nil || changed {
		t.Errorf("unchanged PR: changed=%v err=%v", changed, err)
	}
	pulls.pulls["agent/a"][0].State = github.StateMerged
	if changed, _ := e.svc.SyncPRs(ctx, "forge"); !changed {
		t.Error("state change not reported")
	}
	got, _ := e.svc.LinkedSessions(ctx, store.KindGitHubPR, "daan/forge#42")
	if len(got) != 1 || got[0].ID != a.ID {
		t.Errorf("LinkedSessions = %+v", got)
	}
	_ = b
}

func TestLinksOfOrder(t *testing.T) {
	links := []store.Link{
		{Other: store.PRRef("x/y", 1), LinkMeta: store.LinkMeta{State: "merged"}},
		{Other: store.IssueRef("PROJ-1")},
		{Other: store.PRRef("x/y", 2), LinkMeta: store.LinkMeta{State: "open"}},
	}
	prs := LinksOf(links, store.KindGitHubPR)
	if len(prs) != 2 || prs[0].Other.ID != "x/y#2" {
		t.Errorf("prs = %+v", prs)
	}
	if linkLabel(links) != "#2" {
		t.Errorf("label = %q", linkLabel(links))
	}
}
