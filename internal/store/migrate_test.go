package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestOpenIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "nested", "forge.db")
	want, err := loadMigrations(migrationsFS)
	noErr(t, err)

	for i := range 3 {
		s, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
		v, err := s.SchemaVersion(ctx)
		noErr(t, err)
		if v != len(want) {
			t.Errorf("open #%d: version = %d, want %d", i, v, len(want))
		}
		var rows int
		noErr(t, s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_version`).Scan(&rows))
		if rows != len(want) {
			t.Errorf("open #%d: schema_version rows = %d, want %d", i, rows, len(want))
		}
		noErr(t, s.Close())
	}
}

func TestPragmas(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	tests := []struct {
		pragma string
		want   string
	}{
		{"journal_mode", "wal"},
		{"foreign_keys", "1"},
		{"busy_timeout", "5000"},
		{"synchronous", "1"}, // NORMAL
	}
	for _, tt := range tests {
		t.Run(tt.pragma, func(t *testing.T) {
			var got string
			noErr(t, s.r.QueryRowContext(ctx, "PRAGMA "+tt.pragma).Scan(&got))
			if got != tt.want {
				t.Errorf("PRAGMA %s = %q, want %q", tt.pragma, got, tt.want)
			}
		})
	}
}

func TestEmbeddedSchemaHasTables(t *testing.T) {
	s, _ := newTestStore(t)
	for _, table := range []string{"sessions", "session_events", "links", "pins", "kv_cache", "usage"} {
		var n int
		noErr(t, s.r.QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n))
		if n != 1 {
			t.Errorf("table %s missing", table)
		}
	}
}

func TestLoadMigrationsValidation(t *testing.T) {
	file := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
	tests := []struct {
		name    string
		fs      fstest.MapFS
		wantErr string
		wantN   int
	}{
		{
			name:  "ok, sorted regardless of listing",
			fs:    fstest.MapFS{"0002_b.sql": file("x"), "0001_a.sql": file("y")},
			wantN: 2,
		},
		{name: "empty", fs: fstest.MapFS{}, wantN: 0},
		{
			name:    "gap",
			fs:      fstest.MapFS{"0001_a.sql": file(""), "0003_c.sql": file("")},
			wantErr: "expected version 2",
		},
		{
			name:    "duplicate",
			fs:      fstest.MapFS{"0001_a.sql": file(""), "0001_b.sql": file("")},
			wantErr: "duplicate migration version 1",
		},
		{
			name:    "bad name",
			fs:      fstest.MapFS{"1_init.sql": file("")},
			wantErr: "name must look like",
		},
		{
			name:    "not starting at 1",
			fs:      fstest.MapFS{"0002_b.sql": file("")},
			wantErr: "expected version 1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms, err := loadMigrations(tt.fs)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			noErr(t, err)
			if len(ms) != tt.wantN {
				t.Fatalf("got %d migrations, want %d", len(ms), tt.wantN)
			}
			for i, m := range ms {
				if m.version != i+1 {
					t.Errorf("ms[%d].version = %d", i, m.version)
				}
			}
		})
	}
}

func openRaw(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "raw.db")+"?_txlock=immediate")
	noErr(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigrateFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	db := openRaw(t)
	fsys := fstest.MapFS{
		"0001_a.sql": {Data: []byte(`CREATE TABLE a (x INTEGER);`)},
		"0002_b.sql": {Data: []byte(`CREATE TABLE b (x INTEGER); THIS IS NOT SQL;`)},
	}
	err := migrate(ctx, db, fsys, time.Now)
	if err == nil || !strings.Contains(err.Error(), "0002_b.sql") {
		t.Fatalf("err = %v, want failure in 0002_b.sql", err)
	}
	v, err := currentVersion(ctx, db)
	noErr(t, err)
	if v != 1 {
		t.Errorf("version = %d, want 1", v)
	}
	var n int
	noErr(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'b'`).Scan(&n))
	if n != 0 {
		t.Error("table b exists after failed migration")
	}

	// Fixing the migration and re-running picks up where it stopped.
	fsys["0002_b.sql"] = &fstest.MapFile{Data: []byte(`CREATE TABLE b (x INTEGER);`)}
	noErr(t, migrate(ctx, db, fsys, time.Now))
	v, err = currentVersion(ctx, db)
	noErr(t, err)
	if v != 2 {
		t.Errorf("version = %d, want 2", v)
	}
}

func TestMigrateRejectsNewerDatabase(t *testing.T) {
	ctx := context.Background()
	db := openRaw(t)
	full := fstest.MapFS{
		"0001_a.sql": {Data: []byte(`CREATE TABLE a (x INTEGER);`)},
		"0002_b.sql": {Data: []byte(`CREATE TABLE b (x INTEGER);`)},
	}
	noErr(t, migrate(ctx, db, full, time.Now))
	older := fstest.MapFS{"0001_a.sql": full["0001_a.sql"]}
	err := migrate(ctx, db, older, time.Now)
	if err == nil || !strings.Contains(err.Error(), "newer than this forge binary") {
		t.Fatalf("err = %v, want newer-database error", err)
	}
}
