package home

import (
	"slices"
	"testing"
	"time"

	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
)

var t0 = time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)

func agent(name string, st store.Status, minsAgo int) sessions.Row {
	return sessions.Row{Session: store.Session{Name: name, Status: st, StatusChangedAt: t0.Add(-time.Duration(minsAgo) * time.Minute)}}
}

func pr(n int, mod func(*github.PR)) github.PR {
	p := github.PR{Repo: "o/r", Number: n, Title: "t", State: github.StateOpen, UpdatedAt: t0.Add(-time.Duration(n) * time.Hour)}
	if mod != nil {
		mod(&p)
	}
	return p
}

func issue(id string, group plane.StateGroup, prio plane.Priority) plane.WorkItem {
	return plane.WorkItem{ID: id, Identifier: id, Priority: prio, State: &plane.State{Name: string(group), Group: group}}
}

func ids(items []item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.id
	}
	return out
}

func TestRank(t *testing.T) {
	green := func(p *github.PR) { p.ReviewDecision, p.CI = github.ReviewApproved, github.CISuccess }
	red := func(p *github.PR) { p.CI = github.CIFailure }
	tests := []struct {
		name string
		in   inputs
		want []string
	}{
		{
			name: "every group in order",
			in: inputs{
				sessions: []sessions.Row{agent("busy", store.StatusWorking, 1), agent("err", store.StatusError, 5), agent("wait", store.StatusWaiting, 2)},
				reviews:  []github.PR{pr(1, nil)},
				mine:     []github.PR{pr(2, green), pr(3, red), pr(4, nil)},
				issues:   []plane.WorkItem{issue("P-1", plane.GroupStarted, plane.PriorityNone)},
			},
			want: []string{"agent:wait", "agent:err", "pr:o/r#1", "pr:o/r#3", "pr:o/r#2", "plane:P-1"},
		},
		{
			name: "oldest first within a group",
			in: inputs{sessions: []sessions.Row{
				agent("new", store.StatusWaiting, 1), agent("old", store.StatusWaiting, 30), agent("mid", store.StatusWaiting, 10),
			}},
			want: []string{"agent:old", "agent:mid", "agent:new"},
		},
		{
			name: "error checks count as failing; pending and green-unapproved don't",
			in: inputs{mine: []github.PR{
				pr(1, func(p *github.PR) { p.CI = github.CIError }),
				pr(2, func(p *github.PR) { p.CI = github.CIPending }),
				pr(3, func(p *github.PR) { p.CI = github.CISuccess }),
			}},
			want: []string{"pr:o/r#1"},
		},
		{
			name: "approved but not mergeable is left out",
			in: inputs{mine: []github.PR{
				pr(1, func(p *github.PR) { green(p); p.Draft = true }),
				pr(2, func(p *github.PR) { green(p); p.Mergeable = github.Conflicting }),
				pr(3, func(p *github.PR) { p.ReviewDecision, p.CI = github.ReviewApproved, github.CIPending }),
				pr(4, green),
			}},
			want: []string{"pr:o/r#4"},
		},
		{
			name: "closed and merged PRs are left out",
			in: inputs{
				reviews: []github.PR{pr(1, func(p *github.PR) { p.State = github.StateClosed })},
				mine:    []github.PR{pr(2, func(p *github.PR) { red(p); p.State = github.StateMerged })},
			},
			want: nil,
		},
		{
			name: "a PR in both searches shows once",
			in:   inputs{reviews: []github.PR{pr(1, nil)}, mine: []github.PR{pr(1, red)}},
			want: []string{"pr:o/r#1"},
		},
		{
			name: "plane: in progress first, then priority",
			in: inputs{issues: []plane.WorkItem{
				issue("B-1", plane.GroupBacklog, plane.PriorityUrgent),
				issue("U-low", plane.GroupUnstarted, plane.PriorityLow),
				issue("U-high", plane.GroupUnstarted, plane.PriorityHigh),
				issue("S-1", plane.GroupStarted, plane.PriorityNone),
			}},
			want: []string{"plane:S-1", "plane:U-high", "plane:U-low", "plane:B-1"},
		},
		{
			name: "idle and dead agents don't need me",
			in:   inputs{sessions: []sessions.Row{agent("a", store.StatusIdle, 1), agent("b", store.StatusDead, 1)}},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ids(rank(tt.in)); !slices.Equal(got, tt.want) {
				t.Errorf("rank = %v, want %v", got, tt.want)
			}
		})
	}
}
