package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/daan3011/forge/internal/store"
)

func TestToolsListed(t *testing.T) {
	e := newEnv(t)
	res, err := e.cs.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"create_issue", "update_issue", "comment_issue", "list_issues", "get_issue",
		"write_doc", "read_doc", "list_docs", "log_decision", "pin_artifact", "list_artifacts",
		"set_session_summary", "report_progress", "get_context_pack",
	}
	got := map[string]*mcp.Tool{}
	for _, tl := range res.Tools {
		got[tl.Name] = tl
	}
	for _, n := range want {
		tl, ok := got[n]
		if !ok {
			t.Errorf("missing tool %s", n)
			continue
		}
		if len(tl.Description) < 40 || tl.InputSchema == nil {
			t.Errorf("%s: thin description or no schema", n)
		}
	}
	if len(res.Tools) != len(want) {
		t.Errorf("%d tools, want %d (no delete tools)", len(res.Tools), len(want))
	}
	for n := range got {
		if strings.Contains(n, "delete") || strings.Contains(n, "remove") {
			t.Errorf("destructive tool %s", n)
		}
	}
}

// The "done when" flow: filing a ticket makes a proposal linked to the
// session, visible as proposed for Home, and pings the TUI.
func TestCreateIssueProposes(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	var out CreateIssueOut
	e.call("create_issue", map[string]any{
		"title": "Add retries to the Plane client", "description": "Transient 5xx fail the refresh.",
		"priority": "medium", "labels": []string{"bug"},
	}, &out)
	if out.Issue.ID != "P-1" || !out.Issue.Proposed || out.Issue.Status != "proposed" || out.Issue.FiledBy != "fix" {
		t.Errorf("out %+v", out)
	}
	if !strings.Contains(out.Message, "Home") {
		t.Errorf("message %q", out.Message)
	}
	if e.plane.CreatedCount() != 0 {
		t.Fatal("proposal must not reach Plane")
	}
	ps, err := e.st.Proposals().List(ctx, store.ProposalFilter{Status: store.ProposalProposed, Project: "forge"})
	if err != nil || len(ps) != 1 {
		t.Fatalf("proposals %+v %v", ps, err)
	}
	p := ps[0]
	if p.SessionID != e.x.ID || p.PlaneProjectID != "pp-1" || p.Priority != "medium" || p.Labels[0] != "bug" {
		t.Errorf("proposal %+v", p)
	}
	if ls := e.sessionLinks(store.KindProposal); len(ls) != 1 || ls[0].Other.ID != "1" {
		t.Errorf("session links %+v", ls)
	}
	if e.pingCount() == 0 {
		t.Error("no ping")
	}
	if e.pings[0].Session != "fix" || e.pings[0].Event != PingEvent || e.pings[0].Changed {
		t.Errorf("ping %+v", e.pings[0])
	}
}

func TestCreateIssueAutoCreate(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, withAutoCreate())
	pr := store.PRRef("daan3011/forge", 3)
	if err := e.st.Links().Add(ctx, store.SessionRef(e.x.ID), pr, store.LinkMeta{Title: "Fix"}); err != nil {
		t.Fatal(err)
	}
	var out CreateIssueOut
	e.call("create_issue", map[string]any{"title": "Add retries"}, &out)
	if out.Issue.ID != "FORGE-3" || out.Issue.Proposed {
		t.Errorf("out %+v", out)
	}
	if e.plane.CreatedCount() != 1 || !strings.Contains(e.plane.Created[0].DescriptionHTML, "daan3011/forge#3") {
		t.Fatalf("created %+v", e.plane.Created)
	}
	p, _ := e.st.Proposals().Get(ctx, 1)
	if p.Status != store.ProposalAccepted || p.PlaneKey != "FORGE-3" {
		t.Errorf("row %+v", p)
	}
	if ls := e.sessionLinks(store.KindPlaneIssue); len(ls) != 1 || ls[0].Other.ID != "FORGE-3" {
		t.Errorf("issue links %+v", ls)
	}
}

func TestCreateIssueValidation(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"no title", map[string]any{"title": "  "}, "title is required"},
		{"long title", map[string]any{"title": strings.Repeat("x", 300)}, "under 255"},
		{"bad priority", map[string]any{"title": "x", "priority": "asap"}, "priority"},
		{"missing title", map[string]any{}, "title"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if msg := e.callErr("create_issue", tt.args); !strings.Contains(msg, tt.want) {
				t.Errorf("error %q lacks %q", msg, tt.want)
			}
		})
	}
}

func TestUpdateIssue(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.call("create_issue", map[string]any{"title": "Add retries"}, nil)

	var out IssueOut
	e.call("update_issue", map[string]any{"id": "P-1", "title": "Add retries with backoff", "priority": "high"}, &out)
	if out.Title != "Add retries with backoff" || out.Priority != "high" {
		t.Errorf("proposal update %+v", out)
	}
	if msg := e.callErr("update_issue", map[string]any{"id": "P-1", "state": "Done"}); !strings.Contains(msg, "no Plane state") {
		t.Errorf("state on proposal: %q", msg)
	}

	e.call("update_issue", map[string]any{"id": "FORGE-7", "state": "in progress", "description": "Now **fixed**"}, &out)
	if out.Status != "In Progress" || out.ID != "FORGE-7" {
		t.Errorf("plane update %+v", out)
	}
	up := e.plane.Updated["wi-a"]
	if up.StateID == nil || *up.StateID != "s-doing" || up.DescriptionHTML == nil || !strings.Contains(*up.DescriptionHTML, "<strong>fixed</strong>") {
		t.Errorf("sent %+v", up)
	}
	if msg := e.callErr("update_issue", map[string]any{"id": "FORGE-7", "state": "Nope"}); !strings.Contains(msg, "Todo, In Progress") {
		t.Errorf("unknown state: %q", msg)
	}
	if ls := e.sessionLinks(store.KindPlaneIssue); len(ls) != 1 || ls[0].Other.ID != "FORGE-7" {
		t.Errorf("links %+v", ls)
	}

	// Once dropped, a proposal can't be updated.
	if err := e.st.Proposals().Drop(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if msg := e.callErr("update_issue", map[string]any{"id": "P-1", "title": "x"}); !strings.Contains(msg, "dropped") {
		t.Errorf("dropped: %q", msg)
	}
}

func TestCommentIssue(t *testing.T) {
	e := newEnv(t)
	var out CommentIssueOut
	e.call("comment_issue", map[string]any{"id": "FORGE-7", "body": "Root cause is the *timeout*."}, &out)
	if out.CommentID == "" || len(e.plane.Commented) != 1 {
		t.Fatalf("out %+v", out)
	}
	html := e.plane.Commented[0].CommentHTML
	if !strings.Contains(html, "<em>timeout</em>") || !strings.Contains(html, "forge session fix") {
		t.Errorf("comment html %s", html)
	}
	e.call("create_issue", map[string]any{"title": "x"}, nil)
	if msg := e.callErr("comment_issue", map[string]any{"id": "P-1", "body": "x"}); !strings.Contains(msg, "still a proposal") {
		t.Errorf("proposal comment: %q", msg)
	}
	if msg := e.callErr("comment_issue", map[string]any{"id": "FORGE-99", "body": "x"}); !strings.Contains(msg, "FORGE-99") {
		t.Errorf("unknown: %q", msg)
	}
}

func TestListAndGetIssues(t *testing.T) {
	e := newEnv(t)
	e.call("create_issue", map[string]any{"title": "Add retries", "description": "desc"}, nil)

	var out ListIssuesOut
	e.call("list_issues", map[string]any{}, &out)
	var ids []string
	for _, i := range out.Issues {
		ids = append(ids, i.ID+":"+i.Status)
	}
	if strings.Join(ids, ",") != "P-1:proposed,FORGE-7:Todo,FORGE-8:In Progress" {
		t.Errorf("all: %v", ids)
	}
	e.call("list_issues", map[string]any{"state_group": "started", "exclude_proposed": true}, &out)
	if len(out.Issues) != 1 || out.Issues[0].ID != "FORGE-8" {
		t.Errorf("started: %+v", out.Issues)
	}
	e.call("list_issues", map[string]any{"assigned_to_me": true, "search": "bug"}, &out)
	if len(out.Issues) != 1 || out.Issues[0].ID != "FORGE-7" {
		t.Errorf("mine: %+v", out.Issues)
	}
	if msg := e.callErr("list_issues", map[string]any{"state_group": "doing"}); !strings.Contains(msg, "state_group") {
		t.Errorf("bad group: %q", msg)
	}

	var g GetIssueOut
	e.call("get_issue", map[string]any{"id": "FORGE-7"}, &g)
	if !strings.Contains(g.Issue.Description, "**breaks**") || len(g.Comments) != 1 || !strings.Contains(g.Comments[0].Body, "seen it") {
		t.Errorf("get plane: %+v", g)
	}
	e.call("get_issue", map[string]any{"id": "P-1"}, &g)
	if g.Issue.Description != "desc" || !g.Issue.Proposed {
		t.Errorf("get proposal: %+v", g)
	}
	if msg := e.callErr("get_issue", map[string]any{"id": "P-9"}); !strings.Contains(msg, "no proposal") {
		t.Errorf("missing: %q", msg)
	}
}

func TestPlaneUnavailable(t *testing.T) {
	e := newEnv(t, withoutPlane())
	// Proposals still work without Plane.
	var c CreateIssueOut
	e.call("create_issue", map[string]any{"title": "x"}, &c)
	if c.Issue.ID != "P-1" {
		t.Errorf("create %+v", c)
	}
	var out ListIssuesOut
	e.call("list_issues", map[string]any{}, &out)
	if len(out.Issues) != 1 || !strings.Contains(out.Warning, "plane") {
		t.Errorf("list %+v", out)
	}
	if msg := e.callErr("get_issue", map[string]any{"id": "FORGE-7"}); !strings.Contains(msg, "plane") {
		t.Errorf("get: %q", msg)
	}
}
