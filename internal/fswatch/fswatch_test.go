package fswatch

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

func TestFSWatcherSkipAndFilter(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"node_modules", ".forge/artifacts", ".git"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w, err := New(Options{
		Debounce: 20 * time.Millisecond,
		SkipDir: func(_, name string) bool {
			return name == "node_modules" || name == ".git"
		},
		Filter: func(p string) bool { return filepath.Ext(p) == ".html" },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	if err := w.Set([]string{root}); err != nil {
		t.Fatal(err)
	}

	write := func(rel string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, rel), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("node_modules/a.html")
	noEvent(t, w, "a write in a skipped folder")
	write(".git/b.html")
	noEvent(t, w, "a write in .git")
	write("main.go")
	noEvent(t, w, "a filtered-out file")
	write(".forge/artifacts/c.html")
	waitEvent(t, w, "a write in a dot-folder that isn't skipped")

	// A new folder always signals, and is watched.
	if err := os.Mkdir(filepath.Join(root, "design"), 0o755); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, w, "mkdir")
	write("design/d.html")
	waitEvent(t, w, "a write in the new folder")
	if err := os.Remove(filepath.Join(root, "main.go")); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, w, "a remove")
}
