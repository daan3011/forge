package docs

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPlan(t *testing.T) {
	tests := []struct {
		name string
		root string
		srcs []string
		want []Attachment
	}{
		{
			name: "inside repo keeps its path",
			root: "/r/app",
			srcs: []string{"/r/app/docs/a.md", "/r/app/docs/sub/b.md"},
			want: []Attachment{{"/r/app/docs/a.md", "docs/a.md", false}, {"/r/app/docs/sub/b.md", "docs/sub/b.md", false}},
		},
		{
			name: "outside repo is copied",
			root: "/r/app",
			srcs: []string{"/notes/app/a.md"},
			want: []Attachment{{"/notes/app/a.md", ".forge/attached/a.md", true}},
		},
		{
			name: "sibling with common prefix is outside",
			root: "/r/app",
			srcs: []string{"/r/app-docs/a.md"},
			want: []Attachment{{"/r/app-docs/a.md", ".forge/attached/a.md", true}},
		},
		{
			name: "clashing copies are numbered",
			root: "/r/app",
			srcs: []string{"/x/a.md", "/y/a.md", "/z/a.md"},
			want: []Attachment{{"/x/a.md", ".forge/attached/a.md", true}, {"/y/a.md", ".forge/attached/a-2.md", true}, {"/z/a.md", ".forge/attached/a-3.md", true}},
		},
		{
			name: "duplicates dropped",
			root: "/r/app",
			srcs: []string{"/r/app/docs/a.md", "/r/app/docs/./a.md"},
			want: []Attachment{{"/r/app/docs/a.md", "docs/a.md", false}},
		},
		{
			name: "no repo root copies everything",
			root: "",
			srcs: []string{"/r/app/docs/a.md"},
			want: []Attachment{{"/r/app/docs/a.md", ".forge/attached/a.md", true}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Plan(tt.root, tt.srcs); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Plan = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMentions(t *testing.T) {
	tests := []struct {
		as   []Attachment
		want string
	}{
		{nil, ""},
		{[]Attachment{{Rel: "docs/a.md"}}, "@docs/a.md"},
		{[]Attachment{{Rel: "docs/a.md"}, {Rel: "docs/my doc.md"}}, `@docs/a.md @"docs/my doc.md"`},
	}
	for _, tt := range tests {
		if got := Mentions(tt.as); got != tt.want {
			t.Errorf("Mentions(%v) = %q, want %q", tt.as, got, tt.want)
		}
	}
}

func TestMaterialize(t *testing.T) {
	src := t.TempDir()
	wt := t.TempDir()
	write := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(p string) string {
		t.Helper()
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	write(filepath.Join(src, "docs/tracked.md"), "main checkout edit")
	write(filepath.Join(wt, "docs/tracked.md"), "branch version")
	write(filepath.Join(src, "docs/draft.md"), "draft")
	write(filepath.Join(src, "outside.md"), "outside v2")
	write(filepath.Join(wt, ".forge/attached/outside.md"), "outside v1")

	n, err := Materialize(wt, []Attachment{
		{Src: filepath.Join(src, "docs/tracked.md"), Rel: "docs/tracked.md"},
		{Src: filepath.Join(src, "docs/draft.md"), Rel: "docs/draft.md"},
		{Src: filepath.Join(src, "outside.md"), Rel: ".forge/attached/outside.md", Copy: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("copied = %d, want 1", n)
	}
	if got := read(filepath.Join(wt, "docs/tracked.md")); got != "branch version" {
		t.Errorf("tracked overwritten: %q", got)
	}
	if got := read(filepath.Join(wt, "docs/draft.md")); got != "draft" {
		t.Errorf("draft = %q", got)
	}
	if got := read(filepath.Join(wt, ".forge/attached/outside.md")); got != "outside v2" {
		t.Errorf("outside = %q, want refreshed copy", got)
	}

	if _, err := Materialize(wt, []Attachment{{Src: filepath.Join(src, "gone.md"), Rel: ".forge/attached/gone.md", Copy: true}}); err == nil {
		t.Error("missing source: want error")
	}
	if _, err := Materialize("", nil); err == nil {
		t.Error("no worktree: want error")
	}
}
