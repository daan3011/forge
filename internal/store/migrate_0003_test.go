package store

import (
	"context"
	"testing"
	"testing/fstest"
	"time"
)

// TestMigration0003Defaults upgrades a version-2 database: existing sessions
// ran on forge-made agent/ branches in forge worktrees.
func TestMigration0003Defaults(t *testing.T) {
	ctx := context.Background()
	db := openRaw(t)
	all, err := loadMigrations(migrationsFS)
	noErr(t, err)
	v2 := fstest.MapFS{}
	for _, m := range all[:2] {
		v2[m.name] = &fstest.MapFile{Data: []byte(m.sql)}
	}
	noErr(t, migrate(ctx, db, v2, time.Now))
	_, err = db.ExecContext(ctx, `INSERT INTO sessions (id, name, claude_session_id, project, branch, status, created_at, updated_at)
		VALUES (7, 'fix-ci', 'uuid-1', 'forge', 'agent/fix-ci', 'dead', 1, 2)`)
	noErr(t, err)

	noErr(t, migrate(ctx, db, migrationsFS, time.Now))

	var created, external bool
	noErr(t, db.QueryRowContext(ctx, `SELECT branch_created, worktree_external FROM sessions WHERE id = 7`).Scan(&created, &external))
	if !created || external {
		t.Errorf("branch_created = %v, worktree_external = %v; want true, false", created, external)
	}
}

func TestSessionOwnership(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	r := s.Sessions()
	x, err := r.Create(ctx, Session{
		Name: "pr-42", ClaudeSessionID: "u1", Project: "forge", Branch: "feature/x",
		Worktree: "/repo", WorktreeExternal: true,
	})
	noErr(t, err)
	got, err := r.Get(ctx, x.ID)
	noErr(t, err)
	if got.BranchCreated || !got.WorktreeExternal {
		t.Fatalf("got %+v", got)
	}
	got.BranchCreated = true
	noErr(t, r.Update(ctx, got))
	if got, _ = r.Get(ctx, x.ID); !got.BranchCreated || !got.WorktreeExternal {
		t.Errorf("Update didn't keep ownership: %+v", got)
	}
	list, err := r.List(ctx, SessionFilter{})
	noErr(t, err)
	if len(list) != 1 || !list[0].WorktreeExternal {
		t.Errorf("List = %+v", list)
	}
}
