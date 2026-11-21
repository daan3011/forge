package pipelines

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/cifix"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
)

// "Fix this" (P2.8): F on a failed run (or in the log viewer, on the job
// shown) gathers the failure (error summaries, fetching logs not seen
// yet), finds the PR of the run's branch and asks the Sessions tab to hand
// it to an agent. alt+f does the same with the prompt opened for editing.

const fixTimeout = 90 * time.Second

type fixGatheredMsg struct {
	key string // run key
	src tabs.Source
	err error
}

func (fixGatheredMsg) TargetTab() string { return Title }

// fix starts gathering the failure of the target run.
func (t *Tab) fix(edit bool) tea.Cmd {
	r, ok := t.actionTarget()
	if !ok {
		return nil
	}
	if t.gh == nil {
		return errCmd(errors.New("GitHub isn't available"))
	}
	var jobs []github.Job // nil: fetch them
	if v := t.log; v != nil {
		j, ok := v.current()
		if !ok || j.State != github.CheckFail {
			return info("this job didn't fail; nothing to fix")
		}
		jobs = []github.Job{j}
	} else {
		if r.State != github.CheckFail {
			return info(runLabel(r) + " didn't fail; nothing to fix")
		}
		if e := t.det.jobs[r.Key()]; e != nil && e.jobs != nil && e.forUpdated.Equal(r.UpdatedAt) {
			jobs = failedJobs(e.jobs)
		}
	}
	k := r.Key()
	if t.fixing[k] {
		return info(runLabel(r) + ": already gathering the failure")
	}
	t.fixing[k] = true
	gh, pulls, kv, now := t.gh, t.pulls, t.store, t.now()
	project := ""
	if t.project != nil {
		project = t.project.Name
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fixTimeout)
		defer cancel()
		src, err := gatherRunFix(ctx, gh, pulls, kv, project, now, r, jobs, edit)
		return fixGatheredMsg{key: k, src: src, err: err}
	}
}

func failedJobs(jobs []github.Job) []github.Job {
	var out []github.Job
	for _, j := range jobs {
		if j.State == github.CheckFail {
			out = append(out, j)
		}
	}
	return out
}

// gatherRunFix builds the fix source for run r: its failed jobs (fetched
// when jobs is nil), their error context and the PR of its branch. With no
// open PR the source is the run itself, on a new fix branch at the failing
// commit, after a confirm.
func gatherRunFix(ctx context.Context, gh github.RunsClient, pulls github.PullFinder, kv tabs.KVStore,
	project string, now time.Time, r github.Run, jobs []github.Job, edit bool,
) (tabs.Source, error) {
	if jobs == nil {
		all, err := gh.RunJobs(ctx, r.Repo, r.ID)
		if err != nil {
			return tabs.Source{}, fmt.Errorf("couldn't load the jobs of %s: %s", runLabel(r), github.Reason(err))
		}
		jobs = failedJobs(all)
	}
	if len(jobs) == 0 {
		return tabs.Source{}, fmt.Errorf("%s has no failed jobs to fix", runLabel(r))
	}
	run := cifix.RunOf(r)
	targets := make([]cifix.Target, 0, len(jobs))
	for _, j := range jobs {
		targets = append(targets, cifix.Target{Run: run, JobID: j.ID, Job: j.Name, URL: j.URL})
	}
	var cache cifix.KV // a nil tabs.KVStore must stay a nil interface
	if kv != nil {
		cache = kv
	}
	vars := cifix.Vars(cifix.Summarize(ctx, gh, cache, project, now, targets))

	pr, ok := openPull(ctx, pulls, r.Repo, r.Branch)
	if ok {
		return tabs.Source{
			Kind: store.KindGitHubPR, ID: fmt.Sprintf("%s#%d", r.Repo, pr.Number), Number: pr.Number,
			URL: pr.URL, Title: pr.Title, State: pr.State, Repo: r.Repo, Branch: r.Branch,
			Template: cifix.Template, Vars: vars,
			Fix: &tabs.FixRequest{Reuse: true, Immediate: !edit},
		}, nil
	}
	branch := cifix.FixBranch(r.Workflow, r.ID)
	return tabs.Source{
		Kind: store.KindGitHubRun, ID: fmt.Sprintf("%s#%d", r.Repo, r.ID), URL: r.URL,
		Title: runLabel(r), Repo: r.Repo, Base: r.Branch, Branch: branch,
		Template: cifix.Template, Vars: vars,
		Fix: &tabs.FixRequest{
			Immediate: !edit, StartCommit: r.HeadSHA,
			Confirm: fmt.Sprintf("No open PR for %s. Start a fix session on a new branch %s from %s?",
				r.Branch, branch, short(r.HeadSHA)),
		},
	}, nil
}

// openPull is the newest open (or draft) PR from branch; a failed lookup
// counts as none.
func openPull(ctx context.Context, pulls github.PullFinder, repo, branch string) (github.Pull, bool) {
	if pulls == nil || branch == "" {
		return github.Pull{}, false
	}
	ps, err := pulls.PullsForBranch(ctx, repo, branch)
	if err != nil {
		return github.Pull{}, false
	}
	i := slices.IndexFunc(ps, func(p github.Pull) bool {
		return p.State == github.StateOpen || p.State == github.StateDraft
	})
	if i < 0 {
		return github.Pull{}, false
	}
	return ps[i], true
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func (t *Tab) fixGathered(msg fixGatheredMsg) tea.Cmd {
	delete(t.fixing, msg.key)
	if msg.err != nil {
		return errCmd(msg.err)
	}
	return tabs.StartSessionFrom(msg.src)
}

// Busy implements tabs.Busier while a failure is being gathered.
func (t *Tab) Busy() string {
	if len(t.fixing) == 0 {
		return ""
	}
	return "gathering the CI failure…"
}
