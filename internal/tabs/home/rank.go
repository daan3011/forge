package home

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
)

// kind is a "needs me" row's group; lower kinds come first.
type kind int

const (
	kindWaiting  kind = iota + 1 // agent waiting for input
	kindErrored                  // agent errored
	kindProposed                 // issue an agent proposed through forge mcp (P6.5)
	kindReview                   // review requested from me
	kindFailing                  // my PR with failing checks
	kindApproved                 // my PR approved and green
	kindIssue                    // Plane item in the current cycle, mine
)

// source is the kind's source, as ui.Theme.SourceIcon takes it.
func (k kind) source() string {
	switch k {
	case kindWaiting, kindErrored:
		return "agent"
	case kindIssue, kindProposed:
		return "plane"
	}
	return "pr"
}

// item is one "needs me" row.
type item struct {
	kind     kind
	id       string    // unique row id: "agent:<name>", "pr:<owner/repo#n>", "plane:<uuid>", "proposal:<id>"
	at       time.Time // when it started needing me (sorts oldest first)
	agent    sessions.Row
	pr       github.PR
	issue    plane.WorkItem
	proposal store.Proposal
}

// inputs are what the sources loaded.
type inputs struct {
	sessions  []sessions.Row
	reviews   []github.PR // review-requested:@me
	mine      []github.PR // author:@me
	issues    []plane.WorkItem
	proposals []store.Proposal // waiting for accept / edit / drop
}

// mergeReady reports whether p is approved, green and can be merged.
func mergeReady(p github.PR) bool {
	return p.ReviewDecision == github.ReviewApproved && p.CI == github.CISuccess &&
		!p.Draft && p.Mergeable != github.Conflicting
}

// rank builds the prioritised list: waiting agents, errored agents,
// issues agents proposed, review requests, my failing PRs, my approved green PRs, then my Plane items in
// the current cycle. Within a group the oldest comes first, except Plane
// items: in progress first, then by priority, then most recently updated.
func rank(in inputs) []item {
	var out []item
	seen := map[string]bool{}
	add := func(it item) {
		if seen[it.id] {
			return
		}
		seen[it.id] = true
		out = append(out, it)
	}
	for _, r := range in.sessions {
		var k kind
		switch r.Status {
		case store.StatusWaiting:
			k = kindWaiting
		case store.StatusError:
			k = kindErrored
		default:
			continue
		}
		add(item{kind: k, id: "agent:" + r.Name, at: firstSet(r.StatusChangedAt, r.LastActivityAt), agent: r})
	}
	for _, p := range in.proposals {
		add(item{kind: kindProposed, id: "proposal:" + strconv.FormatInt(p.ID, 10), at: p.CreatedAt, proposal: p})
	}
	for _, p := range in.reviews {
		if p.Open() {
			add(item{kind: kindReview, id: "pr:" + p.ID(), at: firstSet(p.CreatedAt, p.UpdatedAt), pr: p})
		}
	}
	for _, p := range in.mine {
		switch {
		case !p.Open():
		case p.Failing():
			add(item{kind: kindFailing, id: "pr:" + p.ID(), at: p.UpdatedAt, pr: p})
		case mergeReady(p):
			add(item{kind: kindApproved, id: "pr:" + p.ID(), at: p.UpdatedAt, pr: p})
		}
	}
	for _, w := range in.issues {
		add(item{kind: kindIssue, id: "plane:" + w.ID, at: w.UpdatedAt.Time, issue: w})
	}
	slices.SortStableFunc(out, func(a, b item) int {
		if c := cmp.Compare(a.kind, b.kind); c != 0 {
			return c
		}
		if a.kind == kindIssue {
			return compareIssues(a.issue, b.issue)
		}
		if c := a.at.Compare(b.at); c != 0 {
			return c
		}
		return strings.Compare(a.id, b.id)
	})
	return out
}

// compareIssues orders Plane items: in progress first, then by priority,
// then most recently updated.
func compareIssues(a, b plane.WorkItem) int {
	if c := cmp.Compare(groupRank(a), groupRank(b)); c != 0 {
		return c
	}
	if c := cmp.Compare(priorityRank(a.Priority), priorityRank(b.Priority)); c != 0 {
		return c
	}
	if c := b.UpdatedAt.Compare(a.UpdatedAt.Time); c != 0 {
		return c
	}
	return strings.Compare(a.Key(), b.Key())
}

func groupRank(w plane.WorkItem) int {
	if w.State == nil {
		return 3
	}
	switch w.State.Group {
	case plane.GroupStarted:
		return 0
	case plane.GroupUnstarted:
		return 1
	case plane.GroupBacklog, plane.GroupTriage:
		return 2
	}
	return 3
}

func priorityRank(p plane.Priority) int {
	switch p {
	case plane.PriorityUrgent:
		return 0
	case plane.PriorityHigh:
		return 1
	case plane.PriorityMedium:
		return 2
	case plane.PriorityLow:
		return 3
	}
	return 4
}

// firstSet is the first non-zero time.
func firstSet(ts ...time.Time) time.Time {
	for _, t := range ts {
		if !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

// prTitle is "#12 Title", with the repo's name when several repos show.
func prTitle(p github.PR, multi bool) string {
	s := fmt.Sprintf("#%d %s", p.Number, p.Title)
	if multi && p.Repo != "" {
		_, name, _ := strings.Cut(p.Repo, "/")
		s += " · " + name
	}
	return s
}
