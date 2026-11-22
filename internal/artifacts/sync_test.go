package artifacts

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSyncCopy(t *testing.T) {
	mtime := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		src, dest   string // contents; "-" = missing
		sameTime    bool
		wantChanged bool
		wantErr     error
		wantDest    string
	}{
		{"new copy", "v1", "-", false, true, nil, "v1"},
		{"unchanged", "v1", "v1", true, false, nil, "v1"},
		{"changed content", "v2!", "v1", false, true, nil, "v2!"},
		{"same size, newer", "v2", "v1", false, true, nil, "v2"},
		{"missing source", "-", "v1", false, false, os.ErrNotExist, "v1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			src, dest := filepath.Join(dir, "w", "a.html"), filepath.Join(dir, "keep", "s", "a.html")
			write := func(p, data string, at time.Time) {
				if data == "-" {
					return
				}
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(p, at, at); err != nil {
					t.Fatal(err)
				}
			}
			destTime := mtime.Add(-time.Hour)
			if tt.sameTime {
				destTime = mtime
			}
			write(src, tt.src, mtime)
			write(dest, tt.dest, destTime)

			changed, err := SyncCopy(src, dest)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if changed != tt.wantChanged {
				t.Errorf("changed = %v, want %v", changed, tt.wantChanged)
			}
			got, err := os.ReadFile(dest)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.wantDest {
				t.Errorf("dest = %q, want %q", got, tt.wantDest)
			}
			if tt.wantChanged {
				info, _ := os.Stat(dest)
				if !info.ModTime().Equal(mtime) {
					t.Errorf("dest mtime = %v, want %v", info.ModTime(), mtime)
				}
				left, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), ".sync-*"))
				if len(left) > 0 {
					t.Errorf("temp files left: %v", left)
				}
			}
		})
	}
}
