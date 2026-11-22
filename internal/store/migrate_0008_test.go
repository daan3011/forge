package store

import (
	"context"
	"testing"
	"testing/fstest"
	"time"
)

// TestMigration0008ArtifactMeta upgrades a version-7 database: indexed
// artifacts and pins are untouched and the new tables are usable.
func TestMigration0008ArtifactMeta(t *testing.T) {
	ctx := context.Background()
	db := openRaw(t)
	all, err := loadMigrations(migrationsFS)
	noErr(t, err)
	v7 := fstest.MapFS{}
	for _, m := range all[:7] {
		v7[m.name] = &fstest.MapFile{Data: []byte(m.sql)}
	}
	noErr(t, migrate(ctx, db, v7, time.Now))
	_, err = db.ExecContext(ctx, `INSERT INTO artifacts (path, project, type, size, created_at, modified_at, seen_at)
		VALUES ('/x/a.html', 'b', 'html', 1, 1, 1, 1)`)
	noErr(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO pins (kind, ref, created_at) VALUES ('artifact', '/x/a.html', 1)`)
	noErr(t, err)

	noErr(t, migrate(ctx, db, migrationsFS, time.Now))

	var n int
	noErr(t, db.QueryRowContext(ctx, `SELECT count(*) FROM artifacts a JOIN pins p ON p.ref = a.path`).Scan(&n))
	if n != 1 {
		t.Errorf("pinned artifacts = %d, want 1", n)
	}
	for _, q := range []string{
		`INSERT INTO tags (kind, ref, tag) VALUES ('artifact', '/x/a.html', 'design')`,
		`INSERT INTO titles (kind, ref, title) VALUES ('artifact', '/x/a.html', 'Mock')`,
		`INSERT INTO artifact_links (copy_path, source_path) VALUES ('/x/.forge/artifacts/a.html', '/w/a.html')`,
	} {
		_, err = db.ExecContext(ctx, q)
		noErr(t, err)
	}
	noErr(t, db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'tags_by_tag'`).Scan(&n))
	if n != 1 {
		t.Error("tags_by_tag index missing")
	}
}
