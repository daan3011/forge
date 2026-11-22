package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"time"
)

// Artifact is an indexed artifact file (P4.5).
type Artifact struct {
	Path      string
	Project   string
	SessionID int64 // 0 outside a session worktree
	// SessionName is read from the sessions table by List; "" when there is
	// no session or it was deleted.
	SessionName string
	Type        string
	Size        int64
	CreatedAt   time.Time
	ModifiedAt  time.Time

	// P4.6 metadata, filled by List; Sync ignores it.
	Title  string   // display title; "" shows the file name
	Tags   []string // sorted
	Pinned bool
	Link   *ArtifactLink // set for a pinned copy of a worktree file
}

// Artifacts is the repository for the artifact index.
type Artifacts struct{ s *Store }

// Artifacts returns the artifact index repository.
func (s *Store) Artifacts() *Artifacts { return &Artifacts{s} }

// Sync records one scan in a transaction: found files are inserted or
// updated (an existing row keeps its created_at; a new one takes its
// modified time), and rows under roots that weren't found are deleted.
// Rows outside roots are left alone.
func (r *Artifacts) Sync(ctx context.Context, roots []string, found []Artifact) error {
	tx, err := r.s.w.BeginTx(ctx, nil)
	if err != nil {
		return wrap("sync artifacts", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := r.s.now().UnixMilli()
	keep := make(map[string]bool, len(found))
	for _, a := range found {
		keep[a.Path] = true
		created := a.CreatedAt
		if created.IsZero() {
			created = a.ModifiedAt
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO artifacts
			(path, project, session_id, type, size, created_at, modified_at, seen_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (path) DO UPDATE SET
				project = excluded.project, session_id = excluded.session_id, type = excluded.type,
				size = excluded.size, modified_at = excluded.modified_at, seen_at = excluded.seen_at`,
			a.Path, a.Project, nullID(a.SessionID), a.Type, a.Size,
			created.UnixMilli(), a.ModifiedAt.UnixMilli(), now); err != nil {
			return wrap("sync artifacts", err)
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT path FROM artifacts`)
	if err != nil {
		return wrap("sync artifacts", err)
	}
	var gone []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			_ = rows.Close()
			return wrap("sync artifacts", err)
		}
		if !keep[p] && under(p, roots) {
			gone = append(gone, p)
		}
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return wrap("sync artifacts", err)
	}
	for _, p := range gone {
		if _, err := tx.ExecContext(ctx, `DELETE FROM artifacts WHERE path = ?`, p); err != nil {
			return wrap("sync artifacts", err)
		}
	}
	return wrap("sync artifacts", tx.Commit())
}

func under(p string, roots []string) bool {
	for _, r := range roots {
		r = filepath.Clean(r)
		if p == r || strings.HasPrefix(p, r+string(filepath.Separator)) ||
			(r == string(filepath.Separator) && strings.HasPrefix(p, r)) {
			return true
		}
	}
	return false
}

func nullID(id int64) sql.NullInt64 { return sql.NullInt64{Int64: id, Valid: id != 0} }

// List returns the project's artifacts (every project when project is ""),
// newest modified first, with their pins, titles, tags and links.
func (r *Artifacts) List(ctx context.Context, project string) ([]Artifact, error) {
	q := `SELECT a.path, a.project, a.session_id, s.name, a.type, a.size, a.created_at, a.modified_at,
			t.title, p.ref IS NOT NULL,
			l.source_path, l.session_id, l.session_name, l.synced_at, l.detached_at, l.detach_reason
		FROM artifacts a
		LEFT JOIN sessions s ON s.id = a.session_id
		LEFT JOIN titles t ON t.kind = 'artifact' AND t.ref = a.path
		LEFT JOIN pins p ON p.kind = 'artifact' AND p.ref = a.path
		LEFT JOIN artifact_links l ON l.copy_path = a.path`
	var args []any
	if project != "" {
		q += ` WHERE a.project = ?`
		args = append(args, project)
	}
	q += ` ORDER BY a.modified_at DESC, a.path`
	rows, err := r.s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, wrap("list artifacts", err)
	}
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		var (
			a                 Artifact
			sid               sql.NullInt64
			name, title       sql.NullString
			created, modified int64
			lsrc, lname, lwhy sql.NullString
			lsid, lsync, ldet sql.NullInt64
		)
		if err := rows.Scan(&a.Path, &a.Project, &sid, &name, &a.Type, &a.Size, &created, &modified,
			&title, &a.Pinned, &lsrc, &lsid, &lname, &lsync, &ldet, &lwhy); err != nil {
			return nil, wrap("list artifacts", err)
		}
		a.SessionID, a.SessionName, a.Title = sid.Int64, name.String, title.String
		a.CreatedAt, a.ModifiedAt = time.UnixMilli(created), time.UnixMilli(modified)
		if lsrc.Valid {
			a.Link = &ArtifactLink{
				CopyPath: a.Path, SourcePath: lsrc.String, SessionID: lsid.Int64, SessionName: lname.String,
				SyncedAt: fromMillis(lsync), DetachedAt: fromMillis(ldet), DetachReason: lwhy.String,
			}
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list artifacts", err)
	}
	tags, err := r.tagsByPath(ctx)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Tags = tags[out[i].Path]
	}
	return out, nil
}

func (r *Artifacts) tagsByPath(ctx context.Context) (map[string][]string, error) {
	rows, err := r.s.r.QueryContext(ctx, `SELECT ref, tag FROM tags WHERE kind = ? ORDER BY ref, tag`, ArtifactKind)
	if err != nil {
		return nil, wrap("list artifact tags", err)
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var ref, tag string
		if err := rows.Scan(&ref, &tag); err != nil {
			return nil, wrap("list artifact tags", err)
		}
		out[ref] = append(out[ref], tag)
	}
	return out, wrap("list artifact tags", rows.Err())
}

// Delete removes the rows of paths (files that are gone); unknown paths
// are ignored.
func (r *Artifacts) Delete(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	tx, err := r.s.w.BeginTx(ctx, nil)
	if err != nil {
		return wrap("delete artifacts", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, p := range paths {
		if _, err := tx.ExecContext(ctx, `DELETE FROM artifacts WHERE path = ?`, p); err != nil {
			return wrap("delete artifacts", err)
		}
	}
	return wrap("delete artifacts", tx.Commit())
}
