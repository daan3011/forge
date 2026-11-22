package packs

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	docfs "github.com/daan3011/forge/internal/docs"
)

func write(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name, path, data string
		want             Pack
		wantErr          string
	}{
		{name: "empty", path: "/p/auth.yaml", data: "", want: Pack{Name: "auth", Path: "/p/auth.yaml"}},
		{
			name: "full", path: "/p/auth.yaml",
			data: "description: Auth\nfiles:\n  - a/*.go\n  - ' '\n  - b.go\ndocs: [auth.md]\npreamble: |\n  Be careful.\n",
			want: Pack{Name: "auth", Path: "/p/auth.yaml", Description: "Auth", Files: []string{"a/*.go", "b.go"}, Docs: []string{"auth.md"}, Preamble: "Be careful.\n"},
		},
		{name: "bad yaml", path: "/p/x.yaml", data: "files: [", wantErr: "x.yaml"},
		{name: "bad name", path: "/p/.hidden.yaml", data: "", wantErr: "name"},
		{name: "wrong type", path: "/p/x.yaml", data: "files: 3", wantErr: "x.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.path, []byte(tt.data))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != tt.want.Name || got.Path != tt.want.Path || got.Description != tt.want.Description ||
				!slices.Equal(got.Files, tt.want.Files) || !slices.Equal(got.Docs, tt.want.Docs) || got.Preamble != tt.want.Preamble {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCheckName(t *testing.T) {
	for name, ok := range map[string]bool{"auth": true, "a-b_c1": true, "": false, "-x": false, "a/b": false, "a.b": false, strings.Repeat("a", 65): false} {
		if got := CheckName(name) == nil; got != ok {
			t.Errorf("CheckName(%q) ok = %v, want %v", name, got, ok)
		}
	}
}

func TestSaveLoadDelete(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "packs")
	p, err := Save(dir, Pack{Name: "auth", Description: "Auth", Files: []string{"a.go"}, Preamble: "hi\n"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if p.Path != filepath.Join(dir, "auth.yaml") {
		t.Fatalf("path = %s", p.Path)
	}
	if _, err := Save(dir, Pack{Name: "auth"}, true); err == nil {
		t.Fatal("create over an existing pack should fail")
	}
	if _, err := Save(dir, Pack{Name: "bad name"}, true); err == nil {
		t.Fatal("bad name should fail")
	}
	if _, err := Save(dir, Pack{Name: "zeta", Docs: []string{"z.md"}}, true); err != nil {
		t.Fatal(err)
	}
	write(t, dir, map[string]string{"broken.yaml": "files: [", "notes.txt": "x"})
	ps, errs := Load(dir)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "broken.yaml") {
		t.Fatalf("errs = %v", errs)
	}
	if len(ps) != 2 || ps[0].Name != "auth" || ps[1].Name != "zeta" {
		t.Fatalf("packs = %+v", ps)
	}
	if ps[0].Description != "Auth" || !slices.Equal(ps[0].Files, []string{"a.go"}) || ps[0].Preamble != "hi\n" {
		t.Fatalf("round trip = %+v", ps[0])
	}
	if _, ok := Find(ps, "zeta"); !ok {
		t.Fatal("Find zeta")
	}
	if err := Delete(ps[1]); err != nil {
		t.Fatal(err)
	}
	ps, _ = Load(dir)
	if len(ps) != 1 {
		t.Fatalf("after delete = %+v", ps)
	}
	if ps, errs := Load(filepath.Join(dir, "missing")); ps != nil || errs != nil {
		t.Fatal("missing dir should be no packs")
	}
}

func TestExpand(t *testing.T) {
	repo := t.TempDir()
	docs := t.TempDir()
	write(t, repo, map[string]string{
		"internal/auth/token.go":         "package auth",
		"internal/auth/session/store.go": "package session",
		"internal/auth/README.md":        "# auth",
		"internal/db/db.go":              "package db",
		"node_modules/x/auth/y.go":       "x",
		"cmd/main.go":                    "package main",
	})
	write(t, docs, map[string]string{"auth.md": "# doc", "adr/001.md": "adr"})
	abs := func(root string, rels ...string) []string {
		out := make([]string, len(rels))
		for i, r := range rels {
			out[i] = filepath.Join(root, filepath.FromSlash(r))
		}
		return out
	}
	tests := []struct {
		name        string
		pack        Pack
		want        []string
		wantMissing []string
	}{
		{name: "literal", pack: Pack{Files: []string{"cmd/main.go"}}, want: abs(repo, "cmd/main.go")},
		{name: "star", pack: Pack{Files: []string{"internal/auth/*.go"}}, want: abs(repo, "internal/auth/token.go")},
		{
			name: "double star", pack: Pack{Files: []string{"internal/**/*.go"}},
			want: abs(repo, "internal/auth/session/store.go", "internal/auth/token.go", "internal/db/db.go"),
		},
		{name: "leading double star skips node_modules", pack: Pack{Files: []string{"**/y.go"}}, want: nil, wantMissing: []string{"**/y.go"}},
		{
			name: "folder", pack: Pack{Files: []string{"internal/auth"}},
			want: abs(repo, "internal/auth/README.md", "internal/auth/session/store.go", "internal/auth/token.go"),
		},
		{
			name: "dedupe keeps order", pack: Pack{Files: []string{"cmd/main.go", "**/*.go"}},
			want: abs(repo, "cmd/main.go", "internal/auth/session/store.go", "internal/auth/token.go", "internal/db/db.go"),
		},
		{name: "missing", pack: Pack{Files: []string{"nope.go", "x/**"}}, wantMissing: []string{"nope.go", "x/**"}},
		{
			name: "docs", pack: Pack{Docs: []string{"auth.md", "adr/*.md", filepath.Join(docs, "auth.md")}},
			want: abs(docs, "auth.md", "adr/001.md"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.pack.Name, tt.pack.Preamble = "p", "  pre  \n"
			r := Expand(tt.pack, repo, docs)
			if !slices.Equal(r.Paths, tt.want) {
				t.Errorf("paths = %v\nwant    %v", r.Paths, tt.want)
			}
			if !slices.Equal(r.Missing, tt.wantMissing) {
				t.Errorf("missing = %v, want %v", r.Missing, tt.wantMissing)
			}
			if r.Name != "p" || r.Preamble != "pre" {
				t.Errorf("name/preamble = %q %q", r.Name, r.Preamble)
			}
		})
	}
	if r := Expand(Pack{Files: []string{"a.go"}}, "", docs); !slices.Equal(r.Missing, []string{"a.go"}) {
		t.Errorf("no repo: %+v", r)
	}
}

func TestMatchSegs(t *testing.T) {
	tests := []struct {
		pat, name string
		want      bool
	}{
		{"**", "a/b/c", true},
		{"**/*.go", "a.go", true},
		{"**/*.go", "a/b.go", true},
		{"a/**/b.go", "a/b.go", true},
		{"a/**/b.go", "a/x/y/b.go", true},
		{"a/*.go", "a/x/b.go", false},
		{"*.go", "a.md", false},
		{"[", "[", false},
	}
	for _, tt := range tests {
		if got := matchSegs(strings.Split(tt.pat, "/"), strings.Split(tt.name, "/")); got != tt.want {
			t.Errorf("matchSegs(%q, %q) = %v, want %v", tt.pat, tt.name, got, tt.want)
		}
	}
}

func TestTokensAndPaths(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"a": "12345678", "b": "1"})
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	if got := Tokens([]string{a, b, filepath.Join(dir, "none")}); got != 3 {
		t.Errorf("Tokens = %d, want 3", got)
	}
	rs := []Resolved{{Name: "x", Paths: []string{a, b}}, {Name: "y", Paths: []string{b}}}
	if got := Paths(rs); !slices.Equal(got, []string{a, b}) {
		t.Errorf("Paths = %v", got)
	}
}

func TestWithFiles(t *testing.T) {
	p := Pack{Files: []string{"a.go", "x/**/*.go", "b.go"}}
	got := WithFiles(p, []string{"b.go", "c.go", "x/**/*.go"})
	if want := []string{"x/**/*.go", "b.go", "c.go"}; !slices.Equal(got.Files, want) {
		t.Errorf("WithFiles = %v, want %v", got.Files, want)
	}
	if got := Literals(p); !slices.Equal(got, []string{"a.go", "b.go"}) {
		t.Errorf("Literals = %v", got)
	}
}

func TestPrompt(t *testing.T) {
	plan := []docfs.Attachment{{Src: "/r/a.go", Rel: "a.go"}, {Src: "/r/b.go", Rel: "b.go"}}
	tests := []struct {
		name string
		rs   []Resolved
		plan []docfs.Attachment
		rest string
		want string
	}{
		{name: "no packs", rest: "do it\n", want: "do it\n"},
		{
			name: "one pack", rs: []Resolved{{Name: "auth", Preamble: "Careful."}}, plan: plan, rest: "Fix login.\n",
			want: "Careful.\n\nRead these files first (context pack auth): @a.go @b.go\n\nFix login.\n",
		},
		{
			name: "two packs no rest", rs: []Resolved{{Name: "auth"}, {Name: "db", Preamble: "DB."}}, plan: plan,
			want: "DB.\n\nRead these files first (context packs auth, db): @a.go @b.go\n",
		},
		{name: "preamble only", rs: []Resolved{{Name: "x", Preamble: "Hi."}}, want: "Hi.\n"},
		{name: "empty pack", rs: []Resolved{{Name: "x"}}, rest: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Prompt(tt.rs, tt.plan, tt.rest); got != tt.want {
				t.Errorf("Prompt =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}
