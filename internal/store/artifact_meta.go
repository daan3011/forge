package store

import (
	"context"
	"database/sql"
	"time"
)

// ArtifactKind is the pins / tags / titles kind of an artifact; its ref is
// the file path.
const ArtifactKind = "artifact"

// ArtifactLink ties a pinned copy in .forge/artifacts to its original in a
// session worktree (P4.6). While DetachedAt is zero the copy is synced.
type ArtifactLink struct {
	CopyPath     string
	SourcePath   string
	SessionID    int64
	SessionName  string
	SyncedAt     time.Time
	DetachedAt   time.Time
	DetachReason string
}

// Active reports whether the copy is still synced from its original.
func (l ArtifactLink) Active() bool { return l.DetachedAt.IsZero() }

// SetPinned pins or unpins the artifact at path. A pin carries the
// artifact's display title so Home can show pins on their own. Unpinning
// something not pinned is not an error.
func (r *Artifacts) SetPinned(ctx context.Context, path, project string, on bool) error {
	if !on {
		_, err := r.s.w.ExecContext(ctx, `DELETE FROM pins WHERE kind = ? AND ref = ?`, ArtifactKind, path)
		return wrap("unpin artifact", err)
	}
	_, err := r.s.w.ExecContext(ctx, `INSERT INTO pins (kind, ref, project, title, position, created_at)
		VALUES (?, ?, ?, (SELECT title FROM titles WHERE kind = ? AND ref = ?), 0, ?)
		ON CONFLICT (kind, ref) DO UPDATE SET project = excluded.project, title = excluded.title`,
		ArtifactKind, path, project, ArtifactKind, path, r.s.now().UnixMilli())
	return wrap("pin artifact", err)
}

// SetTitle sets the artifact's display title; "" clears it.
func (r *Artifacts) SetTitle(ctx context.Context, path, title string) error {
	tx, err := r.s.w.BeginTx(ctx, nil)
	if err != nil {
		return wrap("set artifact title", err)
	}
	defer func() { _ = tx.Rollback() }()
	if title == "" {
		_, err = tx.ExecContext(ctx, `DELETE FROM titles WHERE kind = ? AND ref = ?`, ArtifactKind, path)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO titles (kind, ref, title) VALUES (?, ?, ?)
			ON CONFLICT (kind, ref) DO UPDATE SET title = excluded.title`, ArtifactKind, path, title)
	}
	if err != nil {
		return wrap("set artifact title", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pins SET title = ? WHERE kind = ? AND ref = ?`,
		nullStr(title), ArtifactKind, path); err != nil {
		return wrap("set artifact title", err)
	}
	return wrap("set artifact title", tx.Commit())
}

// SetTags replaces the artifact's tags (already normalised by the caller).
func (r *Artifacts) SetTags(ctx context.Context, path string, tags []string) error {
	tx, err := r.s.w.BeginTx(ctx, nil)
	if err != nil {
		return wrap("set artifact tags", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM tags WHERE kind = ? AND ref = ?`, ArtifactKind, path); err != nil {
		return wrap("set artifact tags", err)
	}
	for _, tag := range tags {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO tags (kind, ref, tag) VALUES (?, ?, ?)`,
			ArtifactKind, path, tag); err != nil {
			return wrap("set artifact tags", err)
		}
	}
	return wrap("set artifact tags", tx.Commit())
}

// AllTags returns every tag on an indexed artifact, sorted, for completion.
func (r *Artifacts) AllTags(ctx context.Context) ([]string, error) {
	rows, err := r.s.r.QueryContext(ctx, `SELECT DISTINCT t.tag FROM tags t
		JOIN artifacts a ON a.path = t.ref WHERE t.kind = ? ORDER BY t.tag`, ArtifactKind)
	if err != nil {
		return nil, wrap("list artifact tags", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, wrap("list artifact tags", err)
		}
		out = append(out, tag)
	}
	return out, wrap("list artifact tags", rows.Err())
}

// MoveMeta moves the pin, title and tags of from onto to (replacing to's),
// e.g. from a worktree file onto its pinned copy.
func (r *Artifacts) MoveMeta(ctx context.Context, from, to string) error {
	if from == to {
		return nil
	}
	tx, err := r.s.w.BeginTx(ctx, nil)
	if err != nil {
		return wrap("move artifact meta", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`DELETE FROM pins WHERE kind = ? AND ref = ? AND EXISTS (SELECT 1 FROM pins WHERE kind = ? AND ref = ?)`,
		`DELETE FROM titles WHERE kind = ? AND ref = ? AND EXISTS (SELECT 1 FROM titles WHERE kind = ? AND ref = ?)`,
		`DELETE FROM tags WHERE kind = ? AND ref = ? AND EXISTS (SELECT 1 FROM tags WHERE kind = ? AND ref = ?)`,
	} {
		if _, err := tx.ExecContext(ctx, q, ArtifactKind, to, ArtifactKind, from); err != nil {
			return wrap("move artifact meta", err)
		}
	}
	for _, table := range []string{"pins", "titles", "tags"} {
		if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET ref = ? WHERE kind = ? AND ref = ?`,
			to, ArtifactKind, from); err != nil {
			return wrap("move artifact meta", err)
		}
	}
	return wrap("move artifact meta", tx.Commit())
}

// Link records (or replaces) a synced copy, active and synced now.
func (r *Artifacts) Link(ctx context.Context, l ArtifactLink) error {
	synced := l.SyncedAt
	if synced.IsZero() {
		synced = r.s.now()
	}
	_, err := r.s.w.ExecContext(ctx, `INSERT INTO artifact_links
		(copy_path, source_path, session_id, session_name, synced_at, detached_at, detach_reason)
		VALUES (?, ?, ?, ?, ?, NULL, NULL)
		ON CONFLICT (copy_path) DO UPDATE SET
			source_path = excluded.source_path, session_id = excluded.session_id,
			session_name = excluded.session_name, synced_at = excluded.synced_at,
			detached_at = NULL, detach_reason = NULL`,
		l.CopyPath, l.SourcePath, nullID(l.SessionID), l.SessionName, synced.UnixMilli())
	return wrap("link artifact", err)
}

// ActiveLinks returns the copies still synced from their originals.
func (r *Artifacts) ActiveLinks(ctx context.Context) ([]ArtifactLink, error) {
	rows, err := r.s.r.QueryContext(ctx, `SELECT copy_path, source_path, session_id, session_name,
		synced_at, detached_at, detach_reason FROM artifact_links WHERE detached_at IS NULL ORDER BY copy_path`)
	if err != nil {
		return nil, wrap("list artifact links", err)
	}
	defer rows.Close()
	var out []ArtifactLink
	for rows.Next() {
		var (
			l              ArtifactLink
			sid            sql.NullInt64
			synced, detach sql.NullInt64
			reason         sql.NullString
		)
		if err := rows.Scan(&l.CopyPath, &l.SourcePath, &sid, &l.SessionName, &synced, &detach, &reason); err != nil {
			return nil, wrap("list artifact links", err)
		}
		l.SessionID, l.SyncedAt, l.DetachedAt, l.DetachReason = sid.Int64, fromMillis(synced), fromMillis(detach), reason.String
		out = append(out, l)
	}
	return out, wrap("list artifact links", rows.Err())
}

// MarkSynced records that the copy was refreshed from its original.
func (r *Artifacts) MarkSynced(ctx context.Context, copyPath string, at time.Time) error {
	_, err := r.s.w.ExecContext(ctx, `UPDATE artifact_links SET synced_at = ? WHERE copy_path = ?`,
		at.UnixMilli(), copyPath)
	return wrap("mark artifact synced", err)
}

// Detach stops syncing the copy; it stays as the final version. Detaching
// an unlinked or already detached copy is a no-op.
func (r *Artifacts) Detach(ctx context.Context, copyPath, reason string, at time.Time) error {
	_, err := r.s.w.ExecContext(ctx, `UPDATE artifact_links SET detached_at = ?, detach_reason = ?
		WHERE copy_path = ? AND detached_at IS NULL`, at.UnixMilli(), nullStr(reason), copyPath)
	return wrap("detach artifact", err)
}

// Forget removes everything forge knows about the artifacts at paths
// except their pins: index rows, links, titles and tags (P5.3). Callers
// skip pinned artifacts.
func (r *Artifacts) Forget(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	tx, err := r.s.w.BeginTx(ctx, nil)
	if err != nil {
		return wrap("forget artifacts", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, p := range paths {
		for _, q := range []string{
			`DELETE FROM artifacts WHERE path = ?1`,
			`DELETE FROM artifact_links WHERE copy_path = ?1`,
			`DELETE FROM titles WHERE kind = 'artifact' AND ref = ?1`,
			`DELETE FROM tags WHERE kind = 'artifact' AND ref = ?1`,
		} {
			if _, err := tx.ExecContext(ctx, q, p); err != nil {
				return wrap("forget artifacts", err)
			}
		}
	}
	return wrap("forget artifacts", tx.Commit())
}
