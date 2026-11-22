package store

import (
	"context"
	"database/sql"
	"time"
)

// Event is one Claude Code hook event (P1.4).
type Event struct {
	ID              int64
	SessionID       int64 // 0 when the session is unknown to forge
	ClaudeSessionID string
	Event           string // hook_event_name, e.g. "PreToolUse"
	ToolName        string
	Payload         string // raw hook JSON
	CreatedAt       time.Time
}

// Events is the repository for session hook events.
type Events struct{ s *Store }

const eventCols = `id, session_id, claude_session_id, event, tool_name, payload, created_at`

func scanEvent(sc interface{ Scan(...any) error }) (Event, error) {
	var (
		e             Event
		sid, created  sql.NullInt64
		tool, payload sql.NullString
	)
	if err := sc.Scan(&e.ID, &sid, &e.ClaudeSessionID, &e.Event, &tool, &payload, &created); err != nil {
		return Event{}, err
	}
	e.SessionID, e.ToolName, e.Payload = sid.Int64, tool.String, payload.String
	e.CreatedAt = fromMillis(created)
	return e, nil
}

// Append records an event. If SessionID is 0 it is resolved from
// ClaudeSessionID; events for unknown sessions are kept with no session.
// A zero CreatedAt means now.
func (r *Events) Append(ctx context.Context, e Event) (int64, error) {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = r.s.now()
	}
	sid := sql.NullInt64{Int64: e.SessionID, Valid: e.SessionID != 0}
	res, err := r.s.w.ExecContext(ctx, `INSERT INTO session_events
		(session_id, claude_session_id, event, tool_name, payload, created_at)
		VALUES (COALESCE(?1, (SELECT id FROM sessions WHERE claude_session_id = ?2)), ?2, ?3, ?4, ?5, ?6)`,
		sid, e.ClaudeSessionID, e.Event, nullStr(e.ToolName), nullStr(e.Payload), e.CreatedAt.UnixMilli())
	if err != nil {
		return 0, wrap("append event", err)
	}
	id, err := res.LastInsertId()
	return id, wrap("append event", err)
}

// ListForSession returns the session's most recent events, newest first.
// limit <= 0 returns all.
func (r *Events) ListForSession(ctx context.Context, sessionID int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := r.s.r.QueryContext(ctx, `SELECT `+eventCols+` FROM session_events
		WHERE session_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, sessionID, limit)
	if err != nil {
		return nil, wrap("list events", err)
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, wrap("list events", err)
		}
		out = append(out, e)
	}
	return out, wrap("list events", rows.Err())
}

// Latest returns the newest event for a Claude Code session id.
func (r *Events) Latest(ctx context.Context, claudeSessionID string) (Event, error) {
	row := r.s.r.QueryRowContext(ctx, `SELECT `+eventCols+` FROM session_events
		WHERE claude_session_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`, claudeSessionID)
	e, err := scanEvent(row)
	return e, wrap("latest event", err)
}

// PruneBefore deletes events older than t and returns how many were removed.
func (r *Events) PruneBefore(ctx context.Context, t time.Time) (int64, error) {
	res, err := r.s.w.ExecContext(ctx, `DELETE FROM session_events WHERE created_at < ?`, t.UnixMilli())
	if err != nil {
		return 0, wrap("prune events", err)
	}
	n, err := res.RowsAffected()
	return n, wrap("prune events", err)
}

// BytesBySession returns the summed payload size of each session's events.
func (r *Events) BytesBySession(ctx context.Context) (map[int64]int64, error) {
	rows, err := r.s.r.QueryContext(ctx, `SELECT session_id, COALESCE(SUM(length(payload)), 0)
		FROM session_events WHERE session_id IS NOT NULL GROUP BY session_id`)
	if err != nil {
		return nil, wrap("event bytes", err)
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var id, n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, wrap("event bytes", err)
		}
		out[id] = n
	}
	return out, wrap("event bytes", rows.Err())
}
