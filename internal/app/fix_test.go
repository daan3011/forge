package app

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/github"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/sessions/tmux/tmuxtest"
	"github.com/daan3011/forge/internal/sessions/worktree"
	"github.com/daan3011/forge/internal/store"
)

// redRuns serves one failed run on the PR branch "retry", with a failing
// job whose log has a test failure.
type redRuns struct {
	mu   sync.Mutex
	logs []int64
}

func (f *redRuns) ListRuns(_ context.Context, repo string, _ github.RunFilter) ([]github.Run, error) {
	start := time.Now().Add(-10 * time.Minute)
	return []github.Run{{
		Repo: repo, ID: 9001, RunNumber: 77, Attempt: 1, Workflow: "ci", Branch: "retry",
		HeadSHA: "abc1234def", Title: "Add retry to webhook sender",
		CommitMessage: "Add retry to webhook sender\n\nRetries 5xx with backoff.",
		Event:         "pull_request", Actor: "daan", State: github.CheckFail, Status: "completed", Conclusion: "failure",
		URL:       "https://github.com/" + repo + "/actions/runs/9001",
		CreatedAt: start, StartedAt: start, UpdatedAt: start.Add(2 * time.Minute),
	}}, nil
}

func (f *redRuns) RunJobs(context.Context, string, int64) ([]github.Job, error) {
	return []github.Job{
		{ID: 501, Name: "test", State: github.CheckFail, URL: "https://github.com/daan/forge/actions/runs/9001/job/501"},
		{ID: 502, Name: "lint", State: github.CheckPass},
	}, nil
}

func (f *redRuns) Viewer(context.Context) (string, error) { return "daan", nil }

func (f *redRuns) JobLogs(_ context.Context, _ string, id int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logs = append(f.logs, id)
	return []byte("2026-11-21T09:00:00.0Z === RUN   TestRetry\n" +
		"2026-11-21T09:00:01.0Z     sender_test.go:42: got 1 attempt, want 3\n" +
		"2026-11-21T09:00:01.0Z --- FAIL: TestRetry (0.01s)\n" +
		"2026-11-21T09:00:02.0Z ##[error]Process completed with exit code 1.\n"), nil
}

func (f *redRuns) RerunFailedJobs(context.Context, string, int64) error { return nil }
func (f *redRuns) RerunRun(context.Context, string, int64) error        { return nil }
func (f *redRuns) CancelRun(context.Context, string, int64) error       { return nil }

type prFinder struct{}

func (prFinder) PullsForBranch(_ context.Context, repo, branch string) ([]github.Pull, error) {
	if branch != "retry" {
		return nil, nil
	}
	return []github.Pull{{
		Repo: repo, Number: 12, Title: "Add retry to webhook sender", State: github.StateOpen,
		URL: github.PullURL(repo, 12), HeadRef: "retry",
	}}, nil
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestFixThisFromRedCheck is P2.8's done-when: one key on a red run
// starts an agent on the PR's branch with the failure in its first prompt,
// linked to the PR; the next press goes into that same session.
func TestFixThisFromRedCheck(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	ctx := context.Background()
	dir := t.TempDir()

	// origin has main and the PR branch "retry"; the checkout has main.
	origin, checkout := filepath.Join(dir, "origin.git"), filepath.Join(dir, "checkout")
	git(t, dir, "init", "-q", "--bare", "-b", "main", origin)
	git(t, dir, "clone", "-q", origin, checkout)
	git(t, checkout, "commit", "-q", "--allow-empty", "-m", "init")
	git(t, checkout, "push", "-q", "origin", "HEAD:main")
	git(t, checkout, "push", "-q", "origin", "HEAD:retry")

	st, err := store.Open(ctx, filepath.Join(dir, "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := config.Default()
	cfg.Projects = []config.Project{{Name: "forge", Repo: "daan/forge", Path: checkout}}
	fakeTmux := tmuxtest.NewFake()
	data := filepath.Join(dir, "data")
	svc := agent.New(agent.Config{
		Tmux: fakeTmux, Worktrees: worktree.New(filepath.Join(dir, "wt"), worktree.NewExec()),
		Store: agent.StoreFrom(st), Projects: cfg.Projects, DataDir: data, ClaudeBin: "claude",
		ProjectsDir: filepath.Join(dir, "claude"), StartupCheck: time.Nanosecond,
	})
	runs := &redRuns{}
	m := mustNew(t, cfg, WithRuns(runs), WithPulls(prFinder{}), WithSessions(svc),
		WithStore(st.Cache()), WithTemplates(filepath.Join(dir, "templates"), nil))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(160, 30))

	var seen []byte
	wait := func(s string) {
		t.Helper()
		teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
			if !bytes.Contains(append(slices.Clip(seen), b...), []byte(s)) {
				return false
			}
			seen = append(seen, b...)
			return true
		}, teatest.WithDuration(10*time.Second))
	}
	wait("forge")
	tm.Send(press("3")) // Pipelines
	wait("Add retry to webhook sender")
	tm.Send(press("F"))
	wait("fix session pr-12 started")

	prompt, err := os.ReadFile(filepath.Join(data, "sessions", "pr-12", "prompt.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"CI is failing for daan/forge on branch retry",
		"Workflow: ci", "Job: test", "actions/runs/9001",
		"Last commit: Add retry to webhook sender", "Retries 5xx with backoff.",
		"--- FAIL: TestRetry", "sender_test.go:42: got 1 attempt, want 3",
	} {
		if !strings.Contains(string(prompt), want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
	x, err := st.Sessions().GetByName(ctx, "pr-12")
	if err != nil {
		t.Fatal(err)
	}
	if x.Branch != "retry" || x.Template != "fix-ci" {
		t.Errorf("session = %+v", x)
	}
	links, err := st.Links().For(ctx, store.SessionRef(x.ID))
	if err != nil || len(links) != 1 || links[0].Other != store.PRRef("daan/forge", 12) {
		t.Errorf("links = %+v, %v", links, err)
	}

	// Again: the live linked session gets the prompt; nothing new starts.
	tm.Send(press("3"))
	wait("Add retry to webhook sender")
	tm.Send(press("F"))
	wait("sent the failure to pr-12")
	pasted := fakeTmux.Pasted(tmux.Target{Session: x.TmuxSession, Window: x.TmuxWindow})
	if len(pasted) != 1 || !strings.Contains(pasted[0], "--- FAIL: TestRetry") {
		t.Errorf("pasted = %q", pasted)
	}
	tm.Send(press("q"))
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	all, err := st.Sessions().List(ctx, store.SessionFilter{})
	if err != nil || len(all) != 1 {
		t.Errorf("sessions = %d, %v", len(all), err)
	}
	// The second F used the summary saved by the first: one download.
	runs.mu.Lock()
	defer runs.mu.Unlock()
	if !slices.Equal(runs.logs, []int64{501}) {
		t.Errorf("log downloads = %v", runs.logs)
	}
}
