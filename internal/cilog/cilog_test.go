package cilog

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestClean(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"empty", "", nil},
		{"bom and timestamp", "\xef\xbb\xbf2026-11-20T10:00:00.1234567Z hello\n", []string{"hello"}},
		{"timestamp without fraction", "2026-11-20T10:00:00Z hi", []string{"hi"}},
		{"no timestamp kept as is", "plain line", []string{"plain line"}},
		{"ansi", "2026-11-20T10:00:00.1Z \x1b[31mred\x1b[0m text", []string{"red text"}},
		{"crlf", "a\r\nb\r\n", []string{"a", "b"}},
		{"progress redraw", "10%\r50%\r100%", []string{"100%"}},
		{"tabs", "ok\tpkg", []string{"ok    pkg"}},
		{"groups", "##[group]Run x\ninside\n##[endgroup]\nafter", []string{"▸ Run x", "inside", "after"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Clean([]byte(tt.raw)); !slices.Equal(got, tt.want) {
				t.Errorf("Clean(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestIsErrorLine(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"##[error]Process completed with exit code 1.", true},
		{"--- FAIL: TestX (0.01s)", true},
		{"Error: cannot find module", true},
		{"build FAILED", true},
		{"panic: runtime error", true},
		{"exited with Exit Code 2", true},
		{"ok  pkg 0.1s", false},
		{"fail fast is a mindset", false}, // lower-case "fail" alone is not FAIL
		{"DeprecationWarning: url.parse() is prone to errors", false},
		{"cc -Werror main.c", false},
		{"make: *** [Makefile:12: test] Error 1", true},
		{"main.go:3:1: error: expected ';'", true},
		{"goroutine 1 [running]: panic(...)", true},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsErrorLine(tt.line); got != tt.want {
			t.Errorf("IsErrorLine(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

func lines(spec string) []string {
	// "x" is an error line, "." is not.
	out := make([]string, len(spec))
	for i, c := range spec {
		if c == 'x' {
			out[i] = "error"
		} else {
			out[i] = "ok"
		}
	}
	return out
}

func TestExtract(t *testing.T) {
	tests := []struct {
		name string
		spec string
		ctx  int
		want []Hunk
	}{
		{"none", ".....", 2, nil},
		{"middle", "....x....", 2, []Hunk{{2, 7}}},
		{"start edge", "x....", 2, []Hunk{{0, 3}}},
		{"end edge", "....x", 2, []Hunk{{2, 5}}},
		{"overlap merges", "..x.x..", 1, []Hunk{{1, 6}}},
		{"touching merges", "x..x", 1, []Hunk{{0, 4}}},
		{"apart", "x.....x", 1, []Hunk{{0, 2}, {5, 7}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Extract(lines(tt.spec), tt.ctx); !slices.Equal(got, tt.want) {
				t.Errorf("Extract(%s, %d) = %v, want %v", tt.spec, tt.ctx, got, tt.want)
			}
		})
	}
}

func TestCollapse(t *testing.T) {
	l := lines("x.....x...")
	v := Collapse(l, Extract(l, 1))
	var got []string
	for _, x := range v.Lines {
		got = append(got, x.Text)
	}
	want := []string{"error", "ok", "⋯ 3 lines hidden ⋯", "ok", "error", "ok", "⋯ 2 lines hidden ⋯"}
	if !slices.Equal(got, want) || v.Tail {
		t.Fatalf("Collapse = %q (tail %v), want %q", got, v.Tail, want)
	}
	if v.Lines[2].Orig != -1 || v.Lines[4].Orig != 6 {
		t.Errorf("Orig mapping wrong: %+v", v.Lines)
	}
}

func TestCollapseTail(t *testing.T) {
	l := make([]string, TailLines+3)
	for i := range l {
		l[i] = "ok"
	}
	v := Collapse(l, nil)
	if !v.Tail || len(v.Lines) != TailLines+1 || v.Lines[0].Text != "⋯ 3 lines hidden ⋯" || v.Lines[1].Orig != 3 {
		t.Fatalf("tail view wrong: tail=%v len=%d first=%q", v.Tail, len(v.Lines), v.Lines[0].Text)
	}
	short := Collapse([]string{"a", "b"}, nil)
	if len(short.Lines) != 2 || short.Lines[0].Orig != 0 {
		t.Errorf("short tail = %+v", short.Lines)
	}
}

func TestFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/job.log")
	if err != nil {
		t.Fatal(err)
	}
	l := Clean(raw)
	if len(l) != 27 || l[0] != "▸ Run actions/checkout@v4" {
		t.Fatalf("Clean: %d lines, first %q", len(l), l[0])
	}
	for _, s := range l {
		if strings.Contains(s, "\x1b") || strings.HasPrefix(s, "2026-") || strings.Contains(s, "endgroup") {
			t.Errorf("unclean line %q", s)
		}
	}
	h := Extract(l, Context)
	if !slices.Equal(h, []Hunk{{7, 27}}) {
		t.Fatalf("Extract = %v", h)
	}
	v := Collapse(l, h)
	if v.Lines[0].Text != "⋯ 7 lines hidden ⋯" || v.Lines[len(v.Lines)-1].Text != "Post job cleanup." {
		t.Errorf("collapsed = %+v", v.Lines)
	}
	joined := strings.Join(SummaryLines(v), "\n")
	for _, want := range []string{"--- FAIL: TestPipelineLiveProgress", "##[error]Process completed with exit code 2."} {
		if !strings.Contains(joined, want) {
			t.Errorf("summary misses %q", want)
		}
	}
}

func TestSummaryLinesCap(t *testing.T) {
	var v View
	for i := range 500 {
		v.Lines = append(v.Lines, Line{Text: "error line", Orig: i})
	}
	got := SummaryLines(v)
	if len(got) != maxSummaryLines+1 || got[len(got)-1] != "⋯ truncated ⋯" {
		t.Errorf("line cap: %d lines, last %q", len(got), got[len(got)-1])
	}
	v.Lines = []Line{{Text: strings.Repeat("e", maxSummaryBytes-10)}, {Text: strings.Repeat("e", 100)}}
	got = SummaryLines(v)
	if len(got) != 2 || got[1] != "⋯ truncated ⋯" {
		t.Errorf("byte cap: %d lines", len(got))
	}
}

func TestSummaryKey(t *testing.T) {
	if got := SummaryKey("Daan/Forge", 12, 34); got != "ci_errors:daan/forge#12:34" {
		t.Errorf("SummaryKey = %q", got)
	}
}
