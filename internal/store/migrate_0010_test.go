package store

import (
	"context"
	"testing"
	"testing/fstest"
	"time"
)

// TestMigration0010MCP upgrades a version-9 database: links survive the
// rebuild, the new kinds are accepted and sessions gain the mcp columns.
func TestMigration0010MCP(t *testing.T) {
	ctx := context.Background()
	db := openRaw(t)
	all, err := loadMigrations(migrationsFS)
	noErr(t, err)
	v9 := fstest.MapFS{}
	for _, m := range all[:9] {
		v9[m.name] = &fstest.MapFile{Data: []byte(m.sql)}
	}
	noErr(t, migrate(ctx, db, v9, time.Now))
	_, err = db.ExecContext(ctx, `INSERT INTO links (from_kind, from_id, to_kind, to_id, title, created_at, updated_at)
		VALUES ('session', '1', 'github_pr', 'o/r#1', 'PR', 1, 1)`)
	noErr(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO links (from_kind, from_id, to_kind, to_id, created_at, updated_at)
		VALUES ('session', '1', 'doc', '/x.md', 1, 1)`)
	if err == nil {
		t.Fatal("v9 accepted kind doc")
	}

	noErr(t, migrate(ctx, db, migrationsFS, time.Now))

	var title string
	noErr(t, db.QueryRowContext(ctx, `SELECT title FROM links WHERE to_id = 'o/r#1'`).Scan(&title))
	if title != "PR" {
		t.Errorf("kept link title = %q", title)
	}
	for _, q := range []string{
		`INSERT INTO links (from_kind, from_id, to_kind, to_id, created_at, updated_at) VALUES ('session', '1', 'doc', '/x.md', 1, 1)`,
		`INSERT INTO links (from_kind, from_id, to_kind, to_id, created_at, updated_at) VALUES ('session', '1', 'proposed_issue', '1', 1, 1)`,
		`INSERT INTO proposed_issues (session_name, project, title, created_at, updated_at) VALUES ('s', 'p', 't', 1, 1)`,
		`SELECT progress, progress_at, agent_summary FROM sessions`,
	} {
		_, err = db.ExecContext(ctx, q)
		noErr(t, err)
	}
}
