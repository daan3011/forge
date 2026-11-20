package github

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

// Normalised check states: check runs and legacy commit statuses both map
// to one of these.
const (
	CheckFail    = "fail"
	CheckRunning = "running"
	CheckPending = "pending" // queued, waiting or expected
	CheckPass    = "pass"
	CheckSkipped = "skipped" // skipped, neutral, cancelled or stale
)

// Review states (GraphQL PullRequestReviewState); ReviewPending is also a
// requested reviewer who hasn't reviewed (again) yet.
const (
	ReviewStateApproved         = "APPROVED"
	ReviewStateChangesRequested = "CHANGES_REQUESTED"
	ReviewStateCommented        = "COMMENTED"
	ReviewStateDismissed        = "DISMISSED"
	ReviewStatePending          = "PENDING"
)

// Merge state statuses (GraphQL MergeStateStatus) the detail pane explains.
const (
	MergeStateBehind   = "BEHIND"
	MergeStateBlocked  = "BLOCKED"
	MergeStateClean    = "CLEAN"
	MergeStateDirty    = "DIRTY"
	MergeStateUnstable = "UNSTABLE"
)

// Event kinds of a PR's activity.
const (
	EventComment = "comment"
	EventReview  = "review"
)

// Limits of one detail fetch.
const (
	detailLabels    = 20
	detailReviewers = 20
	detailChecks    = 100
	detailEvents    = 30
)

// PRDetail is what the PR detail pane shows beyond the list row. It is also
// the cache format, so fields keep their JSON names.
type PRDetail struct {
	UpdatedAt      time.Time  `json:"updated_at"`
	Body           string     `json:"body"`
	Labels         []Label    `json:"labels"`
	ReviewDecision string     `json:"review_decision"`
	Mergeable      string     `json:"mergeable"`
	MergeState     string     `json:"merge_state"`
	Reviewers      []Reviewer `json:"reviewers"`
	Approvals      int        `json:"approvals"`
	// Checks are ordered failing, running, pending, passed, skipped.
	Checks []Check `json:"checks"`
	// Activity is comments and reviews, newest first.
	Activity []Event `json:"activity"`
	// MergeMethods are the merge methods the repo allows (MergeMethod*),
	// in GitHub's order; empty when unknown (an older cache entry).
	MergeMethods []string `json:"merge_methods"`
}

// Label is a PR label; Color is GitHub's hex without "#".
type Label struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// Reviewer is a requested or past reviewer: a user login or "org/team".
type Reviewer struct {
	Login string `json:"login"`
	State string `json:"state"` // a ReviewState* constant
}

// Check is one check run or commit status of the head commit.
type Check struct {
	Name        string    `json:"name"`
	Workflow    string    `json:"workflow,omitempty"`
	State       string    `json:"state"` // a Check* constant
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	URL         string    `json:"url"`
}

// Event is a comment or a review.
type Event struct {
	Kind   string    `json:"kind"`   // EventComment or EventReview
	Author string    `json:"author"` // "" for a deleted user
	State  string    `json:"state"`  // reviews: a ReviewState* constant
	Body   string    `json:"body"`
	At     time.Time `json:"at"`
}

// PRDetailer fetches a PR's details.
type PRDetailer interface {
	// PRDetail fetches the detail of repo ("owner/name") #number.
	PRDetail(ctx context.Context, repo string, number int) (PRDetail, error)
}

// PRClient is what the PRs tab needs from GitHub.
type PRClient interface {
	PRSearcher
	PRDetailer
	PRActor
	PRReviewer
}

var _ PRClient = (*Client)(nil)

var prDetailQuery = fmt.Sprintf(`query ForgePRDetail($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    mergeCommitAllowed squashMergeAllowed rebaseMergeAllowed
    pullRequest(number: $number) {
      body updatedAt reviewDecision mergeable mergeStateStatus
      labels(first: %[1]d) { nodes { name color } }
      reviewRequests(first: %[2]d) { nodes { requestedReviewer {
        ... on User { login }
        ... on Bot { login }
        ... on Mannequin { login }
        ... on Team { slug organization { login } }
      } } }
      latestReviews(first: %[2]d) { nodes { state author { login } } }
      commits(last: 1) { nodes { commit { statusCheckRollup { contexts(first: %[3]d) { nodes {
        __typename
        ... on CheckRun {
          name status conclusion startedAt completedAt detailsUrl
          checkSuite { workflowRun { workflow { name } } }
        }
        ... on StatusContext { context state createdAt targetUrl }
      } } } } } }
      comments(last: %[4]d) { nodes { body createdAt author { login } } }
      reviews(last: %[4]d) { nodes { state body submittedAt author { login } } }
    }
  }
}`, detailLabels, detailReviewers, detailChecks, detailEvents)

// PRDetail implements PRDetailer with one GraphQL query.
func (c *Client) PRDetail(ctx context.Context, repo string, number int) (PRDetail, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || number <= 0 {
		return PRDetail{}, fmt.Errorf("github: PR detail: want owner/repo and a number, got %q #%d", repo, number)
	}
	g, err := c.graphQL()
	if err != nil {
		return PRDetail{}, fmt.Errorf("github: %w", err)
	}
	var data json.RawMessage
	vars := map[string]any{"owner": owner, "name": name, "number": number}
	if err := g.DoWithContext(ctx, prDetailQuery, vars, &data); err != nil {
		return PRDetail{}, fmt.Errorf("github: %s#%d: %w", repo, number, err)
	}
	d, err := ParsePRDetail(bytes.NewReader(data))
	if err != nil {
		return PRDetail{}, fmt.Errorf("github: %s#%d: %w", repo, number, err)
	}
	return d, nil
}

type gqlLogin struct {
	Login string `json:"login"`
}

func login(a *gqlLogin) string {
	if a == nil {
		return ""
	}
	return a.Login
}

type gqlDetail struct {
	Repository *struct {
		MergeCommitAllowed bool `json:"mergeCommitAllowed"`
		SquashMergeAllowed bool `json:"squashMergeAllowed"`
		RebaseMergeAllowed bool `json:"rebaseMergeAllowed"`
		PullRequest        *struct {
			Body             string    `json:"body"`
			UpdatedAt        time.Time `json:"updatedAt"`
			ReviewDecision   string    `json:"reviewDecision"`
			Mergeable        string    `json:"mergeable"`
			MergeStateStatus string    `json:"mergeStateStatus"`
			Labels           struct {
				Nodes []Label `json:"nodes"`
			} `json:"labels"`
			ReviewRequests struct {
				Nodes []struct {
					RequestedReviewer *struct {
						Login        string    `json:"login"`
						Slug         string    `json:"slug"`
						Organization *gqlLogin `json:"organization"`
					} `json:"requestedReviewer"`
				} `json:"nodes"`
			} `json:"reviewRequests"`
			LatestReviews struct {
				Nodes []struct {
					State  string    `json:"state"`
					Author *gqlLogin `json:"author"`
				} `json:"nodes"`
			} `json:"latestReviews"`
			Commits struct {
				Nodes []struct {
					Commit struct {
						StatusCheckRollup *struct {
							Contexts struct {
								Nodes []gqlContext `json:"nodes"`
							} `json:"contexts"`
						} `json:"statusCheckRollup"`
					} `json:"commit"`
				} `json:"nodes"`
			} `json:"commits"`
			Comments struct {
				Nodes []struct {
					Body      string    `json:"body"`
					CreatedAt time.Time `json:"createdAt"`
					Author    *gqlLogin `json:"author"`
				} `json:"nodes"`
			} `json:"comments"`
			Reviews struct {
				Nodes []struct {
					State       string     `json:"state"`
					Body        string     `json:"body"`
					SubmittedAt *time.Time `json:"submittedAt"`
					Author      *gqlLogin  `json:"author"`
				} `json:"nodes"`
			} `json:"reviews"`
		} `json:"pullRequest"`
	} `json:"repository"`
}

type gqlContext struct {
	Typename string `json:"__typename"`
	// CheckRun
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	Conclusion  string     `json:"conclusion"`
	StartedAt   *time.Time `json:"startedAt"`
	CompletedAt *time.Time `json:"completedAt"`
	DetailsURL  string     `json:"detailsUrl"`
	CheckSuite  *struct {
		WorkflowRun *struct {
			Workflow *struct {
				Name string `json:"name"`
			} `json:"workflow"`
		} `json:"workflowRun"`
	} `json:"checkSuite"`
	// StatusContext
	Context   string     `json:"context"`
	State     string     `json:"state"`
	CreatedAt *time.Time `json:"createdAt"`
	TargetURL string     `json:"targetUrl"`
}

func deref(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// ParsePRDetail reads the data object of a PRDetail response.
func ParsePRDetail(r io.Reader) (PRDetail, error) {
	var raw gqlDetail
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return PRDetail{}, err
	}
	if raw.Repository == nil || raw.Repository.PullRequest == nil {
		return PRDetail{}, fmt.Errorf("PR not found")
	}
	pr := raw.Repository.PullRequest
	d := PRDetail{
		UpdatedAt: pr.UpdatedAt, Body: pr.Body, ReviewDecision: pr.ReviewDecision,
		Mergeable: pr.Mergeable, MergeState: pr.MergeStateStatus,
		Labels: []Label{}, Reviewers: []Reviewer{}, Checks: []Check{}, Activity: []Event{},
	}
	d.Labels = append(d.Labels, pr.Labels.Nodes...)
	for _, m := range []struct {
		ok     bool
		method string
	}{
		{raw.Repository.MergeCommitAllowed, MergeMethodMerge},
		{raw.Repository.SquashMergeAllowed, MergeMethodSquash},
		{raw.Repository.RebaseMergeAllowed, MergeMethodRebase},
	} {
		if m.ok {
			d.MergeMethods = append(d.MergeMethods, m.method)
		}
	}

	// Past reviewers first, then requests; a re-requested reviewer is
	// pending again.
	for _, n := range pr.LatestReviews.Nodes {
		if n.State == ReviewStatePending {
			continue // the viewer's unsubmitted review
		}
		if n.State == ReviewStateApproved {
			d.Approvals++
		}
		d.Reviewers = append(d.Reviewers, Reviewer{Login: login(n.Author), State: n.State})
	}
	for _, n := range pr.ReviewRequests.Nodes {
		rr := n.RequestedReviewer
		if rr == nil {
			continue
		}
		name := rr.Login
		if rr.Slug != "" {
			name = rr.Slug
			if o := login(rr.Organization); o != "" {
				name = o + "/" + rr.Slug
			}
		}
		if name == "" {
			continue
		}
		if i := slices.IndexFunc(d.Reviewers, func(r Reviewer) bool { return r.Login == name }); i >= 0 {
			if d.Reviewers[i].State == ReviewStateApproved {
				d.Approvals--
			}
			d.Reviewers[i].State = ReviewStatePending
			continue
		}
		d.Reviewers = append(d.Reviewers, Reviewer{Login: name, State: ReviewStatePending})
	}

	if len(pr.Commits.Nodes) > 0 {
		if roll := pr.Commits.Nodes[0].Commit.StatusCheckRollup; roll != nil {
			for _, c := range roll.Contexts.Nodes {
				if ch, ok := parseContext(c); ok {
					d.Checks = append(d.Checks, ch)
				}
			}
		}
	}
	slices.SortStableFunc(d.Checks, func(a, b Check) int { return cmp.Compare(checkRank(a.State), checkRank(b.State)) })

	for _, c := range pr.Comments.Nodes {
		d.Activity = append(d.Activity, Event{Kind: EventComment, Author: login(c.Author), Body: c.Body, At: c.CreatedAt})
	}
	for _, rv := range pr.Reviews.Nodes {
		// Skip drafts, and the empty reviews that only carry inline replies.
		if rv.State == ReviewStatePending || rv.SubmittedAt == nil ||
			(rv.State == ReviewStateCommented && strings.TrimSpace(rv.Body) == "") {
			continue
		}
		d.Activity = append(d.Activity, Event{Kind: EventReview, Author: login(rv.Author), State: rv.State, Body: rv.Body, At: *rv.SubmittedAt})
	}
	slices.SortStableFunc(d.Activity, func(a, b Event) int { return b.At.Compare(a.At) })
	return d, nil
}

func parseContext(c gqlContext) (Check, bool) {
	switch c.Typename {
	case "CheckRun":
		ch := Check{
			Name: c.Name, State: checkRunState(c.Status, c.Conclusion), URL: c.DetailsURL,
			StartedAt: deref(c.StartedAt), CompletedAt: deref(c.CompletedAt),
		}
		if s := c.CheckSuite; s != nil && s.WorkflowRun != nil && s.WorkflowRun.Workflow != nil {
			ch.Workflow = s.WorkflowRun.Workflow.Name
		}
		return ch, true
	case "StatusContext":
		return Check{Name: c.Context, State: statusState(c.State), URL: c.TargetURL, StartedAt: deref(c.CreatedAt)}, true
	}
	return Check{}, false
}

func checkRunState(status, conclusion string) string {
	switch status {
	case "IN_PROGRESS":
		return CheckRunning
	case "COMPLETED":
	default:
		return CheckPending // QUEUED, WAITING, PENDING, REQUESTED
	}
	switch conclusion {
	case "SUCCESS":
		return CheckPass
	case "FAILURE", "TIMED_OUT", "STARTUP_FAILURE", "ACTION_REQUIRED":
		return CheckFail
	}
	return CheckSkipped // NEUTRAL, SKIPPED, CANCELLED, STALE
}

func statusState(s string) string {
	switch s {
	case "SUCCESS":
		return CheckPass
	case "FAILURE", "ERROR":
		return CheckFail
	}
	return CheckPending // PENDING, EXPECTED
}

func checkRank(state string) int {
	switch state {
	case CheckFail:
		return 0
	case CheckRunning:
		return 1
	case CheckPending:
		return 2
	case CheckPass:
		return 3
	}
	return 4
}

// Duration is how long a check ran: until it completed, or until now while
// it runs. Zero when it never started.
func (c Check) Duration(now time.Time) time.Duration {
	if c.StartedAt.IsZero() {
		return 0
	}
	end := c.CompletedAt
	if end.IsZero() {
		if c.State != CheckRunning {
			return 0
		}
		end = now
	}
	return max(end.Sub(c.StartedAt), 0)
}
