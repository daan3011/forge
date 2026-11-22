package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var adrNow = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

func TestSlug(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Use SQLite for the cache", "use-sqlite-for-the-cache"},
		{"  Go 1.26 / Charm v2!  ", "go-1-26-charm-v2"},
		{"---", "decision"},
		{"", "decision"},
		{"Ünïcode ok", "n-code-ok"},
		{strings.Repeat("ab ", 40), strings.TrimRight(strings.Repeat("ab-", 20), "-")},
	}
	for _, tt := range tests {
		if got := Slug(tt.in); got != tt.want {
			t.Errorf("Slug(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestStripNumber(t *testing.T) {
	tests := []struct{ in, want string }{
		{"0007. Use SQLite", "Use SQLite"},
		{"7: Use SQLite", "Use SQLite"},
		{"2026 plans", "2026 plans"},
		{"Use SQLite", "Use SQLite"},
		{"12.", "12."},
	}
	for _, tt := range tests {
		if got := stripNumber(tt.in); got != tt.want {
			t.Errorf("stripNumber(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSplitFrontMatter(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantMeta map[string]string
		wantBody string
	}{
		{"none", "# Title\n", nil, "# Title\n"},
		{
			"basic", "---\ndate: 2026-11-22\nstatus: accepted\n---\n# T\n",
			map[string]string{"date": "2026-11-22", "status": "accepted"},
			"# T\n",
		},
		{"crlf", "---\r\nstatus: superseded\r\n---\r\nbody", map[string]string{"status": "superseded"}, "body"},
		{"empty block", "---\n---\nbody", map[string]string{}, "body"},
		{"unterminated", "---\nstatus: x\n# T\n", nil, "---\nstatus: x\n# T\n"},
		{"at end", "---\nstatus: x\n---", map[string]string{"status": "x"}, ""},
		{"bad yaml", "---\n: [\n---\nbody", nil, "---\n: [\n---\nbody"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta, body := SplitFrontMatter([]byte(tt.in))
			if (meta == nil) != (tt.wantMeta == nil) || len(meta) != len(tt.wantMeta) {
				t.Fatalf("meta = %v, want %v", meta, tt.wantMeta)
			}
			for k, v := range tt.wantMeta {
				if meta[k] != v {
					t.Errorf("meta[%q] = %q, want %q", k, meta[k], v)
				}
			}
			if string(body) != tt.wantBody {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
		})
	}
}

func TestFrontMatterLines(t *testing.T) {
	if got := FrontMatterLines("---\na: b\nc: d\n---\n# T\n"); got != 4 {
		t.Errorf("FrontMatterLines = %d, want 4", got)
	}
	if got := FrontMatterLines("# T\n"); got != 0 {
		t.Errorf("FrontMatterLines = %d, want 0", got)
	}
}

func TestNewDecisionNumbersAndRenders(t *testing.T) {
	root := t.TempDir()
	if n := NextNumber(root); n != 1 {
		t.Fatalf("NextNumber(empty) = %d", n)
	}
	d, err := NewDecision(root, DecisionInput{Title: "Use SQLite", Decision: "SQLite via modernc.", Context: "Need a cache."}, adrNow)
	if err != nil {
		t.Fatal(err)
	}
	if d.Number != 1 || d.Rel != "decisions/0001-use-sqlite.md" || d.Status != StatusAccepted {
		t.Fatalf("decision = %+v", d)
	}
	b, _ := os.ReadFile(d.Path)
	want := "---\ndate: 2026-11-22\nstatus: accepted\n---\n# 0001. Use SQLite\n\n## Context\n\nNeed a cache.\n\n## Decision\n\nSQLite via modernc.\n"
	if string(b) != want {
		t.Errorf("file =\n%s\nwant\n%s", b, want)
	}
	// A gap and a stray file: the next number follows the highest.
	if err := os.WriteFile(filepath.Join(root, DecisionsDir, "0009-later.md"), []byte("# 0009. Later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(root, DecisionsDir, "notes.md"), []byte("x"), 0o644)
	d2, err := NewDecision(root, DecisionInput{Title: "Use SQLite", Decision: "again"}, adrNow)
	if err != nil {
		t.Fatal(err)
	}
	if d2.Number != 10 || d2.File() != "0010-use-sqlite.md" {
		t.Errorf("second = %+v", d2)
	}
}

func TestNewDecisionValidates(t *testing.T) {
	root := t.TempDir()
	for _, in := range []DecisionInput{{Decision: "x"}, {Title: "x", Decision: "  "}} {
		if _, err := NewDecision(root, in, adrNow); err == nil {
			t.Errorf("NewDecision(%+v) succeeded", in)
		}
	}
}

func TestListDecisionsNewestFirst(t *testing.T) {
	root := t.TempDir()
	if ds, err := ListDecisions(root); err != nil || len(ds) != 0 {
		t.Fatalf("missing dir: %v, %v", ds, err)
	}
	for _, title := range []string{"One", "Two", "Three"} {
		if _, err := NewDecision(root, DecisionInput{Title: title, Decision: "d"}, adrNow); err != nil {
			t.Fatal(err)
		}
	}
	// No front matter, no heading: defaults from the file name.
	_ = os.WriteFile(filepath.Join(root, DecisionsDir, "0004-plain-file.md"), []byte("text\n"), 0o644)
	ds, err := ListDecisions(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range ds {
		got = append(got, d.Label()+" "+d.Status)
	}
	want := []string{"0004. Plain file accepted", "0003. Three accepted", "0002. Two accepted", "0001. One accepted"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSupersede(t *testing.T) {
	root := t.TempDir()
	old, _ := NewDecision(root, DecisionInput{Title: "Use Postgres", Decision: "pg"}, adrNow)
	mid, _ := NewDecision(root, DecisionInput{Title: "Use MySQL", Decision: "my"}, adrNow)
	newer, _ := NewDecision(root, DecisionInput{Title: "Use SQLite", Decision: "lite"}, adrNow)

	if err := Supersede(newer, old); err == nil {
		t.Error("older superseding newer succeeded")
	}
	if err := Supersede(old, old); err == nil {
		t.Error("self-supersede succeeded")
	}
	if err := Supersede(old, mid); err != nil {
		t.Fatal(err)
	}
	// Superseding again replaces the link rather than adding a second one.
	if err := Supersede(old, newer); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(old.Path)
	want := "---\ndate: 2026-11-22\nstatus: superseded\nsuperseded_by: 0003-use-sqlite.md\n---\n" +
		"# 0001. Use Postgres\n\n> Superseded by [0003. Use SQLite](0003-use-sqlite.md)\n\n## Decision\n\npg\n"
	if string(b) != want {
		t.Errorf("old =\n%q\nwant\n%q", b, want)
	}
	got, err := ReadDecision(newer.Path, newer.Rel)
	if err != nil {
		t.Fatal(err)
	}
	if got.Supersedes != "0001-use-postgres.md" || got.Status != StatusAccepted {
		t.Errorf("newer = %+v", got)
	}
	ds, _ := ListDecisions(root)
	sup, _ := ReadDecision(old.Path, old.Rel)
	if !sup.Superseded() || sup.SupersededBy != "0003-use-sqlite.md" || len(ds) != 3 {
		t.Errorf("old after = %+v", sup)
	}
	if err := Supersede(mid, sup); err == nil {
		t.Error("superseding with a superseded decision succeeded")
	}
}

func TestEnsureClaudeMD(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, ".forge", "docs", DecisionsDir)

	added, err := EnsureClaudeMD(repo, dir)
	if err != nil || !added {
		t.Fatalf("first = %v, %v", added, err)
	}
	added, err = EnsureClaudeMD(repo, dir)
	if err != nil || added {
		t.Fatalf("second = %v, %v", added, err)
	}
	b, _ := os.ReadFile(filepath.Join(repo, "CLAUDE.md"))
	if strings.Count(string(b), ".forge/docs/decisions/") != 1 || strings.HasPrefix(string(b), "\n") {
		t.Errorf("CLAUDE.md = %q", b)
	}

	// An existing file keeps its content and gets a blank line first.
	repo2 := t.TempDir()
	_ = os.WriteFile(filepath.Join(repo2, "CLAUDE.md"), []byte("# proj\n- rule"), 0o644)
	if _, err := EnsureClaudeMD(repo2, filepath.Join(repo2, "docs", DecisionsDir)); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(repo2, "CLAUDE.md"))
	if !strings.HasPrefix(string(b), "# proj\n- rule\n\n- Architecture decisions are logged in docs/decisions/") {
		t.Errorf("CLAUDE.md = %q", b)
	}

	// Outside the repo or no repo: nothing to do.
	if added, err := EnsureClaudeMD(repo, t.TempDir()); added || err != nil {
		t.Errorf("outside = %v, %v", added, err)
	}
	if added, err := EnsureClaudeMD("", dir); added || err != nil {
		t.Errorf("no repo = %v, %v", added, err)
	}
}

func TestLogDecision(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, ".forge", "docs")
	l, err := LogDecision(docs, repo, DecisionInput{Title: "T", Decision: "D"}, adrNow)
	if err != nil || !l.Noted || l.NoteErr != nil || l.Number != 1 {
		t.Fatalf("first = %+v, %v", l, err)
	}
	l, err = LogDecision(docs, repo, DecisionInput{Title: "T2", Decision: "D"}, adrNow)
	if err != nil || l.Noted || l.Number != 2 {
		t.Fatalf("second = %+v, %v", l, err)
	}
}
