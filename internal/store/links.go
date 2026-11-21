package store

import (
	"context"
	"database/sql"
	"strconv"
	"time"
)

// LinkKind is the type of entity at one end of a link.
type LinkKind string

// Link kinds.
const (
	KindSession    LinkKind = "session"
	KindPlaneIssue LinkKind = "plane_issue"
	KindGitHubPR   LinkKind = "github_pr"
	KindGitHubRun  LinkKind = "github_run"
)

// Ref identifies a linkable entity. IDs: sessions use their numeric id,
// PRs "owner/repo#123", Plane issues their item id or key.
type Ref struct {
	Kind LinkKind
	ID   string
}

// SessionRef returns the Ref for a forge session.
func SessionRef(id int64) Ref { return Ref{Kind: KindSession, ID: strconv.FormatInt(id, 10)} }

// PRRef returns the Ref for a GitHub pull request.
func PRRef(repo string, number int) Ref {
	return Ref{Kind: KindGitHubPR, ID: repo + "#" + strconv.Itoa(number)}
}

// RunRef returns the Ref for a GitHub Actions workflow run (P2.8).
func RunRef(repo string, runID int64) Ref {
	return Ref{Kind: KindGitHubRun, ID: repo + "#" + strconv.FormatInt(runID, 10)}
}

// IssueRef returns the Ref for a Plane work item.
func IssueRef(id string) Ref { return Ref{Kind: KindPlaneIssue, ID: id} }

// LinkMeta describes the "to" end of a link, cached so detail panes can show
// it without fetching.
type LinkMeta struct {
	URL   string
	Title string
	State string // e.g. open, merged, closed
}

// Link is a link seen from one entity: Other is the far end.
type Link struct {
	Other Ref
	LinkMeta
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Links is the repository for session/issue/PR links.
type Links struct{ s *Store }

// Add links from to to, or refreshes the metadata of an existing link.
// Links are undirected for lookups; meta describes to.
func (r *Links) Add(ctx context.Context, from, to Ref, meta LinkMeta) error {
	now := r.s.now().UnixMilli()
	_, err := r.s.w.ExecContext(ctx, `INSERT INTO links
		(from_kind, from_id, to_kind, to_id, url, title, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (from_kind, from_id, to_kind, to_id) DO UPDATE SET
			url = excluded.url, title = excluded.title, state = excluded.state,
			updated_at = excluded.updated_at`,
		from.Kind, from.ID, to.Kind, to.ID,
		nullStr(meta.URL), nullStr(meta.Title), nullStr(meta.State), now, now)
	return wrap("add link", err)
}

// Remove deletes the link between a and b in either direction.
func (r *Links) Remove(ctx context.Context, a, b Ref) error {
	res, err := r.s.w.ExecContext(ctx, `DELETE FROM links WHERE
		(from_kind = ?1 AND from_id = ?2 AND to_kind = ?3 AND to_id = ?4) OR
		(from_kind = ?3 AND from_id = ?4 AND to_kind = ?1 AND to_id = ?2)`,
		a.Kind, a.ID, b.Kind, b.ID)
	return mustAffect("remove link", res, err)
}

// For returns every link touching ref, in either direction, oldest first.
func (r *Links) For(ctx context.Context, ref Ref) ([]Link, error) {
	rows, err := r.s.r.QueryContext(ctx, `
		SELECT to_kind, to_id, url, title, state, created_at, updated_at
			FROM links WHERE from_kind = ?1 AND from_id = ?2
		UNION ALL
		SELECT from_kind, from_id, NULL, NULL, NULL, created_at, updated_at
			FROM links WHERE to_kind = ?1 AND to_id = ?2
		ORDER BY 6, 1, 2`, ref.Kind, ref.ID)
	if err != nil {
		return nil, wrap("list links", err)
	}
	defer rows.Close()
	var out []Link
	for rows.Next() {
		var (
			l                Link
			u, title, state  sql.NullString
			created, updated sql.NullInt64
		)
		if err := rows.Scan(&l.Other.Kind, &l.Other.ID, &u, &title, &state, &created, &updated); err != nil {
			return nil, wrap("list links", err)
		}
		l.URL, l.Title, l.State = u.String, title.String, state.String
		l.CreatedAt, l.UpdatedAt = fromMillis(created), fromMillis(updated)
		out = append(out, l)
	}
	return out, wrap("list links", rows.Err())
}

// LinkedSessions returns the ids of sessions linked to the given entity.
func (r *Links) LinkedSessions(ctx context.Context, ref Ref) ([]int64, error) {
	links, err := r.For(ctx, ref)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, l := range links {
		if l.Other.Kind != KindSession {
			continue
		}
		id, err := strconv.ParseInt(l.Other.ID, 10, 64)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// SetState updates the cached state of ref on every link pointing at it,
// e.g. when a PR is seen merged.
func (r *Links) SetState(ctx context.Context, ref Ref, state string) error {
	_, err := r.s.w.ExecContext(ctx,
		`UPDATE links SET state = ?, updated_at = ? WHERE to_kind = ? AND to_id = ?`,
		nullStr(state), r.s.now().UnixMilli(), ref.Kind, ref.ID)
	return wrap("set link state", err)
}
