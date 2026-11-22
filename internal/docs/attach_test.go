package docs

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
			want: []Attachment{{Src: "/r/app/docs/a.md", Rel: "docs/a.md"}, {Src: "/r/app/docs/sub/b.md", Rel: "docs/sub/b.md"}},
		},
		{
			name: "outside repo is copied",
			root: "/r/app",
			srcs: []string{"/notes/app/a.md"},
			want: []Attachment{{Src: "/notes/app/a.md", Rel: ".forge/attached/a.md", Copy: true}},
		},
		{
			name: "sibling with common prefix is outside",
			root: "/r/app",
			srcs: []string{"/r/app-docs/a.md"},
			want: []Attachment{{Src: "/r/app-docs/a.md", Rel: ".forge/attached/a.md", Copy: true}},
		},
		{
			name: "clashing copies are numbered",
			root: "/r/app",
			srcs: []string{"/x/a.md", "/y/a.md", "/z/a.md"},
			want: []Attachment{{Src: "/x/a.md", Rel: ".forge/attached/a.md", Copy: true}, {Src: "/y/a.md", Rel: ".forge/attached/a-2.md", Copy: true}, {Src: "/z/a.md", Rel: ".forge/attached/a-3.md", Copy: true}},
		},
		{
			name: "duplicates dropped",
			root: "/r/app",
			srcs: []string{"/r/app/docs/a.md", "/r/app/docs/./a.md"},
			want: []Attachment{{Src: "/r/app/docs/a.md", Rel: "docs/a.md"}},
		},
		{
			name: "no repo root copies everything",
			root: "",
			srcs: []string{"/r/app/docs/a.md"},
			want: []Attachment{{Src: "/r/app/docs/a.md", Rel: ".forge/attached/a.md", Copy: true}},
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
		{[]Attachment{{Rel: "x/a.md", Newer: true}}, "@x/a.md " + NewerNote},
		{[]Attachment{{Rel: "x/a.md", Diverged: true}}, "@x/a.md " + DivergedNote},
	}
	for _, tt := range tests {
		if got := Mentions(tt.as); got != tt.want {
			t.Errorf("Mentions(%v) = %q, want %q", tt.as, got, tt.want)
		}
	}
}

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMaterialize(t *testing.T) {
	src := t.TempDir()
	wt := t.TempDir()
	writeFile(t, filepath.Join(src, "docs/same.md"), "same")
	writeFile(t, filepath.Join(wt, "docs/same.md"), "same")
	writeFile(t, filepath.Join(src, "docs/draft.md"), "draft")
	writeFile(t, filepath.Join(src, "outside.md"), "outside v2")
	writeFile(t, filepath.Join(wt, ".forge/attached/outside.md"), "outside v1")

	in := []Attachment{
		{Src: filepath.Join(src, "docs/same.md"), Rel: "docs/same.md"},
		{Src: filepath.Join(src, "docs/draft.md"), Rel: "docs/draft.md"},
		{Src: filepath.Join(src, "outside.md"), Rel: ".forge/attached/outside.md", Copy: true},
	}
	out, n, err := Materialize(wt, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("copied = %d, want 1", n)
	}
	if !reflect.DeepEqual(out, in) {
		t.Errorf("out = %+v, want unchanged %+v", out, in)
	}
	if got := readFile(t, filepath.Join(wt, "docs/draft.md")); got != "draft" {
		t.Errorf("draft = %q", got)
	}
	if got := readFile(t, filepath.Join(wt, ".forge/attached/outside.md")); got != "outside v2" {
		t.Errorf("outside = %q, want refreshed copy", got)
	}
	if _, err := os.Stat(filepath.Join(wt, ".forge/attached/docs/same.md")); !os.IsNotExist(err) {
		t.Errorf("identical doc copied: %v", err)
	}

	if _, _, err := Materialize(wt, []Attachment{{Src: filepath.Join(src, "gone.md"), Rel: ".forge/attached/gone.md", Copy: true}}, nil); err == nil {
		t.Error("missing source: want error")
	}
	if _, _, err := Materialize("", nil, nil); err == nil {
		t.Error("no worktree: want error")
	}
}

func TestMaterializeDiffering(t *testing.T) {
	errGit := errors.New("git broke")
	untouched := func(string) (bool, error) { return false, nil }
	changedIt := func(string) (bool, error) { return true, nil }
	failed := func(string) (bool, error) { return false, errGit }
	newer := Attachment{Rel: ".forge/attached/docs/plan.md", Copy: true, Newer: true}
	diverged := Attachment{Rel: ".forge/attached/docs/plan.md", Copy: true, Diverged: true}
	tests := []struct {
		name    string
		changed ChangedFunc
		want    Attachment
	}{
		{name: "branch untouched: source is newer", changed: untouched, want: newer},
		{name: "nil check counts as untouched", changed: nil, want: newer},
		{name: "branch changed it: diverged", changed: changedIt, want: diverged},
		{name: "check failed: diverged", changed: failed, want: diverged},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src, wt := t.TempDir(), t.TempDir()
			writeFile(t, filepath.Join(src, "docs/plan.md"), "main checkout")
			writeFile(t, filepath.Join(wt, "docs/plan.md"), "branch")
			var asked []string
			changed := tt.changed
			if changed != nil {
				changed = func(rel string) (bool, error) { asked = append(asked, rel); return tt.changed(rel) }
			}
			out, n, err := Materialize(wt, []Attachment{{Src: filepath.Join(src, "docs/plan.md"), Rel: "docs/plan.md"}}, changed)
			if err != nil {
				t.Fatal(err)
			}
			want := tt.want
			want.Src = filepath.Join(src, "docs/plan.md")
			if len(out) != 1 || out[0] != want || n != 1 {
				t.Fatalf("out = %+v (copied %d), want %+v", out, n, want)
			}
			if changed != nil && !slices.Equal(asked, []string{"docs/plan.md"}) {
				t.Errorf("asked about %q", asked)
			}
			if got := readFile(t, filepath.Join(wt, "docs/plan.md")); got != "branch" {
				t.Errorf("branch file overwritten: %q", got)
			}
			if got := readFile(t, filepath.Join(wt, ".forge/attached/docs/plan.md")); got != "main checkout" {
				t.Errorf("copy = %q", got)
			}
		})
	}
}

func TestMaterializeDifferingClash(t *testing.T) {
	src, wt, notes := t.TempDir(), t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, "README.md"), "main checkout")
	writeFile(t, filepath.Join(wt, "README.md"), "branch")
	writeFile(t, filepath.Join(notes, "README.md"), "notes")
	plan := Plan(src, []string{filepath.Join(notes, "README.md"), filepath.Join(src, "README.md")})
	out, _, err := Materialize(wt, plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Rel != ".forge/attached/README.md" || out[1].Rel != ".forge/attached/README-2.md" {
		t.Fatalf("rels = %q, %q", out[0].Rel, out[1].Rel)
	}
	if got := readFile(t, filepath.Join(wt, ".forge/attached/README.md")); got != "notes" {
		t.Errorf("outside copy = %q", got)
	}
	if got := readFile(t, filepath.Join(wt, ".forge/attached/README-2.md")); got != "main checkout" {
		t.Errorf("newer copy = %q", got)
	}
}

func TestRewrite(t *testing.T) {
	plain := Attachment{Rel: "docs/a.md"}
	newer := Attachment{Rel: ".forge/attached/docs/a.md", Copy: true, Newer: true}
	diverged := Attachment{Rel: ".forge/attached/docs/a.md", Copy: true, Diverged: true}
	spaced := Attachment{Rel: "docs/my doc.md"}
	spacedNewer := Attachment{Rel: ".forge/attached/docs/my doc.md", Copy: true, Newer: true}
	tests := []struct {
		name          string
		prompt        string
		before, after Attachment
		want          string
	}{
		{
			"newer", "Read @docs/a.md first.", plain, newer,
			"Read @.forge/attached/docs/a.md (newer version than on this branch) first.",
		},
		{
			"diverged at end of sentence", "See @docs/a.md.", plain, diverged,
			"See @.forge/attached/docs/a.md (version from the main checkout; this branch has its own changes to this file).",
		},
		{
			"every occurrence", "@docs/a.md and again @docs/a.md\n", plain, newer,
			"@.forge/attached/docs/a.md " + NewerNote + " and again @.forge/attached/docs/a.md " + NewerNote + "\n",
		},
		{
			"longer path untouched", "@docs/a.md.bak x@docs/a.md @docs/a.mdx", plain, newer,
			"@docs/a.md.bak x@docs/a.md @docs/a.mdx",
		},
		{
			"quoted", `Read @"docs/my doc.md", please`, spaced, spacedNewer,
			`Read @".forge/attached/docs/my doc.md" ` + NewerNote + `, please`,
		},
		{"unchanged", "Read @docs/a.md", plain, plain, "Read @docs/a.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Rewrite(tt.prompt, []Attachment{tt.before}, []Attachment{tt.after}); got != tt.want {
				t.Errorf("Rewrite =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}
