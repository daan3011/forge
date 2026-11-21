package cifix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/daan3011/forge/internal/cilog"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/store"
)

type fakeKV struct {
	m    map[string][]byte
	puts int
}

func newKV() *fakeKV { return &fakeKV{m: map[string][]byte{}} }

func (k *fakeKV) Get(_ context.Context, key string) (store.CacheEntry, error) {
	v, ok := k.m[key]
	if !ok {
		return store.CacheEntry{}, store.ErrNotFound
	}
	return store.CacheEntry{Key: key, Value: v}, nil
}

func (k *fakeKV) Put(_ context.Context, key, kind, _ string, value []byte, _ time.Duration) error {
	if kind != cilog.Kind {
		return fmt.Errorf("kind %q", kind)
	}
	k.puts++
	k.m[key] = value
	return nil
}

type fakeLogs struct {
	logs  map[int64]string
	errs  map[int64]error
	calls int
}

func (f *fakeLogs) JobLogs(_ context.Context, _ string, jobID int64) ([]byte, error) {
	f.calls++
	if err := f.errs[jobID]; err != nil {
		return nil, err
	}
	return []byte(f.logs[jobID]), nil
}

var run = Run{
	Repo: "acme/app", ID: 77, Number: 12, Attempt: 1, Workflow: "CI", Branch: "feat/x",
	HeadSHA: "abc1234", URL: "https://github.com/acme/app/actions/runs/77", CommitMsg: "Fix the parser",
}

func TestSummarize(t *testing.T) {
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	kv := newKV()
	cached := NewSummary(run, 1, "lint", "u1", cilog.View{Lines: []cilog.Line{{Text: "cached error"}}}, now)
	if err := Save(context.Background(), kv, "p", cached); err != nil {
		t.Fatal(err)
	}
	logs := &fakeLogs{
		logs: map[int64]string{2: "2026-11-21T09:00:00.000Z ok\n2026-11-21T09:00:01.000Z ##[error]boom\n"},
		errs: map[int64]error{3: github.ErrLogsUnavailable},
	}
	targets := []Target{
		{Run: run, JobID: 1, Job: "lint", URL: "u1"},
		{Run: run, JobID: 2, Job: "test", URL: "u2"},
		{Run: run, JobID: 3, Job: "e2e", URL: "u3"},
		{Run: run, JobID: 4, Job: "dropped", URL: "u4"}, // over MaxJobs
	}
	f := Summarize(context.Background(), logs, kv, "p", now, targets)
	if f.Repo != "acme/app" || f.Branch != "feat/x" || f.HeadSHA != "abc1234" || f.CommitMessage != "Fix the parser" {
		t.Errorf("failure = %+v", f)
	}
	if len(f.Jobs) != MaxJobs {
		t.Fatalf("jobs = %+v", f.Jobs)
	}
	if got := f.Jobs[0].Lines; len(got) != 1 || got[0] != "cached error" {
		t.Errorf("cached job lines = %q", got)
	}
	if !containsLine(f.Jobs[1].Lines, "##[error]boom") {
		t.Errorf("fetched job lines = %q", f.Jobs[1].Lines)
	}
	if !strings.Contains(f.Jobs[2].NoLog, "unavailable") {
		t.Errorf("unavailable NoLog = %q", f.Jobs[2].NoLog)
	}
	if logs.calls != 2 {
		t.Errorf("log downloads = %d, want 2 (cache hit skipped)", logs.calls)
	}
	// The fetched summary was saved for next time.
	s, ok := Load(context.Background(), kv, "ACME/app", 77, 2)
	if !ok || s.Job != "test" || s.Workflow != "CI" || !s.ExtractedAt.Equal(now) {
		t.Errorf("saved summary = %+v %v", s, ok)
	}
}

func containsLine(lines []string, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func TestSummarizeNoLogsNoKV(t *testing.T) {
	f := Summarize(context.Background(), nil, nil, "", time.Now(), []Target{
		{Run: Run{Repo: "a/b", Workflow: "ext"}, Job: "buildkite", URL: "https://bk/1"},
		{Run: Run{Repo: "a/b", ID: 5}, JobID: 9, Job: "test"},
	})
	if !strings.Contains(f.Jobs[0].NoLog, "not a GitHub Actions job") || !strings.Contains(f.Jobs[0].NoLog, "https://bk/1") {
		t.Errorf("status NoLog = %q", f.Jobs[0].NoLog)
	}
	if f.Jobs[1].NoLog != "log not available" || f.Jobs[1].RunURL != RunURL("a/b", 5) {
		t.Errorf("job = %+v", f.Jobs[1])
	}
}

func TestLogError(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{github.ErrLogsUnavailable, "unavailable"},
		{fmt.Errorf("x: %w", github.ErrLogTooLarge), "too large"},
		{errors.New("HTTP 500"), "HTTP 500"},
	}
	for _, tt := range tests {
		if got := logError(tt.err); !strings.Contains(got, tt.want) {
			t.Errorf("logError(%v) = %q", tt.err, got)
		}
	}
}

func TestVars(t *testing.T) {
	tests := []struct {
		name string
		f    Failure
		want map[string]string
	}{
		{
			name: "one job",
			f: Failure{CommitMessage: "Fix it\n\nbody", Jobs: []JobFailure{
				{Workflow: "CI", Job: "test", RunURL: "r1", Lines: []string{"--- FAIL: TestX", "exit code 1"}},
			}},
			want: map[string]string{
				VarWorkflow: "CI", VarJob: "test", VarRun: "r1", VarCommitMessage: "Fix it\n\nbody",
				VarFailingLog: "── CI › test ──\n--- FAIL: TestX\nexit code 1",
			},
		},
		{
			name: "several jobs, tail and no log",
			f: Failure{Jobs: []JobFailure{
				{Workflow: "CI", Job: "test", RunURL: "r1", Lines: []string{"a"}, Tail: true},
				{Workflow: "CI", Job: "lint", RunURL: "r1", NoLog: "log unavailable"},
				{Job: "ext", URL: "u"},
			}},
			want: map[string]string{
				VarWorkflow: "CI", VarJob: "test, lint, ext", VarRun: "r1", VarCommitMessage: "",
				VarFailingLog: "── CI › test ──\n(no error lines matched; last lines of the log)\na\n\n" +
					"── CI › lint ──\n(log unavailable)\n\n── ext ──",
			},
		},
		{
			name: "empty",
			f:    Failure{},
			want: map[string]string{VarWorkflow: "", VarJob: "", VarRun: "", VarCommitMessage: "", VarFailingLog: ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Vars(tt.f)
			if len(got) != len(tt.want) {
				t.Errorf("vars = %q", got)
			}
			for k, w := range tt.want {
				if got[k] != w {
					t.Errorf("%s = %q, want %q", k, got[k], w)
				}
			}
		})
	}
}

func TestVarsCapsLog(t *testing.T) {
	line := strings.Repeat("x", 99)
	var lines []string
	for range 200 {
		lines = append(lines, line)
	}
	f := Failure{Jobs: []JobFailure{
		{Job: "a", Lines: lines}, {Job: "b", Lines: lines},
	}}
	log := Vars(f)[VarFailingLog]
	if len(log) > maxLogBytes+len("⋯ truncated ⋯") || !strings.HasSuffix(log, "⋯ truncated ⋯") {
		t.Errorf("log is %d bytes, suffix %q", len(log), log[max(0, len(log)-20):])
	}
	if strings.Contains(log, "── b ──") && !strings.Contains(log, "── a ──") {
		t.Error("jobs out of order")
	}
}

func TestFixBranch(t *testing.T) {
	tests := []struct {
		workflow string
		id       int64
		want     string
	}{
		{"CI", 42, "fix/ci-42"},
		{"Build & Test (Linux)", 7, "fix/build-test-linux-7"},
		{"", 1, "fix/ci-1"},
		{"!!!", 1, "fix/ci-1"},
		{strings.Repeat("ab ", 20), 3, "fix/ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-3"},
	}
	for _, tt := range tests {
		if got := FixBranch(tt.workflow, tt.id); got != tt.want {
			t.Errorf("FixBranch(%q, %d) = %q, want %q", tt.workflow, tt.id, got, tt.want)
		}
	}
}

func TestRunOf(t *testing.T) {
	r := RunOf(github.Run{Repo: "a/b", ID: 1, RunNumber: 2, Attempt: 3, Workflow: "w", Branch: "br", HeadSHA: "s", URL: "u", CommitMessage: "m"})
	want := Run{Repo: "a/b", ID: 1, Number: 2, Attempt: 3, Workflow: "w", Branch: "br", HeadSHA: "s", URL: "u", CommitMsg: "m"}
	if r != want {
		t.Errorf("RunOf = %+v", r)
	}
	b, _ := json.Marshal(NewSummary(r, 9, "j", "ju", cilog.View{Tail: true}, time.Time{}))
	if !strings.Contains(string(b), `"tail":true`) || !strings.Contains(string(b), `"job_id":9`) {
		t.Errorf("summary json = %s", b)
	}
}
