package artifacts

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/store"
)

func tempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func write(t *testing.T, path, data string, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if !mod.IsZero() {
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNewSpec(t *testing.T) {
	if got := NewSpec(nil); !reflect.DeepEqual(got.Relative, DefaultWatch) || got.Absolute != nil {
		t.Errorf("NewSpec(nil) = %+v", got)
	}
	got := NewSpec([]string{"./mock/*.html", "/abs/**", " ", "x/**"})
	want := Spec{Relative: []string{"mock/*.html", "x/**"}, Absolute: []string{"/abs/**"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NewSpec = %+v, want %+v", got, want)
	}
}

func TestSkipDir(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/r/node_modules", true},
		{"/r/.git", true},
		{"/r/dist", true},
		{"/r/.idea", true},
		{"/r/.forge", false},
		{"/r/.forge/artifacts", false},
		{"/r/.forge/docs", true},
		{"/r/.forge/attached", true},
		{"/r/design", false},
		{"/r/src/components", false},
	}
	for _, tt := range tests {
		if got := SkipDir(tt.path, filepath.Base(tt.path)); got != tt.want {
			t.Errorf("SkipDir(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestType(t *testing.T) {
	tests := map[string]string{
		"a.html": "html", "a.HTM": "html", "a.svg": "svg", "a.PNG": "png", "a.jpeg": "jpg",
		"a.jpg": "jpg", "a.gif": "gif", "a.webp": "webp", "a.pdf": "pdf", "a.md": "md",
		"a.txt": "file", "Makefile": "file",
	}
	for in, want := range tests {
		if got := Type(in); got != want {
			t.Errorf("Type(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTargets(t *testing.T) {
	base := tempDir(t)
	repo := filepath.Join(base, "repo")
	wt := filepath.Join(base, "wt", "s1")
	for _, d := range []string{repo, wt} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	spec := Spec{Relative: []string{"**/*.html"}, Absolute: []string{"/abs/a/*.svg", "/abs/a/**/*.png", "/abs/b/*.png"}}
	got := Targets(
		[]config.Project{{Name: "forge", Path: repo}, {Name: "nopath"}, {Name: "dup", Path: repo}},
		[]store.Session{
			{ID: 1, Name: "s1", Project: "forge", Worktree: wt},
			{ID: 2, Name: "gone", Project: "forge", Worktree: filepath.Join(base, "wt", "gone")},
			{ID: 3, Name: "main", Project: "forge", Worktree: repo, WorktreeExternal: true},
			{ID: 4, Name: "none", Project: "forge"},
		}, spec)
	want := []Target{
		{Path: repo, Project: "forge", Patterns: spec.Relative},
		{Path: wt, Project: "forge", SessionID: 1, SessionName: "s1", Patterns: spec.Relative},
		{Path: "/abs/a", Absolute: true, Patterns: []string{"/abs/a/*.svg", "/abs/a/**/*.png"}},
		{Path: "/abs/b", Absolute: true, Patterns: []string{"/abs/b/*.png"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Targets =\n%+v\nwant\n%+v", got, want)
	}
}

func TestScan(t *testing.T) {
	base := tempDir(t)
	repo := filepath.Join(base, "repo")
	wt := filepath.Join(repo, ".worktrees", "s1") // nested in the repo, but a dot-folder
	nested := filepath.Join(repo, "wt2")          // nested and not skipped
	abs := filepath.Join(base, "elsewhere")
	t1 := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	t3 := t2.Add(time.Hour)

	write(t, filepath.Join(repo, "mock.html"), "a", t1)
	write(t, filepath.Join(repo, "design", "notes.md"), "b", t2)
	write(t, filepath.Join(repo, "main.go"), "c", t1)
	write(t, filepath.Join(repo, "node_modules", "x", "y.html"), "d", t1)
	write(t, filepath.Join(repo, "dist", "index.html"), "e", t1)
	write(t, filepath.Join(repo, ".forge", "artifacts", "s1", "kept.html"), "f", t1)
	write(t, filepath.Join(repo, ".forge", "docs", "x.html"), "g", t1)
	write(t, filepath.Join(wt, "a.svg"), "h", t1)
	write(t, filepath.Join(nested, "b.png"), "i", t3)
	write(t, filepath.Join(abs, "c.png"), "j", t1)
	write(t, filepath.Join(abs, "c.html"), "k", t1)

	spec := Spec{
		Relative: DefaultWatch,
		Absolute: []string{filepath.ToSlash(abs) + "/*.png", filepath.ToSlash(nested) + "/*.png"},
	}
	targets := []Target{
		{Path: repo, Project: "forge", Patterns: spec.Relative},
		{Path: wt, Project: "forge", SessionID: 1, SessionName: "s1", Patterns: spec.Relative},
		{Path: nested, Project: "forge", SessionID: 2, SessionName: "s2", Patterns: spec.Relative},
		{Path: abs, Absolute: true, Patterns: spec.Absolute[:1]},
		{Path: nested, Absolute: true, Patterns: spec.Absolute[1:]},
	}
	got, err := Scan(targets)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		rel, project, session, typ string
	}
	var rows []row
	for _, a := range got {
		rel, _ := filepath.Rel(base, a.Path)
		rows = append(rows, row{filepath.ToSlash(rel), a.Project, a.SessionName, a.Type})
	}
	want := []row{
		{"repo/wt2/b.png", "forge", "s2", "png"},
		{"repo/design/notes.md", "forge", "", "md"},
		{"elsewhere/c.png", "", "", "png"},
		{"repo/.forge/artifacts/s1/kept.html", "forge", "", "html"},
		{"repo/.worktrees/s1/a.svg", "forge", "s1", "svg"},
		{"repo/mock.html", "forge", "", "html"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("Scan =\n%v\nwant\n%v", rows, want)
	}
	if got[0].Size != 1 || !got[0].ModTime.Equal(t3) {
		t.Errorf("first = %+v", got[0])
	}
	var kept []string
	for _, a := range got {
		if a.Kept() {
			kept = append(kept, filepath.Base(a.Path))
		}
	}
	if !reflect.DeepEqual(kept, []string{"kept.html"}) {
		t.Errorf("kept = %v", kept)
	}
}

func TestScanTooMany(t *testing.T) {
	root := tempDir(t)
	for i := range 5 {
		write(t, filepath.Join(root, "f"+string(rune('a'+i))+".html"), "x", time.Time{})
	}
	got, err := scanLimited([]Target{{Path: root, Patterns: []string{"*.html"}}}, 3)
	if !errors.Is(err, ErrTooMany) {
		t.Fatalf("err = %v", err)
	}
	if len(got) == 0 || len(got) >= 5 {
		t.Errorf("found %d, want a partial result", len(got))
	}
}

func TestScanMissingRoot(t *testing.T) {
	got, err := Scan([]Target{{Path: filepath.Join(tempDir(t), "nope"), Patterns: DefaultWatch}})
	if err != nil || len(got) != 0 {
		t.Errorf("Scan = %v, %v", got, err)
	}
}

func TestMatches(t *testing.T) {
	targets := []Target{
		{Path: "/r", Patterns: DefaultWatch},
		{Path: "/abs", Absolute: true, Patterns: []string{"/abs/*.png"}},
	}
	tests := []struct {
		path string
		want bool
	}{
		{"/r/a.html", true},
		{"/r/src/a.svg", true},
		{"/r/main.go", false},
		{"/r/design/notes.txt", true},
		{"/r/node_modules/a.html", false},
		{"/r/.forge/artifacts/x.go", true},
		{"/r/.forge/attached/x.html", false},
		{"/abs/a.png", true},
		{"/abs/sub/a.png", false},
		{"/other/a.html", false},
	}
	for _, tt := range tests {
		if got := Matches(targets, tt.path); got != tt.want {
			t.Errorf("Matches(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestRoots(t *testing.T) {
	got := Roots([]Target{{Path: "/b"}, {Path: "/a"}})
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"/a", "/b"}) {
		t.Errorf("Roots = %v", got)
	}
}
