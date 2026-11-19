package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// t0 is the fixed "now" of test stores.
var t0 = time.Date(2026, 11, 20, 9, 0, 0, 0, time.Local)

// clock is a settable test clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// newTestStore opens a fresh database in a temp dir with a controllable clock.
func newTestStore(t *testing.T) (*Store, *clock) {
	t.Helper()
	c := &clock{t: t0}
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "forge.db"), WithClock(c.Now))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, c
}

func mustSession(t *testing.T, s *Store, name string) Session {
	t.Helper()
	x, err := s.Sessions().Create(context.Background(), Session{
		Name: name, ClaudeSessionID: "uuid-" + name, Project: "forge",
	})
	if err != nil {
		t.Fatalf("Create %s: %v", name, err)
	}
	return x
}

func wantErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("err = %v, want %v", err, target)
	}
}

func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
