package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// CacheEntry is a cached value. Stale entries are still returned so the UI
// can render them immediately and refresh in the background (P0.8).
type CacheEntry struct {
	Key       string
	Kind      string
	Project   string
	Value     []byte
	FetchedAt time.Time
	TTL       time.Duration // 0 = never stale
	Stale     bool
}

// CacheStat summarises the cache for one kind and project (P5.3).
type CacheStat struct {
	Kind    string
	Project string
	Entries int
	Bytes   int64
	Oldest  time.Time
}

// Cache is the key/value cache repository.
type Cache struct{ s *Store }

// Get returns the entry for key, or ErrNotFound.
func (r *Cache) Get(ctx context.Context, key string) (CacheEntry, error) {
	var (
		e            CacheEntry
		fetched, ttl int64
	)
	err := r.s.r.QueryRowContext(ctx,
		`SELECT key, kind, project, value, fetched_at, ttl FROM kv_cache WHERE key = ?`, key).
		Scan(&e.Key, &e.Kind, &e.Project, &e.Value, &fetched, &ttl)
	if err != nil {
		return CacheEntry{}, wrap("cache get", err)
	}
	e.FetchedAt = time.UnixMilli(fetched)
	e.TTL = time.Duration(ttl) * time.Millisecond
	e.Stale = e.TTL > 0 && !r.s.now().Before(e.FetchedAt.Add(e.TTL))
	return e, nil
}

// Put stores value under key, fetched now. ttl 0 means it never goes stale.
func (r *Cache) Put(ctx context.Context, key, kind, project string, value []byte, ttl time.Duration) error {
	if value == nil {
		value = []byte{}
	}
	_, err := r.s.w.ExecContext(ctx, `INSERT INTO kv_cache (key, kind, project, value, fetched_at, ttl)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET kind = excluded.kind, project = excluded.project,
			value = excluded.value, fetched_at = excluded.fetched_at, ttl = excluded.ttl`,
		key, kind, project, value, r.s.now().UnixMilli(), ttl.Milliseconds())
	return wrap("cache put", err)
}

// Delete removes key. Deleting a missing key is not an error.
func (r *Cache) Delete(ctx context.Context, key string) error {
	_, err := r.s.w.ExecContext(ctx, `DELETE FROM kv_cache WHERE key = ?`, key)
	return wrap("cache delete", err)
}

// Clear removes entries of kind (empty = all kinds) for project (empty = all
// projects) and returns how many were removed.
func (r *Cache) Clear(ctx context.Context, kind, project string) (int64, error) {
	res, err := r.s.w.ExecContext(ctx, `DELETE FROM kv_cache
		WHERE (?1 = '' OR kind = ?1) AND (?2 = '' OR project = ?2)`, kind, project)
	if err != nil {
		return 0, wrap("cache clear", err)
	}
	n, err := res.RowsAffected()
	return n, wrap("cache clear", err)
}

// PruneOlderThan removes entries fetched before t.
func (r *Cache) PruneOlderThan(ctx context.Context, t time.Time) (int64, error) {
	res, err := r.s.w.ExecContext(ctx, `DELETE FROM kv_cache WHERE fetched_at < ?`, t.UnixMilli())
	if err != nil {
		return 0, wrap("cache prune", err)
	}
	n, err := res.RowsAffected()
	return n, wrap("cache prune", err)
}

// Stats returns entry counts, sizes and oldest fetch per kind and project.
func (r *Cache) Stats(ctx context.Context) ([]CacheStat, error) {
	rows, err := r.s.r.QueryContext(ctx, `SELECT kind, project, COUNT(*),
		COALESCE(SUM(length(value)), 0), MIN(fetched_at)
		FROM kv_cache GROUP BY kind, project ORDER BY kind, project`)
	if err != nil {
		return nil, wrap("cache stats", err)
	}
	defer rows.Close()
	var out []CacheStat
	for rows.Next() {
		var (
			c      CacheStat
			oldest sql.NullInt64
		)
		if err := rows.Scan(&c.Kind, &c.Project, &c.Entries, &c.Bytes, &oldest); err != nil {
			return nil, wrap("cache stats", err)
		}
		c.Oldest = fromMillis(oldest)
		out = append(out, c)
	}
	return out, wrap("cache stats", rows.Err())
}

// CacheRow describes one cached entry without its value (P5.3).
type CacheRow struct {
	Key       string
	Kind      string
	Project   string
	Bytes     int64
	FetchedAt time.Time
}

// CacheFilter selects cache rows: a row matches when its kind is in Kinds
// and, if KeyPrefixes is set, its key starts with one of them. An empty
// filter matches every row.
type CacheFilter struct {
	Kinds       []string
	KeyPrefixes []string
}

// Rows lists the entries matching f, oldest first, without loading values.
func (r *Cache) Rows(ctx context.Context, f CacheFilter) ([]CacheRow, error) {
	q := `SELECT key, kind, project, length(value), fetched_at FROM kv_cache`
	var (
		where []string
		args  []any
	)
	if len(f.Kinds) > 0 {
		where = append(where, `kind IN (`+placeholders(len(f.Kinds))+`)`)
		for _, k := range f.Kinds {
			args = append(args, k)
		}
	}
	if len(f.KeyPrefixes) > 0 {
		var ors []string
		for _, p := range f.KeyPrefixes {
			ors = append(ors, `substr(key, 1, ?) = ?`)
			args = append(args, len(p), p)
		}
		where = append(where, `(`+strings.Join(ors, ` OR `)+`)`)
	}
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	q += ` ORDER BY fetched_at, key`
	rows, err := r.s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, wrap("cache rows", err)
	}
	defer rows.Close()
	var out []CacheRow
	for rows.Next() {
		var (
			c       CacheRow
			fetched int64
		)
		if err := rows.Scan(&c.Key, &c.Kind, &c.Project, &c.Bytes, &fetched); err != nil {
			return nil, wrap("cache rows", err)
		}
		c.FetchedAt = time.UnixMilli(fetched)
		out = append(out, c)
	}
	return out, wrap("cache rows", rows.Err())
}

// DeleteKeys removes the given keys in one transaction and returns how many
// existed.
func (r *Cache) DeleteKeys(ctx context.Context, keys []string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	tx, err := r.s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, wrap("cache delete keys", err)
	}
	defer func() { _ = tx.Rollback() }()
	var n int64
	for _, k := range keys {
		res, err := tx.ExecContext(ctx, `DELETE FROM kv_cache WHERE key = ?`, k)
		if err != nil {
			return 0, wrap("cache delete keys", err)
		}
		m, err := res.RowsAffected()
		if err != nil {
			return 0, wrap("cache delete keys", err)
		}
		n += m
	}
	return n, wrap("cache delete keys", tx.Commit())
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}
