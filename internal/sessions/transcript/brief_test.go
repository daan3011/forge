package transcript

import (
	"strings"
	"testing"
)

func TestBrief(t *testing.T) {
	p := parseFile(t, "testdata/session.jsonl")
	fixture := Transcript{Entries: p.Entries(), Summary: p.Summary()}
	long := strings.Repeat("word ", 100)
	tests := []struct {
		name     string
		t        Transcript
		worktree string
		n        int
		want     string
	}{
		{
			name: "fixture", t: fixture, worktree: "/repo", n: 3,
			want: "3 messages · 3 tool calls · 2 files: README.md, main.go\n" +
				"Last replies:\n- I'll **write** it.\n- Done.",
		},
		{
			name: "only the last reply", t: fixture, worktree: "/repo", n: 1,
			want: "3 messages · 3 tool calls · 2 files: README.md, main.go\nLast replies:\n- Done.",
		},
		{
			name: "empty", t: Transcript{}, n: 3,
			want: "0 messages · 0 tool calls",
		},
		{
			name: "long multi-line reply is cut",
			t: Transcript{
				Entries: []Entry{{Kind: Assistant, Text: "first\n\n" + long}, {Kind: Assistant, Text: "  \n"}},
				Summary: Summary{Messages: 1},
			},
			n:    3,
			want: "1 message · 0 tool calls\nLast replies:\n- first " + strings.TrimSpace(long[:233]) + "…",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Brief(tt.t, tt.worktree, tt.n); got != tt.want {
				t.Errorf("Brief =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestShortFiles(t *testing.T) {
	files := []string{"/wt/a/x.go", "/wt/a/y.go", "/wt/a/z.go", "/wt/a/q.go", "/other/r.go", "/wt/a/s.go"}
	tests := []struct {
		root  string
		shown int
		want  string
	}{
		{"/wt/a", 4, "x.go, y.go, z.go, q.go, +2"},
		{"/wt/a/", 6, "x.go, y.go, z.go, q.go, /other/r.go, s.go"},
		{"", 1, "/wt/a/x.go, +5"},
	}
	for _, tt := range tests {
		if got := ShortFiles(files, tt.root, tt.shown); got != tt.want {
			t.Errorf("ShortFiles(%q, %d) = %q, want %q", tt.root, tt.shown, got, tt.want)
		}
	}
}
