package docs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func waitEvent(t *testing.T, w *FSWatcher, what string) {
	t.Helper()
	select {
	case <-w.Events():
	case <-time.After(3 * time.Second):
		t.Fatalf("no event after %s", what)
	}
}

func noEvent(t *testing.T, w *FSWatcher, what string) {
	t.Helper()
	select {
	case <-w.Events():
		t.Fatalf("unexpected event after %s", what)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestFSWatcher(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "docs") // doesn't exist yet
	w, err := NewWatcher(20 * time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	if err := w.Set([]string{root}); err != nil {
		t.Fatal(err)
	}

	// The root appearing is seen through its parent.
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, w, "creating the root")

	// A burst of writes is one event.
	for i := range 5 {
		if err := os.WriteFile(filepath.Join(root, "a.md"), []byte{byte('a' + i)}, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	waitEvent(t, w, "writes")
	noEvent(t, w, "the burst")

	// New subfolders are watched.
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, w, "mkdir")
	if err := os.WriteFile(filepath.Join(sub, "b.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, w, "writing in a new subfolder")

	// Unrelated siblings of the root don't signal.
	if err := os.WriteFile(filepath.Join(base, "other.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	noEvent(t, w, "an unrelated write")
}
