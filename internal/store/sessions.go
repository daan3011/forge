package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Status is an agent session's live status.
type Status string

// Session statuses (P1.4 status machine).
const (
	StatusStarting Status = "starting"
	StatusWorking  Status = "working"
	StatusWaiting  Status = "waiting"
	StatusIdle     Status = "idle"
	StatusError    Status = "error"
	StatusDead     Status = "dead"
	// StatusArchived is a killed session whose worktree was removed. Its
	// uuid, branch and links are kept so it can be resumed by recreating the
	// worktree.
	StatusArchived Status = "archived"
)

// Session is a named Claude Code agent session.
type Session struct {
	ID                int64
	Name              string
	ClaudeSessionID   string
	Project           string
	Repo              string
	BaseBranch        string
	Branch            string
	Worktree          string
	TmuxSession       string
	TmuxWindow        string
	Template          string
	Status            Status
	StatusChangedAt   time.Time
	LastActivityAt    time.Time
	TranscriptPath    string
	TranscriptOffset  int64
	WaitingNotifiedAt time.Time
	Notes             string
	Summary           string
	ArchivedAt        time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Archived reports whether the session is in the archive.
func (s Session) Archived() bool { return !s.ArchivedAt.IsZero() }

// Sessions is the repository for agent sessions.
type Sessions struct{ s *Store }

// sessionInsertCols are the columns written by Create, in order.
const sessionInsertCols = `name, claude_session_id, project, repo, base_branch, branch, worktree,
	tmux_session, tmux_window, template, status, status_changed_at, last_activity_at,
	transcript_path, transcript_offset, waiting_notified_at, notes, summary, archived_at,
	created_at, updated_at`

const sessionCols = `id, ` + sessionInsertCols

func scanSession(sc interface{ Scan(...any) error }) (Session, error) {
	var (
		x                                                            Session
		repo, base, branch, wt, tsess, twin, tmpl, tpath, notes, sum sql.NullString
		statusAt, activity, notified, archived, created, updated     sql.NullInt64
	)
	err := sc.Scan(&x.ID, &x.Name, &x.ClaudeSessionID, &x.Project, &repo, &base, &branch, &wt,
		&tsess, &twin, &tmpl, &x.Status, &statusAt, &activity,
		&tpath, &x.TranscriptOffset, &notified, &notes, &sum, &archived,
		&created, &updated)
	if err != nil {
		return Session{}, err
	}
	x.Repo, x.BaseBranch, x.Branch, x.Worktree = repo.String, base.String, branch.String, wt.String
	x.TmuxSession, x.TmuxWindow, x.Template = tsess.String, twin.String, tmpl.String
	x.TranscriptPath, x.Notes, x.Summary = tpath.String, notes.String, sum.String
	x.StatusChangedAt, x.LastActivityAt = fromMillis(statusAt), fromMillis(activity)
	x.WaitingNotifiedAt, x.ArchivedAt = fromMillis(notified), fromMillis(archived)
	x.CreatedAt, x.UpdatedAt = fromMillis(created), fromMillis(updated)
	return x, nil
}

// Create inserts a new session and returns it with ID and timestamps set.
// It returns ErrConflict if the name or Claude session id is taken.
func (r *Sessions) Create(ctx context.Context, x Session) (Session, error) {
	now := r.s.now()
	if x.Status == "" {
		x.Status = StatusStarting
	}
	if x.StatusChangedAt.IsZero() {
		x.StatusChangedAt = now
	}
	if x.LastActivityAt.IsZero() {
		x.LastActivityAt = now
	}
	x.CreatedAt, x.UpdatedAt = now, now
	res, err := r.s.w.ExecContext(ctx, `INSERT INTO sessions (`+sessionInsertCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		x.Name, x.ClaudeSessionID, x.Project, nullStr(x.Repo), nullStr(x.BaseBranch),
		nullStr(x.Branch), nullStr(x.Worktree), nullStr(x.TmuxSession), nullStr(x.TmuxWindow),
		nullStr(x.Template), x.Status, toMillis(x.StatusChangedAt), toMillis(x.LastActivityAt),
		nullStr(x.TranscriptPath), x.TranscriptOffset, toMillis(x.WaitingNotifiedAt),
		nullStr(x.Notes), nullStr(x.Summary), toMillis(x.ArchivedAt),
		toMillis(x.CreatedAt), toMillis(x.UpdatedAt))
	if err != nil {
		return Session{}, wrap("create session", err)
	}
	if x.ID, err = res.LastInsertId(); err != nil {
		return Session{}, wrap("create session", err)
	}
	return x, nil
}

func (r *Sessions) getBy(ctx context.Context, col string, v any) (Session, error) {
	row := r.s.r.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM sessions WHERE `+col+` = ?`, v)
	x, err := scanSession(row)
	return x, wrap("get session", err)
}

// Get returns the session with the given id.
func (r *Sessions) Get(ctx context.Context, id int64) (Session, error) {
	return r.getBy(ctx, "id", id)
}

// GetByName returns the session with the given name.
func (r *Sessions) GetByName(ctx context.Context, name string) (Session, error) {
	return r.getBy(ctx, "name", name)
}

// GetByClaudeID returns the session with the given Claude Code session id.
func (r *Sessions) GetByClaudeID(ctx context.Context, claudeID string) (Session, error) {
	return r.getBy(ctx, "claude_session_id", claudeID)
}

// SessionFilter narrows List. Zero values match everything.
type SessionFilter struct {
	Project  string
	Archived *bool
}

// List returns sessions, waiting ones first, then most recently active.
func (r *Sessions) List(ctx context.Context, f SessionFilter) ([]Session, error) {
	var (
		where []string
		args  []any
	)
	if f.Project != "" {
		where = append(where, "project = ?")
		args = append(args, f.Project)
	}
	if f.Archived != nil {
		if *f.Archived {
			where = append(where, "archived_at IS NOT NULL")
		} else {
			where = append(where, "archived_at IS NULL")
		}
	}
	q := `SELECT ` + sessionCols + ` FROM sessions`
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, " AND ")
	}
	q += ` ORDER BY status = 'waiting' DESC, last_activity_at DESC, id DESC`
	rows, err := r.s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, wrap("list sessions", err)
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		x, err := scanSession(rows)
		if err != nil {
			return nil, wrap("list sessions", err)
		}
		out = append(out, x)
	}
	return out, wrap("list sessions", rows.Err())
}

// Update writes every editable field of x (matched by ID) and bumps
// updated_at. Status and archive state have dedicated methods but are also
// written here as given.
func (r *Sessions) Update(ctx context.Context, x Session) error {
	res, err := r.s.w.ExecContext(ctx, `UPDATE sessions SET
		name = ?, claude_session_id = ?, project = ?, repo = ?, base_branch = ?, branch = ?,
		worktree = ?, tmux_session = ?, tmux_window = ?, template = ?, status = ?,
		status_changed_at = ?, last_activity_at = ?, transcript_path = ?, transcript_offset = ?,
		waiting_notified_at = ?, notes = ?, summary = ?, archived_at = ?, updated_at = ?
		WHERE id = ?`,
		x.Name, x.ClaudeSessionID, x.Project, nullStr(x.Repo), nullStr(x.BaseBranch),
		nullStr(x.Branch), nullStr(x.Worktree), nullStr(x.TmuxSession), nullStr(x.TmuxWindow),
		nullStr(x.Template), x.Status, toMillis(x.StatusChangedAt), toMillis(x.LastActivityAt),
		nullStr(x.TranscriptPath), x.TranscriptOffset, toMillis(x.WaitingNotifiedAt),
		nullStr(x.Notes), nullStr(x.Summary), toMillis(x.ArchivedAt),
		r.s.now().UnixMilli(), x.ID)
	return mustAffect("update session", res, err)
}

// SetStatus records a status at time at. It always bumps last activity; when
// the status actually changes it also resets status_changed_at and clears the
// waiting-notification debounce.
func (r *Sessions) SetStatus(ctx context.Context, id int64, st Status, at time.Time) error {
	ms := at.UnixMilli()
	res, err := r.s.w.ExecContext(ctx, `UPDATE sessions SET
		status_changed_at   = CASE WHEN status = ?1 THEN status_changed_at ELSE ?2 END,
		waiting_notified_at = CASE WHEN status = ?1 THEN waiting_notified_at ELSE NULL END,
		status = ?1, last_activity_at = ?2, updated_at = ?3
		WHERE id = ?4`, st, ms, r.s.now().UnixMilli(), id)
	return mustAffect("set session status", res, err)
}

// SetStatusIf is SetStatus applied only while the session is still in
// status from. It reports whether the row changed, so a caller acting on a
// stale view can't overwrite a status a hook has just written.
func (r *Sessions) SetStatusIf(ctx context.Context, id int64, from, to Status, at time.Time) (bool, error) {
	ms := at.UnixMilli()
	res, err := r.s.w.ExecContext(ctx, `UPDATE sessions SET
		status_changed_at   = CASE WHEN status = ?1 THEN status_changed_at ELSE ?2 END,
		waiting_notified_at = CASE WHEN status = ?1 THEN waiting_notified_at ELSE NULL END,
		status = ?1, last_activity_at = ?2, updated_at = ?3
		WHERE id = ?4 AND status = ?5`, to, ms, r.s.now().UnixMilli(), id, from)
	if err != nil {
		return false, wrap("set session status", err)
	}
	n, err := res.RowsAffected()
	return n > 0, wrap("set session status", err)
}

// SetClaudeID records a new Claude Code session id for the session, e.g.
// after /clear started a fresh conversation in the same window. It returns
// ErrConflict if another session already has the id.
func (r *Sessions) SetClaudeID(ctx context.Context, id int64, claudeID string) error {
	res, err := r.s.w.ExecContext(ctx,
		`UPDATE sessions SET claude_session_id = ?, updated_at = ? WHERE id = ?`,
		claudeID, r.s.now().UnixMilli(), id)
	return mustAffect("set claude session id", res, err)
}

// MarkWaitingNotified records that a "waiting" notification was sent (P1.8).
func (r *Sessions) MarkWaitingNotified(ctx context.Context, id int64, at time.Time) error {
	res, err := r.s.w.ExecContext(ctx,
		`UPDATE sessions SET waiting_notified_at = ?, updated_at = ? WHERE id = ?`,
		at.UnixMilli(), r.s.now().UnixMilli(), id)
	return mustAffect("mark session notified", res, err)
}

// SetTranscript stores the transcript path and the parsed byte offset (P1.6).
func (r *Sessions) SetTranscript(ctx context.Context, id int64, path string, offset int64) error {
	res, err := r.s.w.ExecContext(ctx,
		`UPDATE sessions SET transcript_path = ?, transcript_offset = ?, updated_at = ? WHERE id = ?`,
		nullStr(path), offset, r.s.now().UnixMilli(), id)
	return mustAffect("set session transcript", res, err)
}

// Archive moves the session to the archive with a summary (P1.10). Notes are kept.
func (r *Sessions) Archive(ctx context.Context, id int64, summary string, at time.Time) error {
	res, err := r.s.w.ExecContext(ctx,
		`UPDATE sessions SET archived_at = ?, summary = ?, updated_at = ? WHERE id = ?`,
		at.UnixMilli(), nullStr(summary), r.s.now().UnixMilli(), id)
	return mustAffect("archive session", res, err)
}

// ArchiveRemoved records that a killed session's worktree was removed: the
// status becomes archived, archived_at is set and the worktree path and tmux
// window are cleared. The uuid, branch, links and everything else are kept.
func (r *Sessions) ArchiveRemoved(ctx context.Context, id int64, at time.Time) error {
	ms := at.UnixMilli()
	res, err := r.s.w.ExecContext(ctx, `UPDATE sessions SET
		status = ?1, status_changed_at = ?2, archived_at = ?2,
		worktree = NULL, tmux_window = NULL, waiting_notified_at = NULL, updated_at = ?3
		WHERE id = ?4`, StatusArchived, ms, r.s.now().UnixMilli(), id)
	return mustAffect("archive removed session", res, err)
}

// Unarchive moves the session back to the active list.
func (r *Sessions) Unarchive(ctx context.Context, id int64) error {
	res, err := r.s.w.ExecContext(ctx,
		`UPDATE sessions SET archived_at = NULL, updated_at = ? WHERE id = ?`,
		r.s.now().UnixMilli(), id)
	return mustAffect("unarchive session", res, err)
}

// Delete removes the session, its events and its links.
func (r *Sessions) Delete(ctx context.Context, id int64) error {
	tx, err := r.s.w.BeginTx(ctx, nil)
	if err != nil {
		return wrap("delete session", err)
	}
	defer func() { _ = tx.Rollback() }()
	ref := SessionRef(id)
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM links WHERE (from_kind = ?1 AND from_id = ?2) OR (to_kind = ?1 AND to_id = ?2)`,
		ref.Kind, ref.ID); err != nil {
		return wrap("delete session links", err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	if err := mustAffect("delete session", res, err); err != nil {
		return err
	}
	return wrap("delete session", tx.Commit())
}
