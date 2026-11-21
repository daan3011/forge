package store

import (
	"context"
	"testing"
	"testing/fstest"
	"time"
)

// TestMigration0005LinksGitHubRun upgrades a version-4 database: existing
// links survive the rebuild, and github_run links can be added after it.
func TestMigration0005LinksGitHubRun(t *testing.T) {
	ctx := context.Background()
	db := openRaw(t)
	all, err := loadMigrations(migrationsFS)
	noErr(t, err)
	v4 := fstest.MapFS{}
	for _, m := range all[:4] {
		v4[m.name] = &fstest.MapFile{Data: []byte(m.sql)}
	}
	noErr(t, migrate(ctx, db, v4, time.Now))
	_, err = db.ExecContext(ctx, `INSERT INTO links (id, from_kind, from_id, to_kind, to_id, url, title, state, created_at, updated_at)
		VALUES (3, 'session', '7', 'github_pr', 'a/b#1', 'u', 't', 'open', 1, 2)`)
	noErr(t, err)
	if _, err := db.ExecContext(ctx, `INSERT INTO links (from_kind, from_id, to_kind, to_id, created_at, updated_at)
		VALUES ('session', '7', 'github_run', 'a/b#9', 1, 1)`); err == nil {
		t.Fatal("version 4 accepted github_run")
	}

	noErr(t, migrate(ctx, db, migrationsFS, time.Now))

	var url, title, state string
	var created, updated int64
	noErr(t, db.QueryRowContext(ctx, `SELECT url, title, state, created_at, updated_at FROM links WHERE id = 3`).
		Scan(&url, &title, &state, &created, &updated))
	if url != "u" || title != "t" || state != "open" || created != 1 || updated != 2 {
		t.Errorf("link = %s %s %s %d %d", url, title, state, created, updated)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO links (from_kind, from_id, to_kind, to_id, created_at, updated_at)
		VALUES ('session', '7', 'github_run', 'a/b#9', 1, 1)`)
	noErr(t, err)
	if _, err := db.ExecContext(ctx, `INSERT INTO links (from_kind, from_id, to_kind, to_id, created_at, updated_at)
		VALUES ('session', '7', 'bogus', 'x', 1, 1)`); err == nil {
		t.Error("unknown kind accepted")
	}
	var idx int
	noErr(t, db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'links_to'`).Scan(&idx))
	if idx != 1 {
		t.Error("links_to index missing")
	}
}

func TestLinksGitHubRun(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	l := s.Links()
	run := RunRef("a/b", 9)
	if run.ID != "a/b#9" || run.Kind != KindGitHubRun {
		t.Fatalf("RunRef = %+v", run)
	}
	noErr(t, l.Add(ctx, SessionRef(1), run, LinkMeta{URL: "https://github.com/a/b/actions/runs/9", Title: "CI #4"}))
	got, err := l.For(ctx, SessionRef(1))
	noErr(t, err)
	if len(got) != 1 || got[0].Other != run || got[0].Title != "CI #4" {
		t.Errorf("links = %+v", got)
	}
}
