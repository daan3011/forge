package store

import (
	"context"
	"testing"
	"testing/fstest"
	"time"
)

// TestMigration0009UsageLedger upgrades a version-8 database: existing
// rollups are kept and the ledger tables are usable.
func TestMigration0009UsageLedger(t *testing.T) {
	ctx := context.Background()
	db := openRaw(t)
	all, err := loadMigrations(migrationsFS)
	noErr(t, err)
	v8 := fstest.MapFS{}
	for _, m := range all[:8] {
		v8[m.name] = &fstest.MapFile{Data: []byte(m.sql)}
	}
	noErr(t, migrate(ctx, db, v8, time.Now))
	_, err = db.ExecContext(ctx, `INSERT INTO usage (day, project, model, updated_at) VALUES ('2026-11-01', 'p', 'm', 1)`)
	noErr(t, err)

	noErr(t, migrate(ctx, db, migrationsFS, time.Now))

	var n int
	noErr(t, db.QueryRowContext(ctx, `SELECT count(*) FROM usage`).Scan(&n))
	if n != 1 {
		t.Errorf("usage rows = %d, want 1", n)
	}
	for _, q := range []string{
		`INSERT INTO usage_messages (msg_key, file, claude_session_id, day, ts) VALUES ('m', '/f', 's', '2026-11-01', 1)`,
		`INSERT INTO usage_files (path, size, offset, head, head_len, updated_at) VALUES ('/f', 1, 1, -1, 1, 1)`,
	} {
		_, err = db.ExecContext(ctx, q)
		noErr(t, err)
	}
}
