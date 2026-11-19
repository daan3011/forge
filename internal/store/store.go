package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/daan3011/forge/internal/config"
)

// BusyTimeout is how long a connection waits for a lock held by another
// writer (e.g. `forge hook` while the TUI is writing) before failing.
const BusyTimeout = 5 * time.Second

var (
	// ErrNotFound is returned when a looked-up row does not exist.
	ErrNotFound = errors.New("store: not found")
	// ErrConflict is returned when a write violates a uniqueness constraint.
	ErrConflict = errors.New("store: already exists")
)

// Store is the forge database. It is safe for concurrent use.
//
// Writes go through a single connection (w) so writers in this process queue
// in Go instead of contending for SQLite's lock; only other processes (e.g.
// `forge hook`) hit the busy timeout. Reads use a separate pool (r), which WAL
// lets run alongside the writer.
type Store struct {
	w   *sql.DB
	r   *sql.DB
	now func() time.Time
}

// Option configures Open.
type Option func(*Store)

// WithClock replaces time.Now, for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Store) { s.now = now }
}

// DefaultPath returns $XDG_DATA_HOME/forge/forge.db, or
// ~/.local/share/forge/forge.db.
func DefaultPath() string {
	return filepath.Join(config.DataHome(), "forge", "forge.db")
}

// Open opens (creating if needed) the database at path and applies any
// pending migrations.
func Open(ctx context.Context, path string, opts ...Option) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	w, err := openDB(ctx, path, false)
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	s := &Store{w: w, now: time.Now}
	for _, o := range opts {
		o(s)
	}
	// Migrate before opening the read pool so it never sees a half-built schema.
	if err := migrate(ctx, w, migrationsFS, s.now); err != nil {
		_ = w.Close()
		return nil, err
	}
	if s.r, err = openDB(ctx, path, true); err != nil {
		_ = w.Close()
		return nil, err
	}
	return s, nil
}

func openDB(ctx context.Context, path string, readOnly bool) (*sql.DB, error) {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", BusyTimeout.Milliseconds()))
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	if readOnly {
		q.Add("_pragma", "query_only(1)")
	} else {
		q.Set("_txlock", "immediate")
	}
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	return db, nil
}

// Close closes the database.
func (s *Store) Close() error { return errors.Join(s.r.Close(), s.w.Close()) }

// SchemaVersion returns the highest applied migration version.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	return currentVersion(ctx, s.r)
}

// Vacuum rebuilds the database file, reclaiming free pages.
func (s *Store) Vacuum(ctx context.Context) error {
	if _, err := s.w.ExecContext(ctx, "VACUUM"); err != nil {
		return fmt.Errorf("store: vacuum: %w", err)
	}
	return nil
}

// Sessions returns the sessions repository.
func (s *Store) Sessions() *Sessions { return &Sessions{s} }

// Events returns the session events repository.
func (s *Store) Events() *Events { return &Events{s} }

// Links returns the links repository.
func (s *Store) Links() *Links { return &Links{s} }

// Pins returns the pins repository.
func (s *Store) Pins() *Pins { return &Pins{s} }

// Cache returns the key/value cache repository.
func (s *Store) Cache() *Cache { return &Cache{s} }

// Usage returns the token usage repository.
func (s *Store) Usage() *Usage { return &Usage{s} }

// wrap maps driver errors onto the package's sentinel errors.
func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: %s: %w", op, ErrNotFound)
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return fmt.Errorf("store: %s: %w", op, ErrConflict)
		}
	}
	return fmt.Errorf("store: %s: %w", op, err)
}

// toMillis stores times as unix milliseconds; the zero time becomes NULL.
func toMillis(t time.Time) sql.NullInt64 {
	if t.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.UnixMilli(), Valid: true}
}

// fromMillis is the inverse of toMillis.
func fromMillis(n sql.NullInt64) time.Time {
	if !n.Valid {
		return time.Time{}
	}
	return time.UnixMilli(n.Int64)
}

// nullStr maps "" to NULL.
func nullStr(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// rowsAffected returns ErrNotFound when an update touched nothing.
func mustAffect(op string, res sql.Result, err error) error {
	if err != nil {
		return wrap(op, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return wrap(op, err)
	}
	if n == 0 {
		return fmt.Errorf("store: %s: %w", op, ErrNotFound)
	}
	return nil
}
