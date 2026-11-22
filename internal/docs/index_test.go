package docs

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestParseDoc(t *testing.T) {
	tests := []struct {
		name      string
		rel, text string
		title     string
		headings  []string
	}{
		{"h1 title", "a.md", "# Deploy\n\nbody\n## Steps\n", "Deploy", []string{"Deploy", "Steps"}},
		{"no h1 uses file name", "run/books.MD", "## Only h2\n", "books", []string{"Only h2"}},
		{"empty", "x.md", "", "x", nil},
		{"fenced code skipped", "a.md", "# T\n```sh\n# not a heading\n```\n## Real\n~~~\n## nope\n~~~\n", "T", []string{"T", "Real"}},
		{"closing hashes and indent", "a.md", "   ### Tidy ###\n####### seven\n#nospace\n", "a", []string{"Tidy"}},
		{"crlf", "a.md", "# Win\r\n## Two\r\n", "Win", []string{"Win", "Two"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := ParseDoc("p", "/x/"+tt.rel, tt.rel, tt.text)
			if d.Title != tt.title {
				t.Errorf("title = %q, want %q", d.Title, tt.title)
			}
			var got []string
			for _, h := range d.Headings {
				got = append(got, h.Text)
			}
			if !slices.Equal(got, tt.headings) {
				t.Errorf("headings = %q, want %q", got, tt.headings)
			}
		})
	}
}

func TestSearch(t *testing.T) {
	ix := &Index{docs: []Doc{
		ParseDoc("alpha", "/a/deploy.md", "deploy.md", "# Deploy\n\nUse rollback when broken.\n\n## Rollback\n\nrevert it\n"),
		ParseDoc("alpha", "/a/ops.md", "ops.md", "# Operations\n\nIntro.\n\n## Incidents\n\nThe ROLLBACK script lives here.\nrollback twice.\n"),
		ParseDoc("bravo", "/b/rollback.md", "rollback.md", "# Rollback guide\n\nSteps.\n"),
		ParseDoc("bravo", "/b/none.md", "none.md", "# None\n\nnothing\n"),
		ParseDoc("bravo", "/b/code.md", "code.md", "# Code\n\n```\nrollback()\n```\n"),
	}}
	type want struct {
		rel     string
		kind    MatchKind
		heading string
		snippet string
	}
	tests := []struct {
		name string
		q    string
		want []want
	}{
		{"empty query", "  ", nil},
		{"no match", "zebra", nil},
		{
			"title > heading > body, body by count", "rollback",
			[]want{
				{"rollback.md", MatchTitle, "Rollback guide", "Rollback guide"},
				{"deploy.md", MatchHeading, "Rollback", "Rollback"},
				{"ops.md", MatchBody, "Incidents", "The ROLLBACK script lives here."},
				{"code.md", MatchBody, "Code", "rollback()"},
			},
		},
		{"case-insensitive body, nearest heading", "SCRIPT", []want{{"ops.md", MatchBody, "Incidents", "The ROLLBACK script lives here."}}},
		{"body before any heading below h1", "intro", []want{{"ops.md", MatchBody, "Operations", "Intro."}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits := ix.Search(tt.q, 0)
			var got []want
			for _, h := range hits {
				got = append(got, want{h.Doc.Rel, h.Kind, h.Heading, h.Snippet})
				if h.MatchLen > 0 && h.Snippet[h.MatchAt:h.MatchAt+h.MatchLen] == "" {
					t.Errorf("bad match span in %q", h.Snippet)
				}
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
	if got := ix.Search("rollback", 2); len(got) != 2 {
		t.Fatalf("limit: got %d hits", len(got))
	}
}

func TestBuildIndex(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "a.md"), []byte("# Alpha\n\nfind me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	ix := BuildIndex([]IndexRoot{{Name: "p", Tree: tree}, {Name: "nil"}})
	if ix.Len() != 1 || ix.Docs()[0].Rel != "sub/a.md" || ix.Docs()[0].Root != "p" {
		t.Fatalf("docs = %+v", ix.Docs())
	}
	hits := ix.Search("find", 0)
	if len(hits) != 1 || hits[0].Heading != "Alpha" {
		t.Fatalf("hits = %+v", hits)
	}
	var nilIx *Index
	if nilIx.Search("x", 0) != nil || nilIx.Len() != 0 {
		t.Fatal("nil index should be empty")
	}
}

func TestParseDocSkipsFrontMatter(t *testing.T) {
	d := ParseDoc("p", "/x/0001-a.md", "decisions/0001-a.md", "---\nstatus: superseded\n---\n# 0001. A\n\n## Decision\n")
	if d.Title != "0001. A" || len(d.Headings) != 2 || d.Headings[0].Line != 3 {
		t.Fatalf("doc = %+v", d)
	}
	ix := &Index{docs: []Doc{d}}
	if hits := ix.Search("superseded", 10); len(hits) != 0 {
		t.Errorf("front matter matched: %+v", hits)
	}
}
