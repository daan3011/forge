package store

import (
	"context"
	"errors"
	"time"
)

// Setting keys (P6.6).
const (
	// SettingSetupCompleted is set (to an RFC 3339 time) when the guided
	// setup finished; without it the first launch runs setup.
	SettingSetupCompleted = "setup_completed"
	// SettingSetupStepPrefix + a step id holds that step's last result.
	SettingSetupStepPrefix = "setup_step:"
)

// Settings is the settings repository: durable key/value facts.
type Settings struct{ s *Store }

// Settings returns the settings repository.
func (s *Store) Settings() *Settings { return &Settings{s} }

// Get returns the value of key, or ErrNotFound.
func (r *Settings) Get(ctx context.Context, key string) (string, error) {
	var v string
	err := r.s.r.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err != nil {
		return "", wrap("settings get", err)
	}
	return v, nil
}

// Set stores value under key.
func (r *Settings) Set(ctx context.Context, key, value string) error {
	_, err := r.s.w.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, r.s.now().UnixMilli())
	return wrap("settings set", err)
}

// Delete removes key; a missing key is not an error.
func (r *Settings) Delete(ctx context.Context, key string) error {
	_, err := r.s.w.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
	return wrap("settings delete", err)
}

// SetupCompleted reports when setup finished; ok is false when it never did.
func (r *Settings) SetupCompleted(ctx context.Context) (at time.Time, ok bool, err error) {
	v, err := r.Get(ctx, SettingSetupCompleted)
	switch {
	case errors.Is(err, ErrNotFound):
		return time.Time{}, false, nil
	case err != nil:
		return time.Time{}, false, err
	}
	at, _ = time.Parse(time.RFC3339, v)
	return at, true, nil
}

// MarkSetupCompleted records that setup finished now.
func (r *Settings) MarkSetupCompleted(ctx context.Context) error {
	return r.Set(ctx, SettingSetupCompleted, r.s.now().UTC().Format(time.RFC3339))
}
