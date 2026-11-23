package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiverged(t *testing.T) {
	write := func(root string, files map[string]string) {
		for rel, body := range files {
			p := filepath.Join(root, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	tests := []struct {
		name       string
		main, wt   map[string]string
		noMainRoot bool
		want       string // "rel:status,..."
	}{
		{
			"new and changed",
			map[string]string{"a.md": "A", "b.md": "B", "c.md": "C"},
			map[string]string{"a.md": "A", "b.md": "B2", "c.md": "c", "notes/new.md": "N"},
			false, "notes/new.md:new,b.md:changed,c.md:changed",
		}, // folders first, as Scan orders
		{"identical", map[string]string{"a.md": "A"}, map[string]string{"a.md": "A"}, false, ""},
		{"no main root", nil, map[string]string{"a.md": "A"}, true, "a.md:new"},
		{"hidden skipped", nil, map[string]string{".trash/x.md": "x", ".h.md": "h", "notes.txt": "t"}, false, ""},
		{"same size, different bytes", map[string]string{"a.md": "abc"}, map[string]string{"a.md": "abd"}, false, "a.md:changed"},
		{"no worktree docs", map[string]string{"a.md": "A"}, nil, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := t.TempDir()
			mainRoot, wtRoot := filepath.Join(base, "main"), filepath.Join(base, "wt")
			if !tt.noMainRoot {
				_ = os.MkdirAll(mainRoot, 0o755)
				write(mainRoot, tt.main)
			}
			write(wtRoot, tt.wt)
			got, err := Diverged(mainRoot, wtRoot)
			if err != nil {
				t.Fatal(err)
			}
			var parts []string
			for _, d := range got {
				parts = append(parts, d.Rel+":"+string(d.Status))
				if d.Path != filepath.Join(wtRoot, filepath.FromSlash(d.Rel)) {
					t.Errorf("path %s", d.Path)
				}
			}
			if s := strings.Join(parts, ","); s != tt.want {
				t.Errorf("got %q, want %q", s, tt.want)
			}
		})
	}
}
