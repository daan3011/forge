package store

import (
	"context"
	"time"
)

// dayLayout is the format of usage.day.
const dayLayout = "2006-01-02"

// Tokens is a token count breakdown.
type Tokens struct {
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	Messages   int64
}

// Total is all tokens combined.
func (t Tokens) Total() int64 { return t.Input + t.Output + t.CacheRead + t.CacheWrite }

// UsageRow is one daily rollup for a session and model (P5.4).
type UsageRow struct {
	Day             time.Time // only the local date is used
	Project         string
	ClaudeSessionID string
	Model           string
	Tokens
}

// DayTotal is usage summed over one day.
type DayTotal struct {
	Day time.Time
	Tokens
}

// ProjectTotal is usage summed per project.
type ProjectTotal struct {
	Project string
	Tokens
}

// SessionTotal is usage summed per Claude Code session.
type SessionTotal struct {
	ClaudeSessionID string
	Project         string
	Tokens
}

// Usage is the token usage repository.
type Usage struct{ s *Store }

// Upsert stores the rollup, replacing any previous totals for the same day,
// project, session and model, so re-aggregating a transcript is idempotent.
func (r *Usage) Upsert(ctx context.Context, u UsageRow) error {
	_, err := r.s.w.ExecContext(ctx, `INSERT INTO usage
		(day, project, claude_session_id, model, input_tokens, output_tokens,
		 cache_read_tokens, cache_write_tokens, messages, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (day, project, claude_session_id, model) DO UPDATE SET
			input_tokens = excluded.input_tokens, output_tokens = excluded.output_tokens,
			cache_read_tokens = excluded.cache_read_tokens,
			cache_write_tokens = excluded.cache_write_tokens,
			messages = excluded.messages, updated_at = excluded.updated_at`,
		u.Day.Format(dayLayout), u.Project, u.ClaudeSessionID, u.Model,
		u.Input, u.Output, u.CacheRead, u.CacheWrite, u.Messages, r.s.now().UnixMilli())
	return wrap("usage upsert", err)
}

const sumCols = `SUM(input_tokens), SUM(output_tokens), SUM(cache_read_tokens),
	SUM(cache_write_tokens), SUM(messages)`

// Daily returns per-day totals from since (inclusive), oldest first.
func (r *Usage) Daily(ctx context.Context, since time.Time) ([]DayTotal, error) {
	rows, err := r.s.r.QueryContext(ctx, `SELECT day, `+sumCols+`
		FROM usage WHERE day >= ? GROUP BY day ORDER BY day`, since.Format(dayLayout))
	if err != nil {
		return nil, wrap("usage daily", err)
	}
	defer rows.Close()
	var out []DayTotal
	for rows.Next() {
		var (
			d   DayTotal
			day string
		)
		if err := rows.Scan(&day, &d.Input, &d.Output, &d.CacheRead, &d.CacheWrite, &d.Messages); err != nil {
			return nil, wrap("usage daily", err)
		}
		if d.Day, err = time.ParseInLocation(dayLayout, day, time.Local); err != nil {
			return nil, wrap("usage daily", err)
		}
		out = append(out, d)
	}
	return out, wrap("usage daily", rows.Err())
}

// ByProject returns per-project totals from since, largest first.
func (r *Usage) ByProject(ctx context.Context, since time.Time) ([]ProjectTotal, error) {
	rows, err := r.s.r.QueryContext(ctx, `SELECT project, `+sumCols+`
		FROM usage WHERE day >= ? GROUP BY project
		ORDER BY SUM(input_tokens + output_tokens + cache_read_tokens + cache_write_tokens) DESC, project`,
		since.Format(dayLayout))
	if err != nil {
		return nil, wrap("usage by project", err)
	}
	defer rows.Close()
	var out []ProjectTotal
	for rows.Next() {
		var p ProjectTotal
		if err := rows.Scan(&p.Project, &p.Input, &p.Output, &p.CacheRead, &p.CacheWrite, &p.Messages); err != nil {
			return nil, wrap("usage by project", err)
		}
		out = append(out, p)
	}
	return out, wrap("usage by project", rows.Err())
}

// TopSessions returns the sessions using the most tokens since the given day.
func (r *Usage) TopSessions(ctx context.Context, since time.Time, limit int) ([]SessionTotal, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := r.s.r.QueryContext(ctx, `SELECT claude_session_id, MAX(project), `+sumCols+`
		FROM usage WHERE day >= ? AND claude_session_id != '' GROUP BY claude_session_id
		ORDER BY SUM(input_tokens + output_tokens + cache_read_tokens + cache_write_tokens) DESC,
			claude_session_id
		LIMIT ?`, since.Format(dayLayout), limit)
	if err != nil {
		return nil, wrap("usage top sessions", err)
	}
	defer rows.Close()
	var out []SessionTotal
	for rows.Next() {
		var s SessionTotal
		if err := rows.Scan(&s.ClaudeSessionID, &s.Project,
			&s.Input, &s.Output, &s.CacheRead, &s.CacheWrite, &s.Messages); err != nil {
			return nil, wrap("usage top sessions", err)
		}
		out = append(out, s)
	}
	return out, wrap("usage top sessions", rows.Err())
}

// SessionTotals returns all-time totals for one Claude Code session.
// A session with no usage returns zero Tokens.
func (r *Usage) SessionTotals(ctx context.Context, claudeSessionID string) (Tokens, error) {
	var t Tokens
	err := r.s.r.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0),
		COALESCE(SUM(cache_read_tokens), 0), COALESCE(SUM(cache_write_tokens), 0),
		COALESCE(SUM(messages), 0)
		FROM usage WHERE claude_session_id = ?`, claudeSessionID).
		Scan(&t.Input, &t.Output, &t.CacheRead, &t.CacheWrite, &t.Messages)
	return t, wrap("usage session totals", err)
}
