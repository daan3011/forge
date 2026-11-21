package prs

import (
	"context"
	"errors"
	"fmt"
	"html"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
)

// PR -> Plane work item back-links (P3.7): after a fetch, PRs whose branch
// or title names a work item key are linked to it, and optionally the item
// gets a comment with the PR link the first time.

const backlinkTimeout = 30 * time.Second

// backlinkState serialises back-link runs, so two sections fetched at once
// never both see a link as new (and comment twice).
type backlinkState struct {
	running   bool
	pending   []github.PR
	ids       []string // Plane project identifiers, once loaded
	errShown  bool     // a failure was toasted this run of forge
	commented int      // comments posted (tests)
}

type backlinkedMsg struct {
	ids       []string
	added     []sessions.IssuePR
	commented int
	err       error
}

// TargetTab implements tabs.TargetedMsg.
func (backlinkedMsg) TargetTab() string { return Title }

var _ tabs.TargetedMsg = backlinkedMsg{}

// backlink queues prs for linking and starts a run unless one is going.
func (t *Tab) backlink(prs []github.PR) tea.Cmd {
	if t.sessions == nil || t.plane == nil || len(prs) == 0 {
		return nil
	}
	t.bl.pending = append(t.bl.pending, prs...)
	return t.startBacklink()
}

func (t *Tab) startBacklink() tea.Cmd {
	if t.bl.running || len(t.bl.pending) == 0 {
		return nil
	}
	t.bl.running = true
	prs := t.bl.pending
	t.bl.pending = nil
	api, svc, ids := t.plane, t.sessions, t.bl.ids
	comment := t.cfg.Plane.CommentOnPRLink
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), backlinkTimeout)
		defer cancel()
		var msg backlinkedMsg
		if len(ids) == 0 {
			var err error
			if ids, err = projectIdentifiers(ctx, api); err != nil {
				msg.err = fmt.Errorf("plane projects: %w", err)
				return msg
			}
		}
		msg.ids = ids
		var found []sessions.IssuePR
		for _, p := range prs {
			key, ok := plane.FindKey(ids, p.HeadRef, p.Title)
			if !ok {
				continue
			}
			found = append(found, sessions.IssuePR{
				Key: key, PR: store.PRRef(p.Repo, p.Number),
				Meta: store.LinkMeta{URL: p.URL, Title: p.Title, State: p.State},
			})
		}
		if len(found) == 0 {
			return msg
		}
		added, err := svc.LinkIssuePRs(ctx, found)
		msg.added = added
		errs := []error{err}
		if comment {
			for _, l := range added {
				if l.Meta.State != github.StateOpen && l.Meta.State != github.StateDraft {
					continue // don't comment on old PRs when the option is turned on
				}
				if err := commentPRLink(ctx, api, l); err != nil {
					errs = append(errs, fmt.Errorf("comment on %s: %w", l.Key, err))
					continue
				}
				msg.commented++
			}
		}
		msg.err = errors.Join(errs...)
		return msg
	}
}

func (t *Tab) backlinked(msg backlinkedMsg) tea.Cmd {
	t.bl.running = false
	if len(msg.ids) > 0 {
		t.bl.ids = msg.ids
	}
	t.bl.commented += msg.commented
	cmds := []tea.Cmd{t.startBacklink()}
	if len(msg.added) > 0 {
		if p, ok := t.selected(); ok {
			cmds = append(cmds, t.loadLinks(p.ID()))
		}
	}
	if msg.err != nil && !t.bl.errShown {
		// Once per run: a missing Plane key would otherwise toast every fetch.
		t.bl.errShown = true
		cmds = append(cmds, errCmd(fmt.Errorf("linking PRs to Plane items: %w", msg.err)))
	}
	return tea.Batch(cmds...)
}

// projectIdentifiers are the workspace's Plane project identifiers
// ("PROJ"), from the cache when it has them.
func projectIdentifiers(ctx context.Context, api plane.API) ([]string, error) {
	var ps []plane.Project
	if cr, ok := api.(plane.CacheReader); ok {
		if c, ok := cr.CachedProjects(ctx); ok && !c.Stale {
			ps = c.Items
		}
	}
	if ps == nil {
		var err error
		if ps, err = api.Projects(ctx); err != nil {
			return nil, err
		}
	}
	ids := make([]string, 0, len(ps))
	for _, p := range ps {
		if p.Identifier != "" {
			ids = append(ids, p.Identifier)
		}
	}
	return ids, nil
}

// commentPRLink posts "Pull request owner/repo#12: title" on the item.
func commentPRLink(ctx context.Context, api plane.API, l sessions.IssuePR) error {
	w, err := api.WorkItemByIdentifier(ctx, l.Key)
	if err != nil {
		return err
	}
	link := html.EscapeString(l.PR.ID)
	if l.Meta.URL != "" {
		link = `<a href="` + html.EscapeString(l.Meta.URL) + `">` + link + `</a>`
	}
	body := "<p>Pull request " + link
	if l.Meta.Title != "" {
		body += ": " + html.EscapeString(l.Meta.Title)
	}
	body += "</p>"
	_, err = api.CreateComment(ctx, w.ProjectID, w.ID, plane.CommentCreate{CommentHTML: body})
	return err
}
