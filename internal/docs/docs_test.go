package docs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("# "+f+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func flatten(n *Node) []string {
	var out []string
	for _, c := range n.Children {
		if c.IsDir {
			out = append(out, c.Rel+"/")
			out = append(out, flatten(c)...)
			continue
		}
		out = append(out, c.Rel)
	}
	return out
}

func TestScan(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  []string
	}{
		{"empty", nil, nil},
		{
			"sorted folders first",
			[]string{"b.md", "A.md", "z/x.md", "c/y.md"},
			[]string{"c/", "c/y.md", "z/", "z/x.md", "A.md", "b.md"},
		},
		{
			"non-md and hidden skipped",
			[]string{"a.md", "b.txt", ".hidden.md", ".trash/old.md", "img/x.png"},
			[]string{"a.md"},
		},
		{"nested", []string{"a/b/c.md", "a/d.md"}, []string{"a/", "a/b/", "a/b/c.md", "a/d.md"}},
		{"upper-case extension", []string{"README.MD"}, []string{"README.MD"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, tt.files...)
			n, err := Scan(root)
			if err != nil {
				t.Fatal(err)
			}
			got := flatten(n)
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			if n.Docs() != len(filterDocs(tt.want)) {
				t.Fatalf("Docs() = %d", n.Docs())
			}
		})
	}
}

func filterDocs(rels []string) []string {
	var out []string
	for _, r := range rels {
		if !strings.HasSuffix(r, "/") {
			out = append(out, r)
		}
	}
	return out
}

func TestScanMissingRoot(t *testing.T) {
	n, err := Scan(filepath.Join(t.TempDir(), "nope"))
	if err != nil || n == nil || len(n.Children) != 0 {
		t.Fatalf("got %v, %v", n, err)
	}
}

func TestCleanRel(t *testing.T) {
	tests := []struct {
		in, want string
		err      bool
	}{
		{"deploy", "deploy.md", false},
		{"  runbooks/deploy  ", "runbooks/deploy.md", false},
		{"a//b/./c.md", "a/b/c.md", false},
		{"Notes.MD", "Notes.MD", false},
		{`win\path`, "win/path.md", false},
		{"", "", true},
		{"/etc/passwd", "", true},
		{"../x", "", true},
		{"a/../../x", "", true},
		{".hidden", "", true},
		{"a/.trash/x", "", true},
		{"folder/", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := CleanRel(tt.in)
			if (err != nil) != tt.err || got != tt.want {
				t.Fatalf("CleanRel(%q) = %q, %v", tt.in, got, err)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "docs") // created on demand
	p, err := Create(root, "runbooks/deploy", "body")
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(root, "runbooks", "deploy.md") {
		t.Fatalf("path %s", p)
	}
	if b, _ := os.ReadFile(p); string(b) != "body" {
		t.Fatalf("body %q", b)
	}
	if _, err := Create(root, "runbooks/deploy.md", "x"); !errors.Is(err, ErrExists) {
		t.Fatalf("second create: %v", err)
	}
	if _, err := Create(root, "../x", "x"); err == nil {
		t.Fatal("escape allowed")
	}
}

func TestRename(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.md", "b.md")
	a := filepath.Join(root, "a.md")

	if _, err := Rename(root, a, "b"); !errors.Is(err, ErrExists) {
		t.Fatalf("overwrite: %v", err)
	}
	if _, err := Rename(root, a, "../out"); err == nil {
		t.Fatal("escape allowed")
	}
	if _, err := Rename(root, filepath.Join(t.TempDir(), "x.md"), "y"); err == nil {
		t.Fatal("source outside root allowed")
	}
	if p, err := Rename(root, a, "a.md"); err != nil || p != a {
		t.Fatalf("same name: %s %v", p, err)
	}
	p, err := Rename(root, a, "moved/here")
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(root, "moved", "here.md") {
		t.Fatalf("path %s", p)
	}
	if _, err := os.Stat(a); !os.IsNotExist(err) {
		t.Fatal("old file still there")
	}
}

func TestTrash(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 11, 22, 9, 30, 15, 0, time.UTC)
	write(t, root, "sub/a.md")
	src := filepath.Join(root, "sub", "a.md")
	got, err := Trash(root, src, now)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(root, ".trash", "sub", "a.md") {
		t.Fatalf("first trash %s", got)
	}
	write(t, root, "sub/a.md")
	got, err = Trash(root, src, now)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(root, ".trash", "sub", "a-20261122-093015.md") {
		t.Fatalf("collision %s", got)
	}
	if _, err := Trash(root, root, now); err == nil {
		t.Fatal("trashing the root allowed")
	}
}

func TestTemplate(t *testing.T) {
	now := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	for _, name := range Templates {
		body := Template(name, "Deploy", now)
		if !strings.HasPrefix(body, "# Deploy\n") {
			t.Errorf("%s: %q", name, body)
		}
		if strings.Contains(body, "{{") {
			t.Errorf("%s: unfilled placeholder", name)
		}
		if name != "blank" && !strings.Contains(body, "2026-11-22") {
			t.Errorf("%s: no date", name)
		}
	}
	if Template("unknown", "X", now) != "# X\n" {
		t.Error("unknown template")
	}
}

func TestTitleFrom(t *testing.T) {
	for in, want := range map[string]string{
		"runbooks/deploy-api.md": "Deploy api",
		"notes_2026.md":          "Notes 2026",
		".md":                    "Untitled",
	} {
		if got := TitleFrom(in); got != want {
			t.Errorf("TitleFrom(%q) = %q", in, got)
		}
	}
}
