package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"time"
)

//go:embed migrations/*.sql
var embedded embed.FS

// migrationsFS holds the numbered migrations, rooted so files sit at the top.
var migrationsFS = mustSub(embedded, "migrations")

func mustSub(f fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

type migration struct {
	version int
	name    string
	sql     string
}

var migrationNameRe = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

// loadMigrations reads NNNN_name.sql files from fsys and checks they are
// numbered 1..N without gaps or duplicates.
func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("store: read migrations: %w", err)
	}
	var ms []migration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationNameRe.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("store: migration %q: name must look like 0001_description.sql", e.Name())
		}
		v, _ := strconv.Atoi(m[1])
		body, err := fs.ReadFile(fsys, path.Clean(e.Name()))
		if err != nil {
			return nil, fmt.Errorf("store: read migration %s: %w", e.Name(), err)
		}
		ms = append(ms, migration{version: v, name: e.Name(), sql: string(body)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	for i, m := range ms {
		if want := i + 1; m.version != want {
			if i > 0 && m.version == ms[i-1].version {
				return nil, fmt.Errorf("store: duplicate migration version %d (%s, %s)", m.version, ms[i-1].name, m.name)
			}
			return nil, fmt.Errorf("store: migration %s: expected version %d (no gaps allowed)", m.name, want)
		}
	}
	return ms, nil
}

const schemaVersionDDL = `CREATE TABLE IF NOT EXISTS schema_version (
	version    INTEGER PRIMARY KEY,
	name       TEXT    NOT NULL,
	applied_at INTEGER NOT NULL
)`

func currentVersion(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
},
) (int, error) {
	var v int
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&v); err != nil {
		return 0, fmt.Errorf("store: read schema version: %w", err)
	}
	return v, nil
}

// migrate applies every migration in fsys newer than the database's version.
// Each migration runs in its own transaction with its schema_version row, so
// a failure leaves the database at the previous version. Running it again is
// a no-op.
func migrate(ctx context.Context, db *sql.DB, fsys fs.FS, now func() time.Time) error {
	ms, err := loadMigrations(fsys)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, schemaVersionDDL); err != nil {
		return fmt.Errorf("store: create schema_version: %w", err)
	}
	for _, m := range ms {
		if err := applyMigration(ctx, db, m, len(ms), now); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, m migration, latest int, now func() time.Time) error {
	// _txlock=immediate makes BEGIN take the write lock, so two processes
	// opening a fresh database cannot both apply the same migration.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin migration %s: %w", m.name, err)
	}
	defer func() { _ = tx.Rollback() }()

	cur, err := currentVersion(ctx, tx)
	if err != nil {
		return err
	}
	if cur > latest {
		return fmt.Errorf("store: database schema version %d is newer than this forge binary (%d); upgrade forge", cur, latest)
	}
	if m.version <= cur {
		return nil
	}
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("store: migration %s: %w", m.name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_version (version, name, applied_at) VALUES (?, ?, ?)`,
		m.version, m.name, now().UnixMilli()); err != nil {
		return fmt.Errorf("store: record migration %s: %w", m.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit migration %s: %w", m.name, err)
	}
	return nil
}
