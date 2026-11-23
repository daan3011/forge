package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/proposals"
	"github.com/daan3011/forge/internal/store"
)

// Limits on what agents send.
const (
	maxTitle       = 255
	maxDescription = 64 << 10
	maxListLimit   = 100
)

var priorities = []string{"urgent", "high", "medium", "low", "none"}

// CreateIssueIn is create_issue's input.
type CreateIssueIn struct {
	Title       string   `json:"title,omitempty" jsonschema:"Short imperative issue title, e.g. 'Add retries to the Plane client'. At most 255 characters. Required unless capture is given."`
	Capture     string   `json:"capture,omitempty" jsonschema:"The user's one-line capture text, verbatim, e.g. 'Add retries to Plane client #forge !high +bug'. forge parses it: the words become the title (when title is empty), !priority sets the priority (by prefix: !h is high), +label adds labels and #project files it in that forge project. Pass it unchanged; don't parse it yourself."`
	Description string   `json:"description,omitempty" jsonschema:"Markdown body: the problem, why it matters, and what done looks like. Mention files or functions involved."`
	Priority    string   `json:"priority,omitempty" jsonschema:"One of urgent, high, medium, low, none. Leave empty when unsure."`
	Labels      []string `json:"labels,omitempty" jsonschema:"Plane label names, e.g. ['bug']. Unknown labels are dropped when the issue is created."`
}

// IssueOut describes an issue or proposal.
type IssueOut struct {
	ID          string   `json:"id" jsonschema:"Use this id with get_issue, update_issue and comment_issue. P-<n> is a proposal; a Plane key like FORGE-12 is a real issue."`
	Title       string   `json:"title"`
	Status      string   `json:"status" jsonschema:"proposed, accepted or dropped for proposals; the Plane state name for issues."`
	Proposed    bool     `json:"proposed" jsonschema:"True while the issue only exists as a proposal in forge."`
	Key         string   `json:"key,omitempty" jsonschema:"The Plane key once the issue exists in Plane."`
	Priority    string   `json:"priority,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	Description string   `json:"description,omitempty" jsonschema:"Markdown; only set by get_issue."`
	FiledBy     string   `json:"filed_by,omitempty" jsonschema:"The forge session that proposed it."`
}

// CreateIssueOut is create_issue's result.
type CreateIssueOut struct {
	Issue   IssueOut `json:"issue"`
	Message string   `json:"message"`
}

func checkPriority(p string) error {
	if p == "" || slices.Contains(priorities, p) {
		return nil
	}
	return fmt.Errorf("priority %q: use one of %s", p, strings.Join(priorities, ", "))
}

func checkIssueText(title, desc string) error {
	switch {
	case strings.TrimSpace(title) == "":
		return errors.New("title is required")
	case len(title) > maxTitle:
		return fmt.Errorf("title is %d characters; keep it under %d", len(title), maxTitle)
	case len(desc) > maxDescription:
		return fmt.Errorf("description is too long (%d bytes, max %d)", len(desc), maxDescription)
	}
	return nil
}

func proposalOut(p store.Proposal) IssueOut {
	return IssueOut{
		ID: p.Handle(), Title: p.Title, Status: string(p.Status), Proposed: p.Status == store.ProposalProposed,
		Key: p.PlaneKey, Priority: p.Priority, Labels: p.Labels, FiledBy: p.SessionName,
	}
}

func (s *Server) createIssue(ctx context.Context, _ *mcp.CallToolRequest, in CreateIssueIn) (*mcp.CallToolResult, CreateIssueOut, error) {
	x, err := s.session()
	if err != nil {
		return nil, CreateIssueOut{}, err
	}
	project, notes, err := s.applyCapture(ctx, &in, x.Project)
	if err != nil {
		return nil, CreateIssueOut{}, err
	}
	in.Title = strings.TrimSpace(in.Title)
	if err := errors.Join(checkIssueText(in.Title, in.Description), checkPriority(in.Priority)); err != nil {
		return nil, CreateIssueOut{}, err
	}
	p := store.Proposal{
		SessionID: x.ID, SessionName: x.Name, Project: project, Title: in.Title,
		Description: in.Description, Priority: in.Priority, Labels: in.Labels,
	}
	if s.d.Plane != nil {
		// Best effort now; accepting resolves it again.
		if pp, err := proposals.PlaneProject(ctx, s.proposalDeps(), project); err == nil {
			p.PlaneProjectID = pp.ID
		}
	}
	p, err = s.d.Store.Proposals().Create(ctx, p)
	if err != nil {
		return nil, CreateIssueOut{}, err
	}
	if err := s.attribute(ctx, p.Ref(), store.LinkMeta{Title: p.Title, State: string(p.Status)}); err != nil {
		return nil, CreateIssueOut{}, err
	}
	if !s.d.Config.MCP.AutoCreateIssues {
		return nil, CreateIssueOut{
			Issue: proposalOut(p),
			Message: fmt.Sprintf("Proposed as %s. The user will accept, edit or drop it on forge's Home screen; "+
				"it is not in Plane yet. Mention %s in your summary.", p.Handle(), p.Handle()) + notes,
		}, nil
	}
	acc, err := proposals.Accept(ctx, s.proposalDeps(), p.ID)
	if err != nil {
		return nil, CreateIssueOut{Issue: proposalOut(p)}, fmt.Errorf("saved as proposal %s, but creating it in Plane failed: %w", p.Handle(), err)
	}
	s.ping(ctx)
	p, _ = s.d.Store.Proposals().Get(ctx, p.ID)
	out := proposalOut(p)
	out.ID = acc.Key
	msg := "Created " + acc.Key + " in Plane."
	if len(acc.UnknownLabels) > 0 {
		msg += " Unknown labels left off: " + strings.Join(acc.UnknownLabels, ", ") + "."
	}
	msg += notes
	return nil, CreateIssueOut{Issue: out, Message: msg}, nil
}

// UpdateIssueIn is update_issue's input.
type UpdateIssueIn struct {
	ID          string  `json:"id" jsonschema:"A proposal id (P-7) or a Plane key (FORGE-12)."`
	Title       *string `json:"title,omitempty" jsonschema:"New title."`
	Description *string `json:"description,omitempty" jsonschema:"New markdown description; replaces the old one."`
	Priority    *string `json:"priority,omitempty" jsonschema:"One of urgent, high, medium, low, none."`
	State       *string `json:"state,omitempty" jsonschema:"Plane state name, e.g. 'In Progress' or 'Done'. Plane issues only."`
}

func (s *Server) updateIssue(ctx context.Context, _ *mcp.CallToolRequest, in UpdateIssueIn) (*mcp.CallToolResult, IssueOut, error) {
	if _, err := s.session(); err != nil {
		return nil, IssueOut{}, err
	}
	if in.Priority != nil {
		if err := checkPriority(*in.Priority); err != nil {
			return nil, IssueOut{}, err
		}
	}
	if in.Title != nil {
		t := strings.TrimSpace(*in.Title)
		in.Title = &t
	}
	if id, ok := store.ParseProposalHandle(in.ID); ok {
		if in.State != nil {
			return nil, IssueOut{}, errors.New("a proposal has no Plane state until the user accepts it")
		}
		title, desc := "x", ""
		if in.Title != nil {
			title = *in.Title
		}
		if in.Description != nil {
			desc = *in.Description
		}
		if err := checkIssueText(title, desc); err != nil {
			return nil, IssueOut{}, err
		}
		p, err := s.d.Store.Proposals().Update(ctx, id, store.ProposalEdit{Title: in.Title, Description: in.Description, Priority: in.Priority})
		if errors.Is(err, store.ErrConflict) {
			cur, _ := s.d.Store.Proposals().Get(ctx, id)
			if cur.PlaneKey != "" {
				return nil, IssueOut{}, fmt.Errorf("%s was accepted as %s; update that key instead", in.ID, cur.PlaneKey)
			}
			return nil, IssueOut{}, fmt.Errorf("%s was %s and can't be changed", in.ID, cur.Status)
		}
		if err != nil {
			return nil, IssueOut{}, err
		}
		if err := s.attribute(ctx, p.Ref(), store.LinkMeta{Title: p.Title, State: string(p.Status)}); err != nil {
			return nil, IssueOut{}, err
		}
		return nil, proposalOut(p), nil
	}

	c, err := s.planeAPI()
	if err != nil {
		return nil, IssueOut{}, err
	}
	w, err := c.WorkItemByIdentifier(ctx, in.ID)
	if err != nil {
		return nil, IssueOut{}, fmt.Errorf("issue %s: %w", in.ID, err)
	}
	var up plane.WorkItemUpdate
	if in.Title != nil {
		if err := checkIssueText(*in.Title, ""); err != nil {
			return nil, IssueOut{}, err
		}
		up.Name = in.Title
	}
	if in.Description != nil {
		html, err := plane.MarkdownToHTML(*in.Description)
		if err != nil {
			return nil, IssueOut{}, err
		}
		up.DescriptionHTML = &html
	}
	if in.Priority != nil {
		p := plane.Priority(*in.Priority)
		up.Priority = &p
	}
	states, err := c.States(ctx, w.ProjectID)
	if err != nil {
		return nil, IssueOut{}, err
	}
	if in.State != nil {
		st, ok := stateByName(states, *in.State)
		if !ok {
			return nil, IssueOut{}, fmt.Errorf("unknown state %q; use one of: %s", *in.State, strings.Join(stateNames(states), ", "))
		}
		up.StateID = &st.ID
	}
	w2, err := c.UpdateWorkItem(ctx, w.ProjectID, w.ID, up)
	if err != nil {
		return nil, IssueOut{}, err
	}
	if w2.Identifier == "" {
		w2.Identifier = w.Key()
	}
	if err := s.attribute(ctx, store.IssueRef(w2.Key()), store.LinkMeta{Title: w2.Name}); err != nil {
		return nil, IssueOut{}, err
	}
	return nil, issueOut(w2, states), nil
}

func stateByName(states []plane.State, name string) (plane.State, bool) {
	for _, st := range states {
		if strings.EqualFold(st.Name, strings.TrimSpace(name)) {
			return st, true
		}
	}
	return plane.State{}, false
}

func stateNames(states []plane.State) []string {
	out := make([]string, len(states))
	for i, st := range states {
		out[i] = st.Name
	}
	return out
}

func issueOut(w plane.WorkItem, states []plane.State) IssueOut {
	o := IssueOut{ID: w.Key(), Key: w.Key(), Title: w.Name, Priority: string(w.Priority)}
	for _, st := range states {
		if st.ID == w.StateID {
			o.Status = st.Name
		}
	}
	if o.Status == "" && w.State != nil {
		o.Status = w.State.Name
	}
	for _, l := range w.Labels {
		o.Labels = append(o.Labels, l.Name)
	}
	return o
}

// CommentIssueIn is comment_issue's input.
type CommentIssueIn struct {
	ID   string `json:"id" jsonschema:"Plane key of the issue, e.g. FORGE-12. Proposals can't take comments."`
	Body string `json:"body" jsonschema:"Markdown comment, e.g. what you found or changed."`
}

// CommentIssueOut is comment_issue's result.
type CommentIssueOut struct {
	CommentID string `json:"comment_id"`
	Message   string `json:"message"`
}

func (s *Server) commentIssue(ctx context.Context, _ *mcp.CallToolRequest, in CommentIssueIn) (*mcp.CallToolResult, CommentIssueOut, error) {
	x, err := s.session()
	if err != nil {
		return nil, CommentIssueOut{}, err
	}
	if _, ok := store.ParseProposalHandle(in.ID); ok {
		return nil, CommentIssueOut{}, fmt.Errorf("%s is still a proposal; use update_issue to change its description until the user accepts it", in.ID)
	}
	if strings.TrimSpace(in.Body) == "" {
		return nil, CommentIssueOut{}, errors.New("body is required")
	}
	if len(in.Body) > maxDescription {
		return nil, CommentIssueOut{}, errors.New("body is too long")
	}
	c, err := s.planeAPI()
	if err != nil {
		return nil, CommentIssueOut{}, err
	}
	w, err := c.WorkItemByIdentifier(ctx, in.ID)
	if err != nil {
		return nil, CommentIssueOut{}, fmt.Errorf("issue %s: %w", in.ID, err)
	}
	html, err := plane.MarkdownToHTML(strings.TrimRight(in.Body, "\n") + "\n\n_— forge session " + x.Name + "_\n")
	if err != nil {
		return nil, CommentIssueOut{}, err
	}
	cm, err := c.CreateComment(ctx, w.ProjectID, w.ID, plane.CommentCreate{CommentHTML: html})
	if err != nil {
		return nil, CommentIssueOut{}, err
	}
	if err := s.attribute(ctx, store.IssueRef(w.Key()), store.LinkMeta{Title: w.Name}); err != nil {
		return nil, CommentIssueOut{}, err
	}
	return nil, CommentIssueOut{CommentID: cm.ID, Message: "Commented on " + w.Key() + "."}, nil
}

// ListIssuesIn is list_issues' input.
type ListIssuesIn struct {
	StateGroup      string `json:"state_group,omitempty" jsonschema:"Only issues in this state group: backlog, unstarted, started, completed, cancelled or triage."`
	Search          string `json:"search,omitempty" jsonschema:"Only issues whose title contains this text."`
	AssignedToMe    bool   `json:"assigned_to_me,omitempty" jsonschema:"Only issues assigned to the forge user."`
	ExcludeProposed bool   `json:"exclude_proposed,omitempty" jsonschema:"Leave out proposals still waiting for the user. They are included by default, so you don't file duplicates."`
	Limit           int    `json:"limit,omitempty" jsonschema:"At most this many Plane issues (default 25, max 100)."`
}

// ListIssuesOut is list_issues' result.
type ListIssuesOut struct {
	Issues  []IssueOut `json:"issues"`
	Warning string     `json:"warning,omitempty"`
}

var stateGroups = []string{"backlog", "unstarted", "started", "completed", "cancelled", "triage"}

func (s *Server) listIssues(ctx context.Context, _ *mcp.CallToolRequest, in ListIssuesIn) (*mcp.CallToolResult, ListIssuesOut, error) {
	if in.StateGroup != "" && !slices.Contains(stateGroups, in.StateGroup) {
		return nil, ListIssuesOut{}, fmt.Errorf("state_group %q: use one of %s", in.StateGroup, strings.Join(stateGroups, ", "))
	}
	out := ListIssuesOut{Issues: []IssueOut{}}
	if !in.ExcludeProposed && s.projectName() != "" {
		ps, err := s.d.Store.Proposals().List(ctx, store.ProposalFilter{Status: store.ProposalProposed, Project: s.projectName()})
		if err != nil {
			return nil, ListIssuesOut{}, err
		}
		for _, p := range ps {
			if in.Search == "" || strings.Contains(strings.ToLower(p.Title), strings.ToLower(in.Search)) {
				out.Issues = append(out.Issues, proposalOut(p))
			}
		}
	}
	c, err := s.planeAPI()
	if err != nil {
		out.Warning = err.Error()
		return nil, out, nil
	}
	if s.projectName() == "" {
		out.Warning = "not inside a forge session, so there is no project to list Plane issues for"
		return nil, out, nil
	}
	pp, err := proposals.PlaneProject(ctx, s.proposalDeps(), s.projectName())
	if err != nil {
		out.Warning = err.Error()
		return nil, out, nil
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 25
	}
	limit = min(limit, maxListLimit)
	f := plane.WorkItemFilter{Search: in.Search, Limit: limit}
	if in.StateGroup != "" {
		f.StateGroups = []plane.StateGroup{plane.StateGroup(in.StateGroup)}
	}
	if in.AssignedToMe {
		me, err := c.Me(ctx)
		if err != nil {
			return nil, ListIssuesOut{}, err
		}
		f.AssigneeIDs = []string{me.ID}
	}
	items, err := c.WorkItems(ctx, pp.ID, f)
	if err != nil {
		return nil, ListIssuesOut{}, err
	}
	states, err := c.States(ctx, pp.ID)
	if err != nil {
		return nil, ListIssuesOut{}, err
	}
	for _, w := range items {
		if w.Identifier == "" && pp.Identifier != "" && w.SequenceID > 0 {
			w.Identifier = fmt.Sprintf("%s-%d", pp.Identifier, w.SequenceID)
		}
		out.Issues = append(out.Issues, issueOut(w, states))
	}
	return nil, out, nil
}

// GetIssueIn is get_issue's input.
type GetIssueIn struct {
	ID string `json:"id" jsonschema:"A proposal id (P-7) or a Plane key (FORGE-12)."`
}

// CommentOut is a Plane comment.
type CommentOut struct {
	Body    string `json:"body" jsonschema:"Markdown."`
	Created string `json:"created,omitempty"`
}

// GetIssueOut is get_issue's result.
type GetIssueOut struct {
	Issue    IssueOut     `json:"issue"`
	Comments []CommentOut `json:"comments,omitempty"`
}

func (s *Server) getIssue(ctx context.Context, _ *mcp.CallToolRequest, in GetIssueIn) (*mcp.CallToolResult, GetIssueOut, error) {
	if id, ok := store.ParseProposalHandle(in.ID); ok {
		p, err := s.d.Store.Proposals().Get(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return nil, GetIssueOut{}, fmt.Errorf("no proposal %s", in.ID)
		}
		if err != nil {
			return nil, GetIssueOut{}, err
		}
		o := proposalOut(p)
		o.Description = p.Description
		return nil, GetIssueOut{Issue: o}, nil
	}
	c, err := s.planeAPI()
	if err != nil {
		return nil, GetIssueOut{}, err
	}
	w, err := c.WorkItemByIdentifier(ctx, in.ID)
	if err != nil {
		return nil, GetIssueOut{}, fmt.Errorf("issue %s: %w", in.ID, err)
	}
	if w.Identifier == "" {
		w.Identifier = in.ID
	}
	states, err := c.States(ctx, w.ProjectID)
	if err != nil {
		return nil, GetIssueOut{}, err
	}
	o := issueOut(w, states)
	o.Description = plane.HTMLToMarkdown(w.DescriptionHTML)
	out := GetIssueOut{Issue: o}
	cs, err := c.Comments(ctx, w.ProjectID, w.ID)
	if err != nil {
		return nil, GetIssueOut{}, err
	}
	for _, cm := range cs {
		co := CommentOut{Body: plane.HTMLToMarkdown(cm.CommentHTML)}
		if !cm.CreatedAt.IsZero() {
			co.Created = cm.CreatedAt.Format("2006-01-02 15:04")
		}
		out.Comments = append(out.Comments, co)
	}
	return nil, out, nil
}

func (s *Server) addPlaneTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "create_issue",
		Description: "File a follow-up issue for work you are not doing now: a bug you noticed, missing tests, a refactor for later. " +
			"By default it becomes a proposal (id P-<n>) on the user's forge Home screen, where they accept it into Plane, edit or drop it. " +
			"It is linked to this session, its branch and PR. Call list_issues first to avoid duplicates.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false), IdempotentHint: false},
	}, s.createIssue)
	mcp.AddTool(srv, &mcp.Tool{
		Name: "update_issue",
		Description: "Change an issue's title, description, priority or (Plane issues only) state. " +
			"Works on your proposals (P-<n>) while they wait for the user, and on Plane issues by key (FORGE-12), e.g. to move one to 'In Progress'.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false), IdempotentHint: true},
	}, s.updateIssue)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "comment_issue",
		Description: "Add a markdown comment to a Plane issue (by key, e.g. FORGE-12): findings, progress, or what you changed. Signed with this session's name.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false)},
	}, s.commentIssue)
	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_issues",
		Description: "List this project's Plane issues and the proposals still waiting for the user. " +
			"Filter by state group, title text or assignment. Use it before create_issue to avoid duplicates.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.listIssues)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_issue",
		Description: "Read one issue with its markdown description and comments: a proposal (P-7) or a Plane issue (FORGE-12).",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.getIssue)
}

func ptr[T any](v T) *T { return &v }
