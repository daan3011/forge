package prs

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/cifix"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
)

// "Fix this" (P2.8): F on a PR with failing checks gathers the failure of
// its head commit's failing Actions jobs (other checks by name and link)
// and asks the Sessions tab to hand it to an agent: a live session linked
// to the PR gets the prompt, else a new fix-ci session starts on the head
// branch. alt+f opens the prompt for editing first.

const fixTimeout = 90 * time.Second

type fixGatheredMsg struct {
	id  string // PR id
	src tabs.Source
	err error
}

func (fixGatheredMsg) TargetTab() string { return Title }

func (t *Tab) fix(edit bool) tea.Cmd {
	p, ok := t.selected()
	if !ok {
		return nil
	}
	if t.gh == nil {
		return errCmd(errors.New("GitHub isn't available"))
	}
	if p.State == github.StateMerged || p.State == github.StateClosed {
		return info(fmt.Sprintf("#%d is %s; nothing to fix", p.Number, p.State))
	}
	if e, fresh := t.det.entry(p); fresh && !hasFailing(e.d) {
		// A detail for this very version of the PR is the final word; the
		// list's rollup can lag behind it.
		return info(fmt.Sprintf("#%d has no failing checks", p.Number))
	}
	id := p.ID()
	if _, busy := t.busy[id]; busy {
		return info(fmt.Sprintf("#%d: already on it", p.Number))
	}
	t.busy[id] = fmt.Sprintf("gathering the CI failure of #%d…", p.Number)
	var detail *github.PRDetail
	if e, fresh := t.det.entry(p); fresh {
		d := e.d
		detail = &d
	}
	gh, runs, now := t.gh, t.runs, t.now()
	var kv cifix.KV // a nil tabs.KVStore must stay a nil interface
	if t.store != nil {
		kv = t.store
	}
	var logs cifix.Logs
	if runs != nil {
		logs = runs
	}
	project := ""
	if t.project != nil {
		project = t.project.Name
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fixTimeout)
		defer cancel()
		if detail == nil {
			d, err := gh.PRDetail(ctx, p.Repo, p.Number)
			if err != nil {
				return fixGatheredMsg{id: id, err: fmt.Errorf("couldn't load the checks of #%d: %s", p.Number, github.Reason(err))}
			}
			detail = &d
		}
		targets := FixTargets(p, *detail)
		if len(targets) == 0 {
			return fixGatheredMsg{id: id, err: fmt.Errorf("#%d has no failing checks", p.Number)}
		}
		vars := cifix.Vars(cifix.Summarize(ctx, logs, kv, project, now, targets))
		return fixGatheredMsg{id: id, src: tabs.Source{
			Kind: store.KindGitHubPR, ID: id, Number: p.Number, URL: prURL(p),
			Title: p.Title, State: p.State, Repo: p.Repo, Base: p.BaseRef, Branch: p.HeadRef,
			Template: cifix.Template, Vars: vars,
			Fix: &tabs.FixRequest{Reuse: true, Immediate: !edit},
		}}
	}
}

func hasFailing(d github.PRDetail) bool {
	for _, c := range d.Checks {
		if c.State == github.CheckFail {
			return true
		}
	}
	return false
}

// FixTargets are the PR's failing checks as failure targets, up to
// cifix.MaxJobs: Actions jobs with their run and job ids, other checks
// (commit statuses, third-party apps) by name and URL.
func FixTargets(p github.PR, d github.PRDetail) []cifix.Target {
	var out []cifix.Target
	for _, c := range d.Checks {
		if c.State != github.CheckFail {
			continue
		}
		run := cifix.Run{
			Repo: p.Repo, Workflow: c.Workflow, Branch: p.HeadRef,
			HeadSHA: d.HeadSHA, CommitMsg: d.HeadMessage,
		}
		tg := cifix.Target{Run: run, Job: c.Name, URL: c.URL}
		if runID, jobID, ok := c.CheckIDs(); ok {
			tg.Run.ID, tg.JobID = runID, jobID
		}
		out = append(out, tg)
		if len(out) == cifix.MaxJobs {
			break
		}
	}
	return out
}

func (t *Tab) fixGathered(msg fixGatheredMsg) tea.Cmd {
	delete(t.busy, msg.id)
	if msg.err != nil {
		return errCmd(msg.err)
	}
	return tabs.StartSessionFrom(msg.src)
}
