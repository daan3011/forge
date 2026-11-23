// Package cifix gathers the failure context of red CI jobs for the "fix
// this" flow (P2.8): the error summaries P2.7 saves in kv_cache (fetching
// and saving the logs that aren't there yet), turned into fix-ci template
// variables.
package cifix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/daan3011/forge/internal/cilog"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/store"
)

// MaxJobs is how many failed jobs go into one prompt.
const MaxJobs = 3

// maxLogBytes caps failing_log across all jobs.
const maxLogBytes = 16 << 10

// Template is the session template the flow uses.
const Template = "fix-ci"

// Template variables filled by Vars.
const (
	VarWorkflow      = "ci_workflow"
	VarJob           = "ci_job"
	VarRun           = "ci_run"
	VarCommitMessage = "commit_message"
	VarFailingLog    = "failing_log"
)

// KV is the kv_cache the summaries live in (tabs.KVStore).
type KV interface {
	Get(ctx context.Context, key string) (store.CacheEntry, error)
	Put(ctx context.Context, key, kind, project string, value []byte, ttl time.Duration) error
}

// Logs downloads job logs (github.RunsClient).
type Logs interface {
	JobLogs(ctx context.Context, repo string, jobID int64) ([]byte, error)
}

// Run identifies the workflow run a failed job belongs to.
type Run struct {
	Repo      string // owner/name
	ID        int64
	Number    int
	Attempt   int
	Workflow  string
	Branch    string
	HeadSHA   string
	URL       string
	CommitMsg string
}

// RunOf is the Run of a listed workflow run.
func RunOf(r github.Run) Run {
	return Run{
		Repo: r.Repo, ID: r.ID, Number: r.RunNumber, Attempt: r.Attempt, Workflow: r.Workflow,
		Branch: r.Branch, HeadSHA: r.HeadSHA, URL: r.URL, CommitMsg: r.CommitMessage,
	}
}

// RunURL is the web page of repo's run id.
func RunURL(repo string, id int64) string {
	return fmt.Sprintf("https://github.com/%s/actions/runs/%d", repo, id)
}

// Target is one failed job to summarise. A Target with no job id (a commit
// status from another CI) is reported by name and URL only.
type Target struct {
	Run   Run
	JobID int64
	Job   string
	URL   string
}

// JobFailure is one failed job's context.
type JobFailure struct {
	Workflow string
	Job      string
	URL      string
	RunURL   string
	Lines    []string
	Tail     bool   // no error lines matched; Lines is the log's tail
	NoLog    string // why there are no lines, when there aren't
}

// Failure is what a fix prompt is built from.
type Failure struct {
	Repo          string
	Branch        string
	HeadSHA       string
	CommitMessage string
	Jobs          []JobFailure
}

// NewSummary builds the kv_cache summary of a job's collapsed log.
func NewSummary(r Run, jobID int64, job, url string, v cilog.View, now time.Time) cilog.Summary {
	return cilog.Summary{
		Repo: r.Repo, RunID: r.ID, RunNumber: r.Number, Attempt: r.Attempt,
		Workflow: r.Workflow, Branch: r.Branch, HeadSHA: r.HeadSHA,
		JobID: jobID, Job: job, URL: url,
		Lines: cilog.SummaryLines(v), Tail: v.Tail, ExtractedAt: now,
	}
}

// Save stores s in kv (no TTL).
func Save(ctx context.Context, kv KV, project string, s cilog.Summary) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return kv.Put(ctx, cilog.SummaryKey(s.Repo, s.RunID, s.JobID), cilog.Kind, project, b, 0)
}

// Load reads a saved summary; ok is false when there is none (or kv is nil).
func Load(ctx context.Context, kv KV, repo string, runID, jobID int64) (cilog.Summary, bool) {
	if kv == nil {
		return cilog.Summary{}, false
	}
	e, err := kv.Get(ctx, cilog.SummaryKey(repo, runID, jobID))
	if err != nil {
		return cilog.Summary{}, false
	}
	var s cilog.Summary
	if json.Unmarshal(e.Value, &s) != nil {
		return cilog.Summary{}, false
	}
	return s, true
}

// Summarize collects the failure context of up to MaxJobs targets: saved
// summaries first, else the job's log is downloaded, collapsed and saved
// (when kv is set). A job whose log can't be had says why in NoLog; that
// never fails the whole.
func Summarize(ctx context.Context, logs Logs, kv KV, project string, now time.Time, targets []Target) Failure {
	var f Failure
	if len(targets) > MaxJobs {
		targets = targets[:MaxJobs]
	}
	for _, t := range targets {
		if f.Repo == "" {
			f.Repo, f.Branch, f.HeadSHA, f.CommitMessage = t.Run.Repo, t.Run.Branch, t.Run.HeadSHA, t.Run.CommitMsg
		}
		runURL := t.Run.URL
		if runURL == "" && t.Run.ID > 0 {
			runURL = RunURL(t.Run.Repo, t.Run.ID)
		}
		jf := JobFailure{Workflow: t.Run.Workflow, Job: t.Job, URL: t.URL, RunURL: runURL}
		switch {
		case t.JobID <= 0:
			jf.NoLog = "not a GitHub Actions job; see " + t.URL
		default:
			if s, ok := Load(ctx, kv, t.Run.Repo, t.Run.ID, t.JobID); ok {
				jf.Lines, jf.Tail = s.Lines, s.Tail
				break
			}
			if logs == nil {
				jf.NoLog = "log not available"
				break
			}
			raw, err := logs.JobLogs(ctx, t.Run.Repo, t.JobID)
			if err != nil {
				jf.NoLog = logError(err)
				break
			}
			lines := cilog.Clean(raw)
			v := cilog.Collapse(lines, cilog.Extract(lines, cilog.Context))
			s := NewSummary(t.Run, t.JobID, t.Job, t.URL, v, now)
			jf.Lines, jf.Tail = s.Lines, s.Tail
			if kv != nil {
				_ = Save(ctx, kv, project, s) // a cache miss next time is fine
			}
		}
		f.Jobs = append(f.Jobs, jf)
	}
	return f
}

func logError(err error) string {
	switch {
	case errors.Is(err, github.ErrLogsUnavailable):
		return "log unavailable (expired, or the job hasn't finished)"
	case errors.Is(err, github.ErrLogTooLarge):
		return "log too large to download"
	}
	return "couldn't download the log: " + err.Error()
}

// Vars are the fix-ci template variables for f.
func Vars(f Failure) map[string]string {
	var workflows, jobs, runs []string
	var log strings.Builder
	truncated := false
	for i, j := range f.Jobs {
		workflows = appendUnique(workflows, j.Workflow)
		jobs = appendUnique(jobs, j.Job)
		runs = appendUnique(runs, j.RunURL)
		if truncated {
			continue
		}
		var b strings.Builder
		if i > 0 {
			b.WriteString("\n")
		}
		title := j.Job
		if j.Workflow != "" {
			title = j.Workflow + " › " + j.Job
		}
		fmt.Fprintf(&b, "── %s ──\n", title)
		switch {
		case j.NoLog != "":
			fmt.Fprintf(&b, "(%s)\n", j.NoLog)
		default:
			if j.Tail {
				b.WriteString("(no error lines matched; last lines of the log)\n")
			}
			for _, l := range j.Lines {
				b.WriteString(l)
				b.WriteString("\n")
			}
		}
		if log.Len()+b.Len() > maxLogBytes {
			rest := maxLogBytes - log.Len()
			if rest > 0 {
				chunk := b.String()[:rest]
				if k := strings.LastIndexByte(chunk, '\n'); k >= 0 {
					log.WriteString(chunk[:k+1])
				}
			}
			log.WriteString("⋯ truncated ⋯\n")
			truncated = true
			continue
		}
		log.WriteString(b.String())
	}
	return map[string]string{
		VarWorkflow:      strings.Join(workflows, ", "),
		VarJob:           strings.Join(jobs, ", "),
		VarRun:           strings.Join(runs, ", "),
		VarCommitMessage: f.CommitMessage,
		VarFailingLog:    strings.TrimRight(log.String(), "\n"),
	}
}

func appendUnique(xs []string, x string) []string {
	if x == "" {
		return xs
	}
	for _, y := range xs {
		if y == x {
			return xs
		}
	}
	return append(xs, x)
}

var slugJunk = regexp.MustCompile(`[^a-z0-9]+`)

// maxSlug caps the workflow part of a fix branch.
const maxSlug = 30

// FixBranch is the branch a fix for a run without a PR starts on:
// fix/<workflow-slug>-<run id>.
func FixBranch(workflow string, runID int64) string {
	slug := strings.Trim(slugJunk.ReplaceAllString(strings.ToLower(workflow), "-"), "-")
	if len(slug) > maxSlug {
		slug = strings.TrimRight(slug[:maxSlug], "-")
	}
	if slug == "" {
		slug = "ci"
	}
	return "fix/" + slug + "-" + strconv.FormatInt(runID, 10)
}

// PRTargets are a PR's failing checks as failure targets, up to MaxJobs:
// Actions jobs with their run and job ids, other checks (commit statuses,
// third-party apps) by name and URL. branch is the PR's head branch.
func PRTargets(repo, branch string, d github.PRDetail) []Target {
	var out []Target
	for _, c := range d.Checks {
		if c.State != github.CheckFail {
			continue
		}
		run := Run{
			Repo: repo, Workflow: c.Workflow, Branch: branch,
			HeadSHA: d.HeadSHA, CommitMsg: d.HeadMessage,
		}
		tg := Target{Run: run, Job: c.Name, URL: c.URL}
		if runID, jobID, ok := c.CheckIDs(); ok {
			tg.Run.ID, tg.JobID = runID, jobID
		}
		out = append(out, tg)
		if len(out) == MaxJobs {
			break
		}
	}
	return out
}
