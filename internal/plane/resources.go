package plane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Narrow interfaces for callers that only need part of the client; API is
// all of it. Tabs and commands depend on these so tests can fake them.
type (
	// ProjectReader lists projects and their metadata.
	ProjectReader interface {
		Projects(ctx context.Context) ([]Project, error)
		Project(ctx context.Context, id string) (Project, error)
		States(ctx context.Context, projectID string) ([]State, error)
		Labels(ctx context.Context, projectID string) ([]Label, error)
		Cycles(ctx context.Context, projectID string) ([]Cycle, error)
		CurrentCycle(ctx context.Context, projectID string) (Cycle, bool, error)
		ProjectMembers(ctx context.Context, projectID string) ([]Member, error)
		WorkspaceMembers(ctx context.Context) ([]Member, error)
	}
	// WorkItemLister lists and reads work items.
	WorkItemLister interface {
		WorkItems(ctx context.Context, projectID string, f WorkItemFilter) ([]WorkItem, error)
		CachedWorkItems(ctx context.Context, projectID string, f WorkItemFilter) (Cached[WorkItem], bool)
		WorkItem(ctx context.Context, projectID, id string) (WorkItem, error)
		WorkItemByIdentifier(ctx context.Context, identifier string) (WorkItem, error)
	}
	// WorkItemWriter creates and changes work items.
	WorkItemWriter interface {
		CreateWorkItem(ctx context.Context, projectID string, in WorkItemCreate) (WorkItem, error)
		UpdateWorkItem(ctx context.Context, projectID, id string, in WorkItemUpdate) (WorkItem, error)
	}
	// Commenter reads and writes work item comments.
	Commenter interface {
		Comments(ctx context.Context, projectID, workItemID string) ([]Comment, error)
		CreateComment(ctx context.Context, projectID, workItemID string, in CommentCreate) (Comment, error)
	}
	// API is the whole client.
	API interface {
		Me(ctx context.Context) (User, error)
		ProjectReader
		WorkItemLister
		WorkItemWriter
		Commenter
	}
)

var _ API = (*Client)(nil)

// CacheReader reads the last fetched lists back from the cache without
// the network. It is optional (the Client has it, fakes needn't): quick
// capture (P3.5) completes from it while fresh data loads.
type CacheReader interface {
	CachedMe(ctx context.Context) (User, bool)
	CachedProjects(ctx context.Context) (Cached[Project], bool)
	CachedLabels(ctx context.Context, projectID string) (Cached[Label], bool)
	CachedProjectMembers(ctx context.Context, projectID string) (Cached[Member], bool)
}

var _ CacheReader = (*Client)(nil)

// Me returns the API key's user. It doubles as the connectivity check: a
// 404 means the instance has no v2 API (ErrNoV2).
func (c *Client) Me(ctx context.Context) (User, error) {
	var u User
	err := c.do(ctx, http.MethodGet, "users/me", nil, nil, &u)
	if IsNotFound(err) {
		return User{}, fmt.Errorf("%w: %w", ErrNoV2, err)
	}
	if err == nil && c.cache != nil {
		if raw, err := json.Marshal([]User{u}); err == nil {
			_ = c.cache.Put(ctx, c.cacheKey("me", "", nil, 0), CacheKind, "", raw, c.cacheTTL)
		}
	}
	return u, err
}

// CachedMe returns the last Me result from the cache.
func (c *Client) CachedMe(ctx context.Context) (User, bool) {
	got, ok := readCached[User](ctx, c, "me", "", nil, 0)
	if !ok || len(got.Items) != 1 {
		return User{}, false
	}
	return got.Items[0], true
}

// Projects lists the workspace's projects.
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	return cachedList[Project](ctx, c, "projects", "", c.wsPath("projects"), nil, 0)
}

// CachedProjects returns the last Projects result from the cache.
func (c *Client) CachedProjects(ctx context.Context) (Cached[Project], bool) {
	return readCached[Project](ctx, c, "projects", "", nil, 0)
}

// Project returns one project.
func (c *Client) Project(ctx context.Context, id string) (Project, error) {
	var p Project
	return p, c.do(ctx, http.MethodGet, c.wsPath("projects", id), nil, nil, &p)
}

// States lists a project's states.
func (c *Client) States(ctx context.Context, projectID string) ([]State, error) {
	return cachedList[State](ctx, c, "states", projectID, c.wsPath("projects", projectID, "states"), nil, 0)
}

// labelsQuery orders labels as Plane does by default (sort_order), which
// Plane can't page with a cursor: asking for it explicitly makes listAll
// use offset paging instead of failing with a 400.
func labelsQuery() url.Values { return url.Values{"order_by": {"sort_order"}} }

// Labels lists a project's labels.
func (c *Client) Labels(ctx context.Context, projectID string) ([]Label, error) {
	return cachedList[Label](ctx, c, "labels", projectID, c.wsPath("projects", projectID, "labels"), labelsQuery(), 0)
}

// CachedLabels returns the last Labels result from the cache.
func (c *Client) CachedLabels(ctx context.Context, projectID string) (Cached[Label], bool) {
	return readCached[Label](ctx, c, "labels", projectID, labelsQuery(), 0)
}

// Cycles lists a project's cycles.
func (c *Client) Cycles(ctx context.Context, projectID string) ([]Cycle, error) {
	return cachedList[Cycle](ctx, c, "cycles", projectID, c.wsPath("projects", projectID, "cycles"), nil, 0)
}

// CurrentCycle returns the cycle running now; ok is false if none is. v2
// has no "current" filter, so it is picked from Cycles by date.
func (c *Client) CurrentCycle(ctx context.Context, projectID string) (Cycle, bool, error) {
	cycles, err := c.Cycles(ctx, projectID)
	if err != nil {
		return Cycle{}, false, err
	}
	now := c.now()
	for _, cy := range cycles {
		if cy.IsCurrent(now) {
			return cy, true, nil
		}
	}
	return Cycle{}, false, nil
}

// ProjectMembers lists a project's members, with the user expanded.
func (c *Client) ProjectMembers(ctx context.Context, projectID string) ([]Member, error) {
	q := url.Values{"expand": {"member"}}
	return cachedList[Member](ctx, c, "project_members", projectID, c.wsPath("projects", projectID, "members"), q, 0)
}

// CachedProjectMembers returns the last ProjectMembers result from the
// cache.
func (c *Client) CachedProjectMembers(ctx context.Context, projectID string) (Cached[Member], bool) {
	return readCached[Member](ctx, c, "project_members", projectID, url.Values{"expand": {"member"}}, 0)
}

// WorkspaceMembers lists the workspace's members, with the user expanded.
func (c *Client) WorkspaceMembers(ctx context.Context) ([]Member, error) {
	q := url.Values{"expand": {"member"}}
	return cachedList[Member](ctx, c, "members", "", c.wsPath("members"), q, 0)
}

// WorkItems lists a project's work items matching f, following pages.
func (c *Client) WorkItems(ctx context.Context, projectID string, f WorkItemFilter) ([]WorkItem, error) {
	return cachedList[WorkItem](ctx, c, "work_items", projectID, c.wsPath("projects", projectID, "work-items"), f.encode(), f.Limit)
}

// CachedWorkItems returns the last WorkItems result for the same project
// and filter from the cache, so a view can render before refreshing.
func (c *Client) CachedWorkItems(ctx context.Context, projectID string, f WorkItemFilter) (Cached[WorkItem], bool) {
	return readCached[WorkItem](ctx, c, "work_items", projectID, f.encode(), f.Limit)
}

// WorkItem returns one work item by UUID (or identifier) within a project.
func (c *Client) WorkItem(ctx context.Context, projectID, id string) (WorkItem, error) {
	var w WorkItem
	return w, c.do(ctx, http.MethodGet, c.wsPath("projects", projectID, "work-items", id), nil, nil, &w)
}

// WorkItemByIdentifier returns a work item by its key, e.g. PROJ-12.
// Archived items are not found.
func (c *Client) WorkItemByIdentifier(ctx context.Context, identifier string) (WorkItem, error) {
	identifier = strings.TrimSpace(identifier)
	if !strings.Contains(identifier, "-") {
		return WorkItem{}, fmt.Errorf("plane: %q is not a work item key like PROJ-12", identifier)
	}
	var w WorkItem
	return w, c.do(ctx, http.MethodGet, c.wsPath("work-items", identifier), nil, nil, &w)
}

// CreateWorkItem creates a work item in a project.
func (c *Client) CreateWorkItem(ctx context.Context, projectID string, in WorkItemCreate) (WorkItem, error) {
	if strings.TrimSpace(in.Name) == "" {
		return WorkItem{}, errors.New("plane: a work item needs a name")
	}
	var w WorkItem
	return w, c.do(ctx, http.MethodPost, c.wsPath("projects", projectID, "work-items"), nil, in, &w)
}

// UpdateWorkItem changes the non-nil fields of in.
func (c *Client) UpdateWorkItem(ctx context.Context, projectID, id string, in WorkItemUpdate) (WorkItem, error) {
	var w WorkItem
	return w, c.do(ctx, http.MethodPatch, c.wsPath("projects", projectID, "work-items", id), nil, in, &w)
}

// Comments lists a work item's comments.
func (c *Client) Comments(ctx context.Context, projectID, workItemID string) ([]Comment, error) {
	return listAll[Comment](ctx, c, c.wsPath("projects", projectID, "work-items", workItemID, "comments"), nil, 0)
}

// CreateComment adds a comment to a work item.
func (c *Client) CreateComment(ctx context.Context, projectID, workItemID string, in CommentCreate) (Comment, error) {
	if strings.TrimSpace(in.CommentHTML) == "" {
		return Comment{}, errors.New("plane: empty comment")
	}
	var cm Comment
	return cm, c.do(ctx, http.MethodPost, c.wsPath("projects", projectID, "work-items", workItemID, "comments"), nil, in, &cm)
}

// IssueContext resolves {issue_title} and {issue_body} for a work item key
// (templates.ContextProvider for plane_issue links).
func (c *Client) IssueContext(ctx context.Context, ref string) (map[string]string, error) {
	w, err := c.WorkItemByIdentifier(ctx, ref)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"issue_title": w.Name,
		"issue_body":  HTMLToText(w.DescriptionHTML),
	}, nil
}
