package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

// Run is a GitHub Actions workflow run (P2.6).
type Run struct {
	Repo      string `json:"repo"` // owner/name
	ID        int64  `json:"id"`
	RunNumber int    `json:"run_number"`
	Attempt   int    `json:"attempt"`
	Workflow  string `json:"workflow"` // the workflow's name
	Branch    string `json:"branch"`
	HeadSHA   string `json:"head_sha"`
	// Title is the run's display title (usually the head commit's first
	// line, or the PR title).
	Title string `json:"title"`
	Event string `json:"event"` // push, pull_request, schedule, …
	Actor string `json:"actor"`
	// State is a Check* constant, so runs share the checks' icons.
	State      string    `json:"state"`
	Status     string    `json:"status"`     // REST status: queued, in_progress, completed, …
	Conclusion string    `json:"conclusion"` // REST conclusion once completed
	URL        string    `json:"url"`
	CreatedAt  time.Time `json:"created_at"`
	StartedAt  time.Time `json:"started_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Key is the run's id across repos: "owner/repo#123456".
func (r Run) Key() string { return fmt.Sprintf("%s#%d", r.Repo, r.ID) }

// Active reports whether the run is queued or in progress.
func (r Run) Active() bool { return r.State == CheckRunning || r.State == CheckPending }

// Duration is how long the run took: from its start until its last update
// once completed, until now while it runs, zero while queued.
func (r Run) Duration(now time.Time) time.Duration {
	start := r.StartedAt
	if start.IsZero() {
		start = r.CreatedAt
	}
	switch {
	case start.IsZero() || r.State == CheckPending:
		return 0
	case r.State == CheckRunning:
		return max(now.Sub(start), 0)
	}
	return max(r.UpdatedAt.Sub(start), 0)
}

// Job is one job of a run, with its steps.
type Job struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	State       string    `json:"state"` // a Check* constant
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	URL         string    `json:"url"`
	Steps       []Step    `json:"steps"`
}

// Duration is how long the job ran (until now while it runs).
func (j Job) Duration(now time.Time) time.Duration {
	return span(j.State, j.StartedAt, j.CompletedAt, now)
}

// Step is one step of a job.
type Step struct {
	Number      int       `json:"number"`
	Name        string    `json:"name"`
	State       string    `json:"state"` // a Check* constant
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
}

// Duration is how long the step ran (until now while it runs).
func (s Step) Duration(now time.Time) time.Duration {
	return span(s.State, s.StartedAt, s.CompletedAt, now)
}

func span(state string, start, end, now time.Time) time.Duration {
	if start.IsZero() {
		return 0
	}
	if end.IsZero() {
		if state != CheckRunning {
			return 0
		}
		end = now
	}
	return max(end.Sub(start), 0)
}

// RunFilter narrows a run listing on GitHub's side. Empty fields don't
// filter.
type RunFilter struct {
	Branch string
	// Status is a REST status or conclusion: queued, in_progress,
	// completed, success, failure, cancelled, …
	Status string
	Actor  string // login
}

// RunsClient is what the Pipelines tab needs from GitHub.
type RunsClient interface {
	// ListRuns returns repo's latest workflow runs, newest first.
	ListRuns(ctx context.Context, repo string, f RunFilter) ([]Run, error)
	// RunJobs returns the jobs (with steps) of the run's latest attempt.
	RunJobs(ctx context.Context, repo string, runID int64) ([]Job, error)
	// Viewer is the login gh is authenticated as.
	Viewer(ctx context.Context) (string, error)
	// JobLogs downloads a job's plain-text log (P2.7).
	JobLogs(ctx context.Context, repo string, jobID int64) ([]byte, error)
	// RerunFailedJobs reruns the run's failed jobs (and what they need).
	RerunFailedJobs(ctx context.Context, repo string, runID int64) error
	// RerunRun reruns every job of the run.
	RerunRun(ctx context.Context, repo string, runID int64) error
	// CancelRun cancels a queued or running run.
	CancelRun(ctx context.Context, repo string, runID int64) error
}

// MaxLogBytes is the largest job log JobLogs downloads.
const MaxLogBytes = 32 << 20

// ErrLogTooLarge means a job log is over MaxLogBytes.
var ErrLogTooLarge = errors.New("log is too large to show here; open the job in the browser")

// ErrLogsUnavailable means GitHub has no log for the job: it expired, or
// the job hasn't finished.
var ErrLogsUnavailable = errors.New("logs not available (expired, or the job hasn't finished)")

var _ RunsClient = (*Client)(nil)

// RunsLimit is how many runs per repo are listed.
const RunsLimit = 30

// maxJobs is how many jobs per run are fetched.
const maxJobs = 100

// ListRuns implements RunsClient.
func (c *Client) ListRuns(ctx context.Context, repo string, f RunFilter) ([]Run, error) {
	if !strings.Contains(repo, "/") {
		return nil, fmt.Errorf("github: runs of %q: need owner/repo", repo)
	}
	q := url.Values{"per_page": {fmt.Sprint(RunsLimit)}}
	for k, v := range map[string]string{"branch": f.Branch, "status": f.Status, "actor": f.Actor} {
		if v != "" {
			q.Set(k, v)
		}
	}
	var runs []Run
	err := c.restGet(ctx, "repos/"+repo+"/actions/runs?"+q.Encode(), func(r io.Reader) (err error) {
		runs, err = ParseRuns(r, repo)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("github: runs of %s: %w", repo, err)
	}
	return runs, nil
}

// RunJobs implements RunsClient.
func (c *Client) RunJobs(ctx context.Context, repo string, runID int64) ([]Job, error) {
	path := fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=%d", repo, runID, maxJobs)
	var jobs []Job
	err := c.restGet(ctx, path, func(r io.Reader) (err error) {
		jobs, err = ParseJobs(r)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("github: jobs of run %d: %w", runID, err)
	}
	return jobs, nil
}

// JobLogs implements RunsClient. GitHub redirects to a signed download URL,
// which the HTTP client follows.
func (c *Client) JobLogs(ctx context.Context, repo string, jobID int64) ([]byte, error) {
	if !strings.Contains(repo, "/") {
		return nil, fmt.Errorf("github: logs in %q: need owner/repo", repo)
	}
	var out []byte
	err := c.restGet(ctx, fmt.Sprintf("repos/%s/actions/jobs/%d/logs", repo, jobID), func(r io.Reader) error {
		b, err := io.ReadAll(io.LimitReader(r, MaxLogBytes+1))
		if err != nil {
			return err
		}
		if len(b) > MaxLogBytes {
			return ErrLogTooLarge
		}
		out = b
		return nil
	})
	var he *api.HTTPError
	switch {
	case errors.As(err, &he) && (he.StatusCode == http.StatusNotFound || he.StatusCode == http.StatusGone):
		return nil, fmt.Errorf("github: job %d: %w", jobID, ErrLogsUnavailable)
	case err != nil:
		return nil, fmt.Errorf("github: logs of job %d: %w", jobID, err)
	}
	return out, nil
}

// RerunFailedJobs implements RunsClient.
func (c *Client) RerunFailedJobs(ctx context.Context, repo string, runID int64) error {
	return c.runPost(ctx, repo, runID, "rerun-failed-jobs")
}

// RerunRun implements RunsClient.
func (c *Client) RerunRun(ctx context.Context, repo string, runID int64) error {
	return c.runPost(ctx, repo, runID, "rerun")
}

// CancelRun implements RunsClient.
func (c *Client) CancelRun(ctx context.Context, repo string, runID int64) error {
	return c.runPost(ctx, repo, runID, "cancel")
}

// runPost POSTs to repos/<repo>/actions/runs/<id>/<action> with no body.
func (c *Client) runPost(ctx context.Context, repo string, runID int64, action string) error {
	if !strings.Contains(repo, "/") || runID <= 0 {
		return fmt.Errorf("github: want owner/repo and a run id, got %q %d", repo, runID)
	}
	rc, err := c.rest()
	if err != nil {
		return fmt.Errorf("github: %w", err)
	}
	path := fmt.Sprintf("repos/%s/actions/runs/%d/%s", repo, runID, action)
	if err := rc.DoWithContext(ctx, http.MethodPost, path, nil, nil); err != nil {
		return fmt.Errorf("github: %s run %d: %w", action, runID, err)
	}
	return nil
}

// viewerCache remembers the login once it is known.
type viewerCache struct {
	mu    sync.Mutex
	login string
}

// Viewer implements RunsClient. The login is fetched once.
func (c *Client) Viewer(ctx context.Context) (string, error) {
	c.viewer.mu.Lock()
	defer c.viewer.mu.Unlock()
	if c.viewer.login != "" {
		return c.viewer.login, nil
	}
	var u struct {
		Login string `json:"login"`
	}
	err := c.restGet(ctx, "user", func(r io.Reader) error { return json.NewDecoder(r).Decode(&u) })
	if err != nil {
		return "", fmt.Errorf("github: viewer: %w", err)
	}
	if u.Login == "" {
		return "", fmt.Errorf("github: viewer: no login in response")
	}
	c.viewer.login = u.Login
	return u.Login, nil
}

// restGet GETs path through the REST client and hands the body to read.
func (c *Client) restGet(ctx context.Context, path string, read func(io.Reader) error) error {
	rc, err := c.rest()
	if err != nil {
		return err
	}
	resp, err := rc.RequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return read(resp.Body)
}

type restRun struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	DisplayTitle string    `json:"display_title"`
	HeadBranch   string    `json:"head_branch"`
	HeadSHA      string    `json:"head_sha"`
	Event        string    `json:"event"`
	Status       string    `json:"status"`
	Conclusion   string    `json:"conclusion"`
	RunNumber    int       `json:"run_number"`
	RunAttempt   int       `json:"run_attempt"`
	HTMLURL      string    `json:"html_url"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	RunStartedAt time.Time `json:"run_started_at"`
	Actor        *struct {
		Login string `json:"login"`
	} `json:"actor"`
	TriggeringActor *struct {
		Login string `json:"login"`
	} `json:"triggering_actor"`
	HeadCommit *struct {
		Message string `json:"message"`
	} `json:"head_commit"`
}

// ParseRuns reads a REST list-workflow-runs response.
func ParseRuns(r io.Reader, repo string) ([]Run, error) {
	var raw struct {
		Runs []restRun `json:"workflow_runs"`
	}
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]Run, 0, len(raw.Runs))
	for _, w := range raw.Runs {
		title := w.DisplayTitle
		if title == "" && w.HeadCommit != nil {
			title = w.HeadCommit.Message
		}
		title, _, _ = strings.Cut(strings.TrimSpace(title), "\n")
		actor := ""
		switch {
		case w.Actor != nil:
			actor = w.Actor.Login
		case w.TriggeringActor != nil:
			actor = w.TriggeringActor.Login
		}
		out = append(out, Run{
			Repo: repo, ID: w.ID, RunNumber: w.RunNumber, Attempt: w.RunAttempt,
			Workflow: w.Name, Branch: w.HeadBranch, HeadSHA: w.HeadSHA,
			Title: strings.TrimSpace(title), Event: w.Event, Actor: actor,
			State:  restState(w.Status, w.Conclusion),
			Status: w.Status, Conclusion: w.Conclusion, URL: w.HTMLURL,
			CreatedAt: w.CreatedAt, StartedAt: w.RunStartedAt, UpdatedAt: w.UpdatedAt,
		})
	}
	return out, nil
}

type restStep struct {
	Number      int        `json:"number"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	Conclusion  string     `json:"conclusion"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
}

type restJob struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	Conclusion  string     `json:"conclusion"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
	HTMLURL     string     `json:"html_url"`
	Steps       []restStep `json:"steps"`
}

// ParseJobs reads a REST list-jobs-for-a-run response.
func ParseJobs(r io.Reader) ([]Job, error) {
	var raw struct {
		Jobs []restJob `json:"jobs"`
	}
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(raw.Jobs))
	for _, j := range raw.Jobs {
		job := Job{
			ID: j.ID, Name: j.Name, State: restState(j.Status, j.Conclusion),
			StartedAt: deref(j.StartedAt), CompletedAt: deref(j.CompletedAt), URL: j.HTMLURL,
		}
		for _, s := range j.Steps {
			job.Steps = append(job.Steps, Step{
				Number: s.Number, Name: s.Name, State: restState(s.Status, s.Conclusion),
				StartedAt: deref(s.StartedAt), CompletedAt: deref(s.CompletedAt),
			})
		}
		out = append(out, job)
	}
	return out, nil
}

// restState maps a REST status and conclusion (lower case) to a Check*
// state, the same way as GraphQL check runs.
func restState(status, conclusion string) string {
	return checkRunState(strings.ToUpper(status), strings.ToUpper(conclusion))
}
