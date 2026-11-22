package artifacts

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestKeep(t *testing.T) {
	base := tempDir(t)
	repo := filepath.Join(base, "repo")
	wt := filepath.Join(base, "wt")
	mod := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	src := filepath.Join(wt, "design", "mock.html")
	write(t, src, "v1", mod)
	keptDir := filepath.Join(repo, ".forge", "artifacts")

	dest, wrote, err := Keep(src, "s1", repo)
	if err != nil || !wrote || dest != filepath.Join(keptDir, "s1", "mock.html") {
		t.Fatalf("Keep = %q, %v, %v", dest, wrote, err)
	}
	if data, _ := os.ReadFile(dest); string(data) != "v1" {
		t.Errorf("copy = %q", data)
	}
	if info, _ := os.Stat(dest); !info.ModTime().Equal(mod) {
		t.Errorf("mtime = %v, want %v", info.ModTime(), mod)
	}

	// The same content again reuses the copy.
	dest, wrote, err = Keep(src, "s1", repo)
	if err != nil || wrote || dest != filepath.Join(keptDir, "s1", "mock.html") {
		t.Errorf("again = %q, %v, %v", dest, wrote, err)
	}

	// Changed content gets a new name.
	write(t, src, "v2", time.Time{})
	dest, wrote, err = Keep(src, "s1", repo)
	if err != nil || !wrote || dest != filepath.Join(keptDir, "s1", "mock-2.html") {
		t.Errorf("changed = %q, %v, %v", dest, wrote, err)
	}

	// Without a session it goes straight into the folder; odd session
	// names stay one folder.
	if dest, _, _ := Keep(src, "", repo); dest != filepath.Join(keptDir, "mock.html") {
		t.Errorf("no session = %q", dest)
	}
	if dest, _, _ := Keep(src, "../x/y", repo); dest != filepath.Join(keptDir, "_..-x-y", "mock.html") {
		t.Errorf("odd session = %q", dest)
	}

	// A kept artifact can't be kept again, and a project needs a checkout.
	if _, _, err := Keep(filepath.Join(keptDir, "mock.html"), "", repo); !errors.Is(err, ErrAlreadyKept) {
		t.Errorf("kept again: %v", err)
	}
	if _, _, err := Keep(src, "s1", ""); !errors.Is(err, ErrNoCheckout) {
		t.Errorf("no checkout: %v", err)
	}
	if _, _, err := Keep(filepath.Join(wt, "missing.html"), "", repo); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing: %v", err)
	}
	// No temp files are left behind.
	entries, _ := os.ReadDir(filepath.Join(keptDir, "s1"))
	if len(entries) != 2 {
		t.Errorf("s1 holds %d entries", len(entries))
	}
}
