package artifacts

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFSWatcher(t *testing.T) {
	repo := tempDir(t)
	wt := filepath.Join(tempDir(t), "s1") // created later
	w, err := NewWatcher(20 * time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	targets := []Target{
		{Path: repo, Project: "p", Patterns: DefaultWatch},
		{Path: wt, Project: "p", SessionID: 1, SessionName: "s1", Patterns: DefaultWatch},
	}
	if err := w.SetTargets(targets); err != nil {
		t.Fatal(err)
	}
	wait := func(what string) {
		t.Helper()
		select {
		case <-w.Events():
		case <-time.After(3 * time.Second):
			t.Fatalf("no event after %s", what)
		}
	}
	quiet := func(what string) {
		t.Helper()
		select {
		case <-w.Events():
			t.Fatalf("unexpected event after %s", what)
		case <-time.After(150 * time.Millisecond):
		}
	}

	write(t, filepath.Join(repo, "main.go"), "x", time.Time{})
	quiet("creating code")
	write(t, filepath.Join(repo, "main.go"), "y", time.Time{})
	quiet("editing code")
	write(t, filepath.Join(repo, "mock.html"), "x", time.Time{})
	wait("writing a mockup")

	if err := os.MkdirAll(filepath.Join(wt, "design"), 0o755); err != nil {
		t.Fatal(err)
	}
	wait("the worktree appearing")
	write(t, filepath.Join(wt, "design", "a.png"), "x", time.Time{})
	wait("writing in the new worktree")
}
