package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Pin is a pinned item, e.g. an artifact (kind "artifact", ref = its path).
type Pin struct {
	Kind      string
	Ref       string
	Project   string
	Title     string
	Position  int
	CreatedAt time.Time
}

// Pins is the repository for pinned items.
type Pins struct{ s *Store }

// Pin adds p, or updates its project, title and position if already pinned.
// The original pin time is kept.
func (r *Pins) Pin(ctx context.Context, p Pin) error {
	if p.CreatedAt.IsZero() {
		p.CreatedAt = r.s.now()
	}
	_, err := r.s.w.ExecContext(ctx, `INSERT INTO pins (kind, ref, project, title, position, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (kind, ref) DO UPDATE SET
			project = excluded.project, title = excluded.title, position = excluded.position`,
		p.Kind, p.Ref, p.Project, nullStr(p.Title), p.Position, p.CreatedAt.UnixMilli())
	return wrap("pin", err)
}

// Unpin removes a pin. It returns ErrNotFound if it was not pinned.
func (r *Pins) Unpin(ctx context.Context, kind, ref string) error {
	res, err := r.s.w.ExecContext(ctx, `DELETE FROM pins WHERE kind = ? AND ref = ?`, kind, ref)
	return mustAffect("unpin", res, err)
}

// IsPinned reports whether the item is pinned.
func (r *Pins) IsPinned(ctx context.Context, kind, ref string) (bool, error) {
	var n int
	err := r.s.r.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pins WHERE kind = ? AND ref = ?`, kind, ref).Scan(&n)
	return n > 0, wrap("is pinned", err)
}

// List returns pins ordered by position, then pin time. Empty kind or
// project match everything.
func (r *Pins) List(ctx context.Context, kind, project string) ([]Pin, error) {
	var (
		where []string
		args  []any
	)
	if kind != "" {
		where = append(where, "kind = ?")
		args = append(args, kind)
	}
	if project != "" {
		where = append(where, "project = ?")
		args = append(args, project)
	}
	q := `SELECT kind, ref, project, title, position, created_at FROM pins`
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, " AND ")
	}
	q += ` ORDER BY position, created_at, kind, ref`
	rows, err := r.s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, wrap("list pins", err)
	}
	defer rows.Close()
	var out []Pin
	for rows.Next() {
		var (
			p       Pin
			title   sql.NullString
			created sql.NullInt64
		)
		if err := rows.Scan(&p.Kind, &p.Ref, &p.Project, &title, &p.Position, &created); err != nil {
			return nil, wrap("list pins", err)
		}
		p.Title, p.CreatedAt = title.String, fromMillis(created)
		out = append(out, p)
	}
	return out, wrap("list pins", rows.Err())
}
