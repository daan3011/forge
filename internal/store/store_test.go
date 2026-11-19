package store

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestDefaultPath(t *testing.T) {
	tests := []struct {
		name string
		xdg  string
		home string
		want string
	}{
		{"xdg set", "/data", "/home/me", "/data/forge/forge.db"},
		{"xdg unset", "", "/home/me", "/home/me/.local/share/forge/forge.db"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", tt.xdg)
			t.Setenv("HOME", tt.home)
			if got := DefaultPath(); got != tt.want {
				t.Errorf("DefaultPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestConcurrentWriters simulates `forge hook` and the TUI writing the same
// database from separate handles; WAL + busy timeout + immediate transactions
// must keep them from failing with SQLITE_BUSY.
func TestConcurrentWriters(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "forge.db")
	a, err := Open(ctx, path)
	noErr(t, err)
	defer a.Close()
	b, err := Open(ctx, path)
	noErr(t, err)
	defer b.Close()

	sess, err := a.Sessions().Create(ctx, Session{Name: "s", ClaudeSessionID: "u", Project: "p"})
	noErr(t, err)

	const n = 50
	var wg sync.WaitGroup
	errs := make(chan error, 3*n)
	for i := range n {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_, err := b.Events().Append(ctx, Event{ClaudeSessionID: "u", Event: "PreToolUse"})
			errs <- err
		}()
		go func() {
			defer wg.Done()
			errs <- a.Sessions().SetStatus(ctx, sess.ID, StatusWorking, t0)
		}()
		go func() {
			defer wg.Done()
			errs <- a.Cache().Put(ctx, fmt.Sprintf("k%d", i), "github", "p", []byte("v"), 0)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	evs, err := a.Events().ListForSession(ctx, sess.ID, 0)
	noErr(t, err)
	if len(evs) != n {
		t.Errorf("events = %d, want %d", len(evs), n)
	}
}

func TestVacuum(t *testing.T) {
	s, _ := newTestStore(t)
	noErr(t, s.Vacuum(context.Background()))
}
