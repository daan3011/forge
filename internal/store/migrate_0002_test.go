package store

import (
	"context"
	"testing"
	"testing/fstest"
	"time"
)

// TestMigration0002KeepsData upgrades a version-1 database holding a session
// and its events and checks nothing is lost (DROP TABLE sessions would
// otherwise cascade into session_events) and 'archived' is accepted.
func TestMigration0002KeepsData(t *testing.T) {
	ctx := context.Background()
	db := openRaw(t)
	_, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	noErr(t, err)

	all, err := loadMigrations(migrationsFS)
	noErr(t, err)
	v1 := fstest.MapFS{all[0].name: {Data: []byte(all[0].sql)}}
	noErr(t, migrate(ctx, db, v1, time.Now))

	_, err = db.ExecContext(ctx, `INSERT INTO sessions (id, name, claude_session_id, project, branch, status, created_at, updated_at)
		VALUES (7, 'fix-ci', 'uuid-1', 'forge', 'agent/fix-ci', 'dead', 1, 2)`)
	noErr(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO session_events (session_id, claude_session_id, event, created_at)
		VALUES (7, 'uuid-1', 'Stop', 3), (7, 'uuid-1', 'Notification', 4)`)
	noErr(t, err)
	_, err = db.ExecContext(ctx, `UPDATE sessions SET status = 'archived' WHERE id = 7`)
	if err == nil {
		t.Fatal("version 1 accepted status 'archived'")
	}

	noErr(t, migrate(ctx, db, migrationsFS, time.Now))

	var name, branch, status string
	noErr(t, db.QueryRowContext(ctx, `SELECT name, branch, status FROM sessions WHERE id = 7`).Scan(&name, &branch, &status))
	if name != "fix-ci" || branch != "agent/fix-ci" || status != "dead" {
		t.Errorf("session = %q %q %q", name, branch, status)
	}
	var events int
	noErr(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM session_events WHERE session_id = 7`).Scan(&events))
	if events != 2 {
		t.Errorf("events = %d, want 2", events)
	}
	_, err = db.ExecContext(ctx, `UPDATE sessions SET status = 'archived' WHERE id = 7`)
	noErr(t, err)

	// The foreign key still cascades after the rebuild.
	_, err = db.ExecContext(ctx, `DELETE FROM sessions WHERE id = 7`)
	noErr(t, err)
	noErr(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM session_events`).Scan(&events))
	if events != 0 {
		t.Errorf("events after delete = %d, want 0", events)
	}
	var bad int
	noErr(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&bad))
	if bad != 0 {
		t.Errorf("foreign key violations = %d", bad)
	}
}

func TestSessionArchiveRemoved(t *testing.T) {
	s, c := newTestStore(t)
	ctx := context.Background()
	r := s.Sessions()
	x, err := r.Create(ctx, Session{
		Name: "fix-ci", ClaudeSessionID: "uuid-1", Project: "forge", Branch: "agent/fix-ci",
		Worktree: "/wt/forge/fix-ci", TmuxSession: "forge", TmuxWindow: "fix-ci",
	})
	noErr(t, err)
	noErr(t, s.Links().Add(ctx, SessionRef(x.ID), PRRef("daan/forge", 12), LinkMeta{}))
	c.Advance(time.Minute)
	noErr(t, r.ArchiveRemoved(ctx, x.ID, c.Now()))

	got, err := r.Get(ctx, x.ID)
	noErr(t, err)
	if got.Status != StatusArchived || !got.Archived() || !got.ArchivedAt.Equal(c.Now()) {
		t.Errorf("status=%s archivedAt=%v", got.Status, got.ArchivedAt)
	}
	if got.Worktree != "" || got.TmuxWindow != "" {
		t.Errorf("worktree=%q window=%q, want cleared", got.Worktree, got.TmuxWindow)
	}
	if got.ClaudeSessionID != "uuid-1" || got.Branch != "agent/fix-ci" || got.TmuxSession != "forge" {
		t.Errorf("kept fields changed: %+v", got)
	}
	links, err := s.Links().For(ctx, SessionRef(x.ID))
	noErr(t, err)
	if len(links) != 1 {
		t.Errorf("links = %d, want 1", len(links))
	}
	wantErr(t, r.ArchiveRemoved(ctx, 999, c.Now()), ErrNotFound)
}
