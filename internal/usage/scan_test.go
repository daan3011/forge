package usage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/store"
)

type fixture struct {
	t   *testing.T
	dir string // projects dir
	st  *store.Store
	svc *Service
}

func newFixture(t *testing.T, sessions ...store.Session) *fixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for _, x := range sessions {
		if _, err := st.Sessions().Create(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	f := &fixture{t: t, dir: t.TempDir(), st: st}
	f.svc = New(Config{
		Ledger: st.Usage(), Sessions: st.Sessions(), ProjectsDir: f.dir,
		Projects: []config.Project{
			{Name: "forge", Path: "/code/forge"},
			{Name: "forge-web", Path: "/code/forge/web"},
		},
		Usage: config.Usage{Days: 14, TopSessions: 10, Prices: map[string]config.Price{"m": {Input: 1e6}}},
		Now:   func() time.Time { return time.Date(2026, 11, 20, 18, 0, 0, 0, time.Local) },
	})
	return f
}

// msg is one assistant transcript line.
func msg(id, cwd string, in int64, at string) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"cwd":%q,"requestId":"req-%s","message":{"id":%q,"model":"m","role":"assistant","usage":{"input_tokens":%d,"output_tokens":1}}}`+"\n",
		at, cwd, id, id, in)
}

func (f *fixture) write(rel, body string) string {
	f.t.Helper()
	p := filepath.Join(f.dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return p
}

func (f *fixture) appendTo(rel, body string) {
	f.t.Helper()
	fh, err := os.OpenFile(filepath.Join(f.dir, rel), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		f.t.Fatal(err)
	}
	defer fh.Close()
	if _, err := fh.WriteString(body); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) scan() ScanResult {
	f.t.Helper()
	res, err := f.svc.Scan(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return res
}

// inputs is the input tokens per session in the rollups.
func (f *fixture) inputs() map[string]int64 {
	f.t.Helper()
	rows, err := f.st.Usage().Rows(context.Background(), time.Time{}, "")
	if err != nil {
		f.t.Fatal(err)
	}
	out := map[string]int64{}
	for _, r := range rows {
		out[r.ClaudeSessionID] += r.Input
	}
	return out
}

func (f *fixture) projectOf(session string) string {
	f.t.Helper()
	rows, _ := f.st.Usage().Rows(context.Background(), time.Time{}, session)
	if len(rows) == 0 {
		return ""
	}
	return rows[0].Project
}

// TestScanSharedMessages: a resumed session's file repeats the original's
// messages; each message counts once, for the original session.
func TestScanSharedMessages(t *testing.T) {
	f := newFixture(t)
	f.write("p/aaaa.jsonl", msg("m1", "/code/forge", 10, "2026-11-19T09:00:00Z")+msg("m2", "/code/forge", 20, "2026-11-19T09:05:00Z"))
	// The resumed copy sorts first by name but its first message is the
	// same, older one; its own new message is later.
	f.write("p/0000.jsonl", msg("m1", "/code/forge", 10, "2026-11-19T09:00:00Z")+
		msg("m2", "/code/forge", 20, "2026-11-19T09:05:00Z")+msg("m3", "/code/forge", 5, "2026-11-20T08:00:00Z"))
	res := f.scan()
	if res.Files != 2 || res.Read != 2 || res.Messages != 5 {
		t.Errorf("scan = %+v", res)
	}
	got := f.inputs()
	if got["aaaa"]+got["0000"] != 35 {
		t.Fatalf("total = %v, want 35 (shared messages once)", got)
	}
	// Rescanning changes nothing.
	f.scan()
	if again := f.inputs(); again["aaaa"]+again["0000"] != 35 {
		t.Errorf("after rescan %v", again)
	}
}

// TestScanOrderCreditsOriginal: when a fork file is already read and the
// original is new, credit still goes by ingest order (first file wins), and
// within one scan the file with the oldest message goes first.
func TestScanOrderCreditsOriginal(t *testing.T) {
	f := newFixture(t)
	f.write("p/zzzz.jsonl", msg("m1", "/x", 10, "2026-11-19T09:00:00Z"))
	f.write("p/aaaa.jsonl", msg("m0", "/x", 1, "2026-11-20T09:00:00Z")+msg("m1", "/x", 10, "2026-11-19T09:00:00Z"))
	f.scan()
	// aaaa's earliest message is m1 as well, so both tie; stable order by
	// path puts aaaa first.
	got := f.inputs()
	if got["aaaa"] != 11 || got["zzzz"] != 0 {
		t.Errorf("credit = %v", got)
	}
}

func TestScanSubagentsAndProjects(t *testing.T) {
	f := newFixture(t, store.Session{Name: "fix-ci", ClaudeSessionID: "forged", Project: "forge"})
	f.write("p/sess.jsonl", msg("a1", "/code/forge/web/src", 3, "2026-11-20T09:00:00Z"))
	f.write("p/sess/subagents/agent-1.jsonl", msg("a2", "/code/forge/web", 4, "2026-11-20T09:01:00Z"))
	f.write("q/forged.jsonl", msg("b1", "/wt/fix-ci", 7, "2026-11-20T09:00:00Z"))
	f.write("r/loose.jsonl", msg("c1", "/elsewhere", 1, "2026-11-20T09:00:00Z"))
	f.scan()
	if got := f.inputs(); got["sess"] != 7 || got["forged"] != 7 || got["loose"] != 1 || len(got) != 3 {
		t.Errorf("inputs = %v", got)
	}
	for sess, want := range map[string]string{"sess": "forge-web", "forged": "forge", "loose": Other} {
		if got := f.projectOf(sess); got != want {
			t.Errorf("%s project = %q, want %q", sess, got, want)
		}
	}
	r, err := f.svc.Report(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) != 3 || len(r.Projects) != 3 {
		t.Fatalf("report = %+v", r)
	}
	labels := map[string]string{}
	for _, s := range r.Sessions {
		labels[s.ClaudeSessionID] = s.Label()
	}
	if labels["forged"] != "fix-ci" || labels["loose"] != "loose · elsewhere" {
		t.Errorf("labels = %v", labels)
	}
	tot, err := f.svc.Session(context.Background(), "sess")
	if err != nil || tot.Input != 7 || tot.Output != 2 || tot.Cost != 7 {
		t.Errorf("session totals = %+v, %v", tot, err)
	}
}

func TestScanIncremental(t *testing.T) {
	f := newFixture(t)
	partial := `{"type":"assistant","timestamp":"2026-11-20T09:09:00Z","message":{"id":"m3","usage":{"input_tokens":100`
	f.write("p/s.jsonl", msg("m1", "/x", 1, "2026-11-20T09:00:00Z")+"{\"usage\": broken\n"+partial)
	res := f.scan()
	if f.inputs()["s"] != 1 || res.Bad != 1 {
		t.Fatalf("first scan: %v %+v", f.inputs(), res)
	}
	// The partial line completes and a repeat of m1 (streamed again, larger)
	// replaces its earlier value.
	f.appendTo("p/s.jsonl", "}}}\n"+msg("m1", "/x", 2, "2026-11-20T09:00:00Z"))
	f.scan()
	if got := f.inputs()["s"]; got != 102 {
		t.Errorf("after append = %d, want 102", got)
	}
	// Unchanged size: nothing read.
	if res := f.scan(); res.Read != 0 {
		t.Errorf("unchanged scan read %d files", res.Read)
	}
}

func TestScanReplacedFile(t *testing.T) {
	f := newFixture(t)
	f.write("p/s.jsonl", msg("m1", "/x", 50, "2026-11-20T09:00:00Z")+msg("m2", "/x", 50, "2026-11-20T09:01:00Z"))
	f.scan()
	// Truncated: read again from the start.
	f.write("p/s.jsonl", msg("m9", "/x", 3, "2026-11-20T10:00:00Z"))
	f.scan()
	if got := f.inputs()["s"]; got != 3 {
		t.Errorf("after truncate = %d, want 3", got)
	}
	// Replaced by a longer file with a different start.
	f.write("p/s.jsonl", msg("n1", "/x", 4, "2026-11-20T11:00:00Z")+msg("n2", "/x", 4, "2026-11-20T11:01:00Z")+strings.Repeat("\n", 10))
	f.scan()
	if got := f.inputs()["s"]; got != 8 {
		t.Errorf("after replace = %d, want 8", got)
	}
}

func TestScanNoDir(t *testing.T) {
	f := newFixture(t)
	f.svc.cfg.ProjectsDir = filepath.Join(f.dir, "missing")
	if res := f.scan(); res.Files != 0 {
		t.Errorf("res = %+v", res)
	}
}
