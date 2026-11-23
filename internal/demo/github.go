package demo

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/daan3011/forge/internal/github"
)

// errDemo answers every write: the demo changes nothing.
var errDemo = errors.New("demo mode: nothing is changed")

// Fictional repos and people; nothing here refers to a real account.
const (
	repoAnvil   = "ember-labs/anvil"
	repoBellows = "ember-labs/bellows"
	viewer      = "you"
)

// gitHub is an in-memory github.PRClient, RunsClient and PullFinder.
type gitHub struct {
	now  time.Time
	prs  []github.PR
	runs []github.Run
	jobs map[int64][]github.Job
}

var (
	_ github.PRClient   = (*gitHub)(nil)
	_ github.RunsClient = (*gitHub)(nil)
	_ github.PullFinder = (*gitHub)(nil)
)

func newGitHub(now time.Time) *gitHub {
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	pr := func(repo string, n int, title, author, ci, review string, add, del int, updated time.Duration, mod func(*github.PR)) github.PR {
		p := github.PR{
			Repo: repo, Number: n, Title: title, Author: author, URL: github.PullURL(repo, n),
			State: github.StateOpen, ReviewDecision: review, CI: ci, Mergeable: github.Mergeable,
			Additions: add, Deletions: del, HeadRef: fmt.Sprintf("forge/%d", n), BaseRef: "main",
			CreatedAt: ago(updated + 26*time.Hour), UpdatedAt: ago(updated),
		}
		if mod != nil {
			mod(&p)
		}
		return p
	}
	g := &gitHub{now: now, jobs: map[int64][]github.Job{}}
	g.prs = []github.PR{
		pr(repoAnvil, 142, "Tempering: retry flaky quench step", viewer, github.CIFailure, github.ReviewRequired, 184, 31, 40*time.Minute,
			func(p *github.PR) { p.HeadRef = "quench-retry" }),
		pr(repoAnvil, 139, "Bellows API: stream heat readings", viewer, github.CISuccess, github.ReviewApproved, 412, 88, 3*time.Hour, nil),
		pr(repoBellows, 57, "Draft: airflow presets per alloy", viewer, github.CIPending, "", 96, 12, 25*time.Minute,
			func(p *github.PR) { p.Draft, p.State = true, github.StateDraft }),
		pr(repoAnvil, 145, "Fix crucible overflow on large batches", "smith", github.CISuccess, github.ReviewRequired, 57, 9, 90*time.Minute, nil),
		pr(repoBellows, 61, "Docs: forge-weld runbook", "ember-bot", github.CISuccess, github.ReviewRequired, 230, 4, 5*time.Hour, nil),
		pr(repoAnvil, 131, "Hammer cadence: configurable strikes", "smith", github.CIFailure, github.ReviewChangesRequested, 77, 40, 26*time.Hour,
			func(p *github.PR) { p.Mergeable = github.Conflicting }),
	}

	run := func(id int64, n int, repo, wf, branch, title, state string, started time.Duration, took time.Duration) github.Run {
		r := github.Run{
			Repo: repo, ID: id, RunNumber: n, Attempt: 1, Workflow: wf, Branch: branch,
			HeadSHA: fmt.Sprintf("%07x", id*7919), Title: title, Event: "pull_request", Actor: viewer,
			State: state, Status: "completed", Conclusion: "success",
			URL:       fmt.Sprintf("https://github.com/%s/actions/runs/%d", repo, id),
			CreatedAt: ago(started), StartedAt: ago(started), UpdatedAt: ago(started - took),
		}
		switch state {
		case github.CheckFail:
			r.Conclusion = "failure"
		case github.CheckRunning:
			r.Status, r.Conclusion, r.UpdatedAt = "in_progress", "", now
		}
		return r
	}
	g.runs = []github.Run{
		run(9101, 512, repoAnvil, "ci", "quench-retry", "Tempering: retry flaky quench step", github.CheckFail, 42*time.Minute, 6*time.Minute),
		run(9102, 511, repoAnvil, "ci", "main", "Merge #137: cooler cache", github.CheckPass, 2*time.Hour, 5*time.Minute),
		run(9103, 88, repoBellows, "ci", "forge/57", "Draft: airflow presets per alloy", github.CheckRunning, 4*time.Minute, 0),
		run(9104, 510, repoAnvil, "release", "main", "v0.9.2", github.CheckPass, 6*time.Hour, 9*time.Minute),
		run(9105, 87, repoBellows, "lint", "docs-weld", "Docs: forge-weld runbook", github.CheckPass, 5*time.Hour, time.Minute),
	}
	step := func(n int, name, state string) github.Step {
		return github.Step{Number: n, Name: name, State: state, StartedAt: ago(42 * time.Minute), CompletedAt: ago(37 * time.Minute)}
	}
	g.jobs[9101] = []github.Job{
		{
			ID: 77001, Name: "build", State: github.CheckPass, StartedAt: ago(42 * time.Minute), CompletedAt: ago(40 * time.Minute),
			Steps: []github.Step{step(1, "Set up job", github.CheckPass), step(2, "go build", github.CheckPass)},
		},
		{
			ID: 77002, Name: "test", State: github.CheckFail, StartedAt: ago(40 * time.Minute), CompletedAt: ago(36 * time.Minute),
			Steps: []github.Step{step(1, "Set up job", github.CheckPass), step(2, "go test ./...", github.CheckFail), step(3, "Upload coverage", github.CheckSkipped)},
		},
	}
	g.jobs[9102] = []github.Job{{ID: 77003, Name: "test", State: github.CheckPass, Steps: []github.Step{step(1, "go test ./...", github.CheckPass)}}}
	g.jobs[9103] = []github.Job{{ID: 77004, Name: "test", State: github.CheckRunning, Steps: []github.Step{step(1, "go test ./...", github.CheckRunning)}}}
	return g
}

// SearchPRs answers the default PR sections by their qualifier.
func (g *gitHub) SearchPRs(_ context.Context, q string, _ int) (github.SearchResult, error) {
	var out []github.PR
	for _, p := range g.prs {
		mine := p.Author == viewer
		switch {
		case strings.Contains(q, "author:@me") && !mine,
			strings.Contains(q, "review-requested:@me") && (mine || p.ReviewDecision != github.ReviewRequired):
			continue
		}
		if i := strings.Index(q, "repo:"); i >= 0 && strings.Fields(q[i+len("repo:"):])[0] != p.Repo {
			continue
		}
		out = append(out, p)
	}
	if out == nil {
		out = []github.PR{}
	}
	return github.SearchResult{PRs: out, Total: len(out), Viewer: viewer}, nil
}

func (g *gitHub) find(repo string, n int) (github.PR, bool) {
	i := slices.IndexFunc(g.prs, func(p github.PR) bool { return p.Repo == repo && p.Number == n })
	if i < 0 {
		return github.PR{}, false
	}
	return g.prs[i], true
}

// PRDetail is the PR's body, checks and a little review activity.
func (g *gitHub) PRDetail(_ context.Context, repo string, n int) (github.PRDetail, error) {
	p, ok := g.find(repo, n)
	if !ok {
		return github.PRDetail{}, fmt.Errorf("%s#%d: not found", repo, n)
	}
	check := github.CheckPass
	switch p.CI {
	case github.CIFailure:
		check = github.CheckFail
	case github.CIPending:
		check = github.CheckRunning
	}
	d := github.PRDetail{
		UpdatedAt:      p.UpdatedAt,
		Body:           "## What\n\n" + p.Title + ".\n\n## Why\n\nThe quench step times out on cold mornings; retry it twice with backoff before failing the batch.\n\n- [x] unit tests\n- [ ] runbook updated\n",
		Labels:         []github.Label{{Name: "forge", Color: "ff7a1a"}, {Name: "reliability", Color: "7fb069"}},
		ReviewDecision: p.ReviewDecision, Mergeable: p.Mergeable, MergeState: github.MergeStateBlocked,
		Reviewers: []github.Reviewer{{Login: "smith", State: github.ReviewStatePending}},
		Checks: []github.Check{
			{Name: "build", Workflow: "ci", State: github.CheckPass, StartedAt: p.UpdatedAt, CompletedAt: p.UpdatedAt.Add(2 * time.Minute), RunID: 9101, JobID: 77001},
			{Name: "test", Workflow: "ci", State: check, StartedAt: p.UpdatedAt, CompletedAt: p.UpdatedAt.Add(4 * time.Minute), RunID: 9101, JobID: 77002},
			{Name: "lint", Workflow: "ci", State: github.CheckPass, StartedAt: p.UpdatedAt, CompletedAt: p.UpdatedAt.Add(time.Minute)},
		},
		Activity: []github.Event{
			{Kind: github.EventComment, Author: "ember-bot", Body: "Coverage 81.4% (+0.6%)", At: p.UpdatedAt.Add(-20 * time.Minute)},
			{Kind: github.EventReview, Author: "smith", State: github.ReviewStateCommented, Body: "Can the backoff be configurable?", At: p.UpdatedAt.Add(-10 * time.Minute)},
		},
		MergeMethods: []string{github.MergeMethodSquash, github.MergeMethodMerge},
		HeadSHA:      "3f9c2e1", HeadMessage: p.Title,
	}
	return d, nil
}

// PR returns the row as listed.
func (g *gitHub) PR(_ context.Context, repo string, n int) (github.PR, error) {
	if p, ok := g.find(repo, n); ok {
		return p, nil
	}
	return github.PR{}, fmt.Errorf("%s#%d: not found", repo, n)
}

func (g *gitHub) UpdateBranch(context.Context, string, int) error            { return errDemo }
func (g *gitHub) Merge(context.Context, string, int, string) error           { return errDemo }
func (g *gitHub) EnableAutoMerge(context.Context, string, int, string) error { return errDemo }
func (g *gitHub) SetDraft(context.Context, string, int, bool) error          { return errDemo }
func (g *gitHub) SubmitReview(context.Context, string, int, string, string) error {
	return errDemo
}

// Diff is the same small change for every PR.
func (g *gitHub) Diff(context.Context, string, int) (string, error) { return sampleDiff, nil }

// ListRuns returns repo's runs, newest first.
func (g *gitHub) ListRuns(_ context.Context, repo string, f github.RunFilter) ([]github.Run, error) {
	var out []github.Run
	for _, r := range g.runs {
		if r.Repo != repo || (f.Branch != "" && r.Branch != f.Branch) || (f.Actor != "" && r.Actor != f.Actor) {
			continue
		}
		out = append(out, r)
	}
	if out == nil {
		out = []github.Run{}
	}
	return out, nil
}

func (g *gitHub) RunJobs(_ context.Context, _ string, id int64) ([]github.Job, error) {
	if js, ok := g.jobs[id]; ok {
		return js, nil
	}
	return []github.Job{}, nil
}

func (g *gitHub) Viewer(context.Context) (string, error) { return viewer, nil }

// JobLogs is a plausible failing Go test log for the failed job.
func (g *gitHub) JobLogs(_ context.Context, _ string, id int64) ([]byte, error) {
	if id != 77002 {
		return []byte("2026-11-24T09:18:02.1Z ok  \tember-labs/anvil/...\t1.2s\n"), nil
	}
	return []byte(failingLog), nil
}

func (g *gitHub) RerunFailedJobs(context.Context, string, int64) error { return errDemo }
func (g *gitHub) RerunRun(context.Context, string, int64) error        { return errDemo }
func (g *gitHub) CancelRun(context.Context, string, int64) error       { return errDemo }

// PullsForBranch finds the PR of a run's branch (for "fix this").
func (g *gitHub) PullsForBranch(_ context.Context, repo, branch string) ([]github.Pull, error) {
	var out []github.Pull
	for _, p := range g.prs {
		if p.Repo == repo && p.HeadRef == branch {
			out = append(out, github.Pull{Repo: p.Repo, Number: p.Number, Title: p.Title, State: p.State, URL: p.URL, HeadRef: p.HeadRef, UpdatedAt: p.UpdatedAt})
		}
	}
	return out, nil
}

const sampleDiff = `diff --git a/temper/quench.go b/temper/quench.go
index 4b1d2c0..9e8f7a1 100644
--- a/temper/quench.go
+++ b/temper/quench.go
@@ -12,14 +12,27 @@ import (
 // Quench cools the blade in oil and reports the final hardness.
-func Quench(ctx context.Context, b *Blade) (Hardness, error) {
-	return b.Dip(ctx, Oil)
+func Quench(ctx context.Context, b *Blade, opts ...Option) (Hardness, error) {
+	cfg := defaults(opts)
+	var err error
+	for attempt := range cfg.Retries + 1 {
+		var h Hardness
+		if h, err = b.Dip(ctx, Oil); err == nil {
+			return h, nil
+		}
+		if !errors.Is(err, ErrTooCold) {
+			return 0, err
+		}
+		select {
+		case <-ctx.Done():
+			return 0, ctx.Err()
+		case <-time.After(cfg.Backoff << attempt):
+		}
+	}
+	return 0, fmt.Errorf("quench: %w", err)
 }
diff --git a/temper/quench_test.go b/temper/quench_test.go
index 0c1e2d3..5a6b7c8 100644
--- a/temper/quench_test.go
+++ b/temper/quench_test.go
@@ -40,6 +40,18 @@ func TestQuench(t *testing.T) {
+func TestQuenchRetriesWhenTooCold(t *testing.T) {
+	b := &Blade{fails: 2}
+	h, err := Quench(context.Background(), b, WithRetries(2), WithBackoff(0))
+	if err != nil || h < 58 {
+		t.Fatalf("got %v, %v", h, err)
+	}
+}
`

const failingLog = `2026-11-24T09:17:41.0Z ##[group]Run go test ./...
2026-11-24T09:17:41.1Z go test ./...
2026-11-24T09:17:58.4Z ok  	ember-labs/anvil/bellows	0.84s
2026-11-24T09:17:59.0Z ok  	ember-labs/anvil/crucible	1.02s
2026-11-24T09:18:01.7Z --- FAIL: TestQuenchRetriesWhenTooCold (0.31s)
2026-11-24T09:18:01.7Z     quench_test.go:45: got 54, quench: blade too cold: 3 attempts
2026-11-24T09:18:01.8Z FAIL
2026-11-24T09:18:01.8Z FAIL	ember-labs/anvil/temper	0.47s
2026-11-24T09:18:02.0Z ok  	ember-labs/anvil/hammer	0.66s
2026-11-24T09:18:02.1Z FAIL
2026-11-24T09:18:02.1Z ##[error]Process completed with exit code 1.
`
