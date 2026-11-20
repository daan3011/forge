package store

import (
	"context"
	"testing"
	"testing/fstest"
	"time"
)

// TestMigration0004Default upgrades a version-3 database: no session was
// restored from the archive before P1.10.
func TestMigration0004Default(t *testing.T) {
	ctx := context.Background()
	db := openRaw(t)
	all, err := loadMigrations(migrationsFS)
	noErr(t, err)
	v3 := fstest.MapFS{}
	for _, m := range all[:3] {
		v3[m.name] = &fstest.MapFile{Data: []byte(m.sql)}
	}
	noErr(t, migrate(ctx, db, v3, time.Now))
	_, err = db.ExecContext(ctx, `INSERT INTO sessions (id, name, claude_session_id, project, status, created_at, updated_at)
		VALUES (7, 'fix-ci', 'uuid-1', 'forge', 'idle', 1, 2)`)
	noErr(t, err)

	noErr(t, migrate(ctx, db, migrationsFS, time.Now))

	var off bool
	noErr(t, db.QueryRowContext(ctx, `SELECT auto_archive_off FROM sessions WHERE id = 7`).Scan(&off))
	if off {
		t.Error("auto_archive_off = true, want false")
	}
}

func TestUnarchive(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name       string
		removed    bool // worktree removed (status archived)
		wantStatus Status
	}{
		{"worktree kept", false, StatusIdle},
		{"worktree removed", true, StatusDead},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, c := newTestStore(t)
			r := s.Sessions()
			x, err := r.Create(ctx, Session{Name: "a", ClaudeSessionID: "u", Project: "p", Status: StatusIdle, Notes: "n"})
			noErr(t, err)
			noErr(t, r.Archive(ctx, x.ID, "sum", c.Now()))
			first := c.Now()
			if tt.removed {
				c.Advance(time.Hour)
				noErr(t, r.ArchiveRemoved(ctx, x.ID, c.Now()))
				got, err := r.Get(ctx, x.ID)
				noErr(t, err)
				if !got.ArchivedAt.Equal(first) {
					t.Errorf("ArchiveRemoved moved archived_at to %v, want %v", got.ArchivedAt, first)
				}
			}
			noErr(t, r.Unarchive(ctx, x.ID))
			got, err := r.Get(ctx, x.ID)
			noErr(t, err)
			if got.Archived() || !got.AutoArchiveOff || got.Status != tt.wantStatus {
				t.Errorf("archived=%v off=%v status=%s; want false true %s", got.Archived(), got.AutoArchiveOff, got.Status, tt.wantStatus)
			}
			if got.Summary != "sum" || got.Notes != "n" {
				t.Errorf("summary=%q notes=%q, want kept", got.Summary, got.Notes)
			}
		})
	}
	s, _ := newTestStore(t)
	wantErr(t, s.Sessions().Unarchive(ctx, 999), ErrNotFound)
}
