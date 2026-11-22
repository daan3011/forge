package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// UsageMessage is one assistant message's usage in the ledger (P5.4).
type UsageMessage struct {
	// Key is the message id, or the request id when there is none.
	Key             string
	ClaudeSessionID string
	Cwd             string
	Model           string
	Time            time.Time
	Tokens          // Messages is ignored
}

// UsageFile is how far one transcript file has been read.
type UsageFile struct {
	Path   string
	Size   int64
	Offset int64
	// Head hashes the first HeadLen bytes of the file.
	Head    uint64
	HeadLen int64
}

// LedgerGroup is the ledger summed per day, session, cwd and model.
type LedgerGroup struct {
	Day             time.Time
	ClaudeSessionID string
	Cwd             string
	Model           string
	Tokens
}

// UsageFile returns the read state of path; ok is false when it was never
// read.
func (r *Usage) UsageFile(ctx context.Context, path string) (f UsageFile, ok bool, err error) {
	var head int64
	err = r.s.r.QueryRowContext(ctx, `SELECT path, size, offset, head, head_len
		FROM usage_files WHERE path = ?`, path).Scan(&f.Path, &f.Size, &f.Offset, &head, &f.HeadLen)
	if errors.Is(err, sql.ErrNoRows) {
		return UsageFile{}, false, nil
	}
	if err != nil {
		return UsageFile{}, false, wrap("usage file", err)
	}
	f.Head = uint64(head) //nolint:gosec // round-trips the stored bits
	return f, true, nil
}

// Ingest records msgs read from f.Path and the file's new read state, in one
// transaction. A message whose key is already in the ledger from another
// file is ignored (the first file keeps it); from the same file it is
// replaced, so the last streamed line of a message wins.
func (r *Usage) Ingest(ctx context.Context, f UsageFile, msgs []UsageMessage) error {
	tx, err := r.s.w.BeginTx(ctx, nil)
	if err != nil {
		return wrap("usage ingest", err)
	}
	defer func() { _ = tx.Rollback() }()
	if len(msgs) > 0 {
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO usage_messages
			(msg_key, file, claude_session_id, cwd, model, day, ts,
			 input_tokens, output_tokens, cache_read_tokens, cache_write_tokens)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (msg_key) DO UPDATE SET
				claude_session_id = excluded.claude_session_id, cwd = excluded.cwd,
				model = excluded.model, day = excluded.day, ts = excluded.ts,
				input_tokens = excluded.input_tokens, output_tokens = excluded.output_tokens,
				cache_read_tokens = excluded.cache_read_tokens,
				cache_write_tokens = excluded.cache_write_tokens
			WHERE usage_messages.file = excluded.file`)
		if err != nil {
			return wrap("usage ingest", err)
		}
		defer stmt.Close()
		for _, m := range msgs {
			if _, err := stmt.ExecContext(ctx, m.Key, f.Path, m.ClaudeSessionID, m.Cwd, m.Model,
				m.Time.In(time.Local).Format(dayLayout), m.Time.UnixMilli(),
				m.Input, m.Output, m.CacheRead, m.CacheWrite); err != nil {
				return wrap("usage ingest", err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO usage_files (path, size, offset, head, head_len, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (path) DO UPDATE SET size = excluded.size, offset = excluded.offset,
			head = excluded.head, head_len = excluded.head_len, updated_at = excluded.updated_at`,
		f.Path, f.Size, f.Offset, int64(f.Head), f.HeadLen, r.s.now().UnixMilli()); err != nil { //nolint:gosec // stores the bits
		return wrap("usage ingest", err)
	}
	return wrap("usage ingest", tx.Commit())
}

// ForgetFile drops the messages read from path and its read state, so a
// replaced or truncated file is read again from the start.
func (r *Usage) ForgetFile(ctx context.Context, path string) error {
	tx, err := r.s.w.BeginTx(ctx, nil)
	if err != nil {
		return wrap("usage forget file", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`DELETE FROM usage_messages WHERE file = ?`,
		`DELETE FROM usage_files WHERE path = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, path); err != nil {
			return wrap("usage forget file", err)
		}
	}
	return wrap("usage forget file", tx.Commit())
}

// LedgerGroups sums the whole ledger per day, session, cwd and model.
func (r *Usage) LedgerGroups(ctx context.Context) ([]LedgerGroup, error) {
	rows, err := r.s.r.QueryContext(ctx, `SELECT day, claude_session_id, cwd, model,
		SUM(input_tokens), SUM(output_tokens), SUM(cache_read_tokens), SUM(cache_write_tokens), COUNT(*)
		FROM usage_messages GROUP BY day, claude_session_id, cwd, model
		ORDER BY day, claude_session_id, cwd, model`)
	if err != nil {
		return nil, wrap("usage ledger groups", err)
	}
	defer rows.Close()
	var out []LedgerGroup
	for rows.Next() {
		var (
			g   LedgerGroup
			day string
		)
		if err := rows.Scan(&day, &g.ClaudeSessionID, &g.Cwd, &g.Model,
			&g.Input, &g.Output, &g.CacheRead, &g.CacheWrite, &g.Messages); err != nil {
			return nil, wrap("usage ledger groups", err)
		}
		if g.Day, err = time.ParseInLocation(dayLayout, day, time.Local); err != nil {
			return nil, wrap("usage ledger groups", err)
		}
		out = append(out, g)
	}
	return out, wrap("usage ledger groups", rows.Err())
}

// ReplaceRollups replaces every daily rollup with rows, in one transaction.
func (r *Usage) ReplaceRollups(ctx context.Context, rows []UsageRow) error {
	tx, err := r.s.w.BeginTx(ctx, nil)
	if err != nil {
		return wrap("usage replace rollups", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM usage`); err != nil {
		return wrap("usage replace rollups", err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO usage
		(day, project, claude_session_id, model, input_tokens, output_tokens,
		 cache_read_tokens, cache_write_tokens, messages, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (day, project, claude_session_id, model) DO UPDATE SET
			input_tokens = input_tokens + excluded.input_tokens,
			output_tokens = output_tokens + excluded.output_tokens,
			cache_read_tokens = cache_read_tokens + excluded.cache_read_tokens,
			cache_write_tokens = cache_write_tokens + excluded.cache_write_tokens,
			messages = messages + excluded.messages`)
	if err != nil {
		return wrap("usage replace rollups", err)
	}
	defer stmt.Close()
	now := r.s.now().UnixMilli()
	for _, u := range rows {
		if _, err := stmt.ExecContext(ctx, u.Day.Format(dayLayout), u.Project, u.ClaudeSessionID, u.Model,
			u.Input, u.Output, u.CacheRead, u.CacheWrite, u.Messages, now); err != nil {
			return wrap("usage replace rollups", err)
		}
	}
	return wrap("usage replace rollups", tx.Commit())
}

// Rows returns the daily rollups from since (inclusive), per model; an empty
// claudeSessionID means every session.
func (r *Usage) Rows(ctx context.Context, since time.Time, claudeSessionID string) ([]UsageRow, error) {
	q := `SELECT day, project, claude_session_id, model, input_tokens, output_tokens,
		cache_read_tokens, cache_write_tokens, messages FROM usage WHERE day >= ?`
	args := []any{since.Format(dayLayout)}
	if claudeSessionID != "" {
		q += ` AND claude_session_id = ?`
		args = append(args, claudeSessionID)
	}
	rows, err := r.s.r.QueryContext(ctx, q+` ORDER BY day, project, claude_session_id, model`, args...)
	if err != nil {
		return nil, wrap("usage rows", err)
	}
	defer rows.Close()
	var out []UsageRow
	for rows.Next() {
		var (
			u   UsageRow
			day string
		)
		if err := rows.Scan(&day, &u.Project, &u.ClaudeSessionID, &u.Model,
			&u.Input, &u.Output, &u.CacheRead, &u.CacheWrite, &u.Messages); err != nil {
			return nil, wrap("usage rows", err)
		}
		if u.Day, err = time.ParseInLocation(dayLayout, day, time.Local); err != nil {
			return nil, wrap("usage rows", err)
		}
		out = append(out, u)
	}
	return out, wrap("usage rows", rows.Err())
}
