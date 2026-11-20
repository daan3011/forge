package transcript

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daan3011/forge/internal/store"
)

func parseFile(t *testing.T, path string) *Parser {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p := NewParser()
	feedLines(p, b)
	return p
}

func TestParseFixture(t *testing.T) {
	p := parseFile(t, "testdata/session.jsonl")
	got := p.Entries()

	type want struct {
		kind    Kind
		text    string
		tool    string
		summary string
		result  string
		isErr   bool
	}
	wants := []want{
		{kind: User, text: "Add a README"},
		{kind: Assistant, text: "I'll **write** it."},
		{kind: Tool, tool: "Write", summary: "/repo/README.md", result: "File created"},
		{kind: Tool, tool: "Bash", summary: "Run tests", result: "FAIL foo", isErr: true},
		{kind: Tool, tool: "Edit", summary: "/repo/main.go"},
		{kind: Notice, text: "/clear"},
		{kind: Notice, text: "conversation compacted"},
		{kind: Assistant, text: "Done."},
	}
	if len(got) != len(wants) {
		for i, e := range got {
			t.Logf("%d: %+v", i, e)
		}
		t.Fatalf("got %d entries, want %d", len(got), len(wants))
	}
	for i, w := range wants {
		e := got[i]
		if e.Kind != w.kind || e.Text != w.text || e.ToolName != w.tool || e.Summary != w.summary ||
			e.Result != w.result || e.IsError != w.isErr {
			t.Errorf("entry %d = %+v, want %+v", i, e, w)
		}
	}
	if p.Skipped() != 1 {
		t.Errorf("Skipped = %d, want 1", p.Skipped())
	}

	s := p.Summary()
	if s.Messages != 3 || s.ToolCalls != 3 {
		t.Errorf("Messages/ToolCalls = %d/%d, want 3/3", s.Messages, s.ToolCalls)
	}
	if strings.Join(s.FilesTouched, ",") != "/repo/README.md,/repo/main.go" {
		t.Errorf("FilesTouched = %v", s.FilesTouched)
	}
	if want := time.Date(2026, 11, 20, 10, 0, 12, 0, time.UTC); !s.LastActivity.Equal(want) {
		t.Errorf("LastActivity = %v, want %v", s.LastActivity, want)
	}
	// Usage counts once per message id; the sidechain is ignored.
	wantTok := store.Tokens{Input: 14, Output: 16, CacheRead: 100, CacheWrite: 20, Messages: 4}
	if s.Tokens != wantTok || s.Estimated {
		t.Errorf("Tokens = %+v (estimated %v), want %+v", s.Tokens, s.Estimated, wantTok)
	}
}

func TestParseLines(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  []Entry // only Kind, Text, Summary, Result compared
	}{
		{
			name:  "blank and unknown lines",
			lines: []string{"", "   ", `{"type":"mode"}`, `{"type":"assistant"}`, `[1,2]`},
		},
		{
			name: "consecutive text blocks of one message merge",
			lines: []string{
				`{"type":"assistant","message":{"id":"m","content":[{"type":"text","text":"a"}]}}`,
				`{"type":"assistant","message":{"id":"m","content":[{"type":"text","text":"b"}]}}`,
			},
			want: []Entry{{Kind: Assistant, Text: "a\n\nb"}},
		},
		{
			name: "text after a tool call starts a new entry",
			lines: []string{
				`{"type":"assistant","message":{"id":"m","content":[{"type":"text","text":"a"},{"type":"tool_use","id":"t","name":"Grep","input":{"pattern":"foo"}},{"type":"text","text":"b"}]}}`,
			},
			want: []Entry{{Kind: Assistant, Text: "a"}, {Kind: Tool, Summary: "foo"}, {Kind: Assistant, Text: "b"}},
		},
		{
			name: "tool result for unknown call is dropped",
			lines: []string{
				`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"nope","content":"x"}]}}`,
			},
		},
		{
			name: "unknown tool summarised by first string field",
			lines: []string{
				`{"type":"assistant","message":{"id":"m","content":[{"type":"tool_use","id":"t","name":"mcp__x","input":{"n":3,"b":"second","a":"first\nline"}}]}}`,
			},
			want: []Entry{{Kind: Tool, Summary: "first …"}},
		},
		{
			name: "user text blocks are a prompt",
			lines: []string{
				`{"type":"user","message":{"content":[{"type":"text","text":"hello"}]}}`,
			},
			want: []Entry{{Kind: User, Text: "hello"}},
		},
		{
			name: "image tool result",
			lines: []string{
				`{"type":"assistant","message":{"id":"m","content":[{"type":"tool_use","id":"t","name":"Read","input":{"file_path":"/a.png"}}]}}`,
				`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t","content":[{"type":"image","source":{}}]}]}}`,
			},
			want: []Entry{{Kind: Tool, Summary: "/a.png", Result: "[image]"}},
		},
		{
			name: "slash command with args and stdout",
			lines: []string{
				`{"type":"user","message":{"content":"<command-name>/model</command-name><command-args>opus</command-args>"}}`,
				`{"type":"user","message":{"content":"<local-command-stdout>Set model to opus</local-command-stdout>"}}`,
			},
			want: []Entry{{Kind: Notice, Text: "/model opus"}, {Kind: Notice, Text: "Set model to opus"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewParser()
			for _, l := range tt.lines {
				p.Feed([]byte(l))
			}
			got := p.Entries()
			if len(got) != len(tt.want) {
				t.Fatalf("got %d entries %+v, want %d", len(got), got, len(tt.want))
			}
			for i, w := range tt.want {
				g := got[i]
				if g.Kind != w.Kind || g.Text != w.Text || g.Summary != w.Summary || g.Result != w.Result {
					t.Errorf("entry %d = %+v, want %+v", i, g, w)
				}
			}
		})
	}
}

func TestSummaryEstimate(t *testing.T) {
	p := NewParser()
	p.Feed([]byte(`{"type":"user","message":{"content":"12345678"}}`))
	s := p.Summary()
	if !s.Estimated || s.Tokens.Input != 2 {
		t.Fatalf("Summary = %+v, want estimated 2 tokens", s)
	}
}

func TestCacheIncremental(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	write := func(flag int, s string) {
		t.Helper()
		f, err := os.OpenFile(path, flag|os.O_WRONLY|os.O_CREATE, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(s); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	prompt := func(s string) string {
		return `{"type":"user","message":{"content":"` + s + `"}}`
	}
	texts := func(tr Transcript) string {
		var out []string
		for _, e := range tr.Entries {
			out = append(out, e.Text)
		}
		return strings.Join(out, ",")
	}

	c := NewCache()
	write(os.O_TRUNC, prompt("a")+"\n"+prompt("b")[:10])
	tr, err := c.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if texts(tr) != "a" {
		t.Fatalf("first load = %q, want a (partial line held back)", texts(tr))
	}

	// Completing the partial line and appending more is picked up.
	write(os.O_APPEND, prompt("b")[10:]+"\n"+prompt("c")+"\n")
	if tr, _ = c.Load(path); texts(tr) != "a,b,c" {
		t.Fatalf("second load = %q, want a,b,c", texts(tr))
	}
	if st := c.state(path); st.offset != st.info.Size() {
		t.Fatalf("offset %d, size %d", st.offset, st.info.Size())
	}

	// A truncated file starts over.
	write(os.O_TRUNC, prompt("z")+"\n")
	if tr, _ = c.Load(path); texts(tr) != "z" {
		t.Fatalf("after truncate = %q, want z", texts(tr))
	}

	// A replaced file (new inode) starts over even if it is larger.
	repl := path + ".new"
	if err := os.WriteFile(repl, []byte(prompt("x")+"\n"+prompt("y")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(repl, path); err != nil {
		t.Fatal(err)
	}
	if tr, _ = c.Load(path); texts(tr) != "x,y" {
		t.Fatalf("after replace = %q, want x,y", texts(tr))
	}

	if _, err := c.Load(filepath.Join(t.TempDir(), "missing.jsonl")); !errors.Is(err, ErrNoTranscript) {
		t.Fatalf("missing file err = %v, want ErrNoTranscript", err)
	}
}

func TestLocate(t *testing.T) {
	dir := t.TempDir()
	const id = "11111111-2222-3333-4444-555555555555"
	proj := filepath.Join(dir, "-repo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	scanned := filepath.Join(proj, id+".jsonl")
	if err := os.WriteFile(scanned, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	hooked := filepath.Join(dir, "hooked.jsonl")
	if err := os.WriteFile(hooked, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		x       store.Session
		want    string
		wantErr error
	}{
		{"hook path wins", store.Session{TranscriptPath: hooked, ClaudeSessionID: id}, hooked, nil},
		{"stale hook path falls back to scan", store.Session{TranscriptPath: filepath.Join(dir, "gone.jsonl"), ClaudeSessionID: id}, scanned, nil},
		{"scan only", store.Session{ClaudeSessionID: id}, scanned, nil},
		{"nothing", store.Session{ClaudeSessionID: "99999999-2222-3333-4444-555555555555"}, "", ErrNoTranscript},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Locate(tt.x, dir)
			if got != tt.want || !errors.Is(err, tt.wantErr) {
				t.Fatalf("Locate = %q, %v; want %q, %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

var realDir = flag.String("real", "", "parse every transcript under this projects dir (e.g. ~/.claude/projects)")

// TestRealFiles sanity-checks the parser against real transcripts. It only
// runs with -real.
func TestRealFiles(t *testing.T) {
	if *realDir == "" {
		t.Skip("pass -real <projects dir> to run")
	}
	files, err := filepath.Glob(filepath.Join(*realDir, "*", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	c := NewCache()
	var total, withMessages int
	for _, f := range files {
		tr, err := c.Load(f)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		total++
		if tr.Summary.Messages > 0 {
			withMessages++
		}
	}
	t.Logf("parsed %d files, %d with messages", total, withMessages)
	if total > 0 && withMessages == 0 {
		t.Fatal("no messages found in any transcript")
	}
}
