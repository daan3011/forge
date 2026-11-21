package store

import (
	"context"
	"database/sql"
	"time"
)

// Project is a repo forge remembers (zero-config projects).
type Project struct {
	Repo     string // owner/name
	Path     string // last known checkout; "" = none
	LastUsed time.Time
	// PlaneProjectID is the linked Plane project; PlaneDecided with an
	// empty id means the repo has none.
	PlaneProjectID string
	PlaneDecided   bool
}

// Projects is the repository of remembered repos.
type Projects struct{ s *Store }

// Projects returns the remembered repos repository.
func (s *Store) Projects() *Projects { return &Projects{s} }

// Touch records that repo was used now, at path when path isn't empty.
func (r *Projects) Touch(ctx context.Context, repo, path string) error {
	_, err := r.s.w.ExecContext(ctx, `INSERT INTO projects (repo, path, last_used_at) VALUES (?, ?, ?)
		ON CONFLICT (repo) DO UPDATE SET
			path = COALESCE(excluded.path, path), last_used_at = excluded.last_used_at`,
		repo, nullStr(path), r.s.now().UnixMilli())
	return wrap("touch project", err)
}

// SetPlane links repo to a Plane project; id "" records that it has none.
func (r *Projects) SetPlane(ctx context.Context, repo, id string) error {
	_, err := r.s.w.ExecContext(ctx, `INSERT INTO projects (repo, plane_project_id, plane_decided) VALUES (?, ?, 1)
		ON CONFLICT (repo) DO UPDATE SET
			plane_project_id = excluded.plane_project_id, plane_decided = 1`,
		repo, nullStr(id))
	return wrap("set project plane link", err)
}

// List returns every remembered repo, most recently used first.
func (r *Projects) List(ctx context.Context) ([]Project, error) {
	rows, err := r.s.r.QueryContext(ctx, `SELECT repo, path, last_used_at, plane_project_id, plane_decided
		FROM projects ORDER BY COALESCE(last_used_at, 0) DESC, repo`)
	if err != nil {
		return nil, wrap("list projects", err)
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var (
			p           Project
			path, plane sql.NullString
			used        sql.NullInt64
		)
		if err := rows.Scan(&p.Repo, &path, &used, &plane, &p.PlaneDecided); err != nil {
			return nil, wrap("list projects", err)
		}
		p.Path, p.PlaneProjectID, p.LastUsed = path.String, plane.String, fromMillis(used)
		out = append(out, p)
	}
	return out, wrap("list projects", rows.Err())
}
