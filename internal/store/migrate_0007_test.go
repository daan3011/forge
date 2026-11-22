package store

import (
	"context"
	"testing"
	"testing/fstest"
	"time"
)

// TestMigration0007Artifacts upgrades a version-6 database: existing data
// is untouched and the artifacts table is usable.
func TestMigration0007Artifacts(t *testing.T) {
	ctx := context.Background()
	db := openRaw(t)
	all, err := loadMigrations(migrationsFS)
	noErr(t, err)
	v6 := fstest.MapFS{}
	for _, m := range all[:6] {
		v6[m.name] = &fstest.MapFile{Data: []byte(m.sql)}
	}
	noErr(t, migrate(ctx, db, v6, time.Now))
	_, err = db.ExecContext(ctx, `INSERT INTO projects (repo, path) VALUES ('a/b', '/x')`)
	noErr(t, err)

	noErr(t, migrate(ctx, db, migrationsFS, time.Now))

	var path string
	noErr(t, db.QueryRowContext(ctx, `SELECT path FROM projects WHERE repo = 'a/b'`).Scan(&path))
	if path != "/x" {
		t.Errorf("project path = %q", path)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO artifacts (path, project, type, size, created_at, modified_at, seen_at)
		VALUES ('/x/a.html', 'b', 'html', 1, 1, 1, 1)`)
	noErr(t, err)
	var idx int
	noErr(t, db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'artifacts_project'`).Scan(&idx))
	if idx != 1 {
		t.Error("artifacts_project index missing")
	}
}
