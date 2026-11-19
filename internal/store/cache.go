package store

import (
	"context"
	"database/sql"
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
