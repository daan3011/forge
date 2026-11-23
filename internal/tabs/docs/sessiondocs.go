package docs

import (
	"context"
	"os"
	"path/filepath"
	"strconv"

	"github.com/daan3011/forge/internal/config"
	docfs "github.com/daan3011/forge/internal/docs"
	"github.com/daan3011/forge/internal/projects"
	"github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
)

// fromSessions is the label of the read-only group of docs agents wrote in
// their worktrees that the main checkout lacks (P6.5).
const fromSessions = "From sessions"

// sessionsKey is the synthetic path of a root's "From sessions" group,
// used for its open / closed state and selection.
func sessionsKey(rootPath string) string { return "forge-sessions:" + rootPath }

// sessionDoc is what the tree shows about a doc from a session.
type sessionDoc struct {
	session string
	status  docfs.DocStatus
	agent   bool // written through forge mcp (linked to the session)
}

// sessionScan is one root's "From sessions" group: nil when no live
// session has docs the main checkout lacks.
type sessionScan struct {
	group *docfs.Node
	info  map[string]sessionDoc // by doc path
}

// scanSessionDocs builds the "From sessions" group for each root: per live
// session of the root's project, the docs in its worktree's .forge/docs
// that are new or changed against the root. Docs the session wrote through
// forge mcp (session → doc links) are marked. Failures for one session
// skip it.
func scanSessionDocs(ctx context.Context, svc sessions.API, links tabs.LinkReader, roots []root) []sessionScan {
	out := make([]sessionScan, len(roots))
	if svc == nil {
		return out
	}
	rows, err := svc.List(ctx, "", false)
	if err != nil {
		return out
	}
	for i, r := range roots {
		var kids []*docfs.Node
		info := map[string]sessionDoc{}
		for _, x := range rows {
			if x.Project != r.name || !live(x.Session) || x.Worktree == "" || x.WorktreeExternal ||
				(r.repo != "" && projects.SamePath(x.Worktree, r.repo)) {
				continue
			}
			if fi, err := os.Stat(x.Worktree); err != nil || !fi.IsDir() {
				continue
			}
			wtRoot := filepath.Join(x.Worktree, filepath.FromSlash(config.DefaultDocsDir))
			docs, err := docfs.Diverged(r.path, wtRoot)
			if err != nil || len(docs) == 0 {
				continue
			}
			agent := agentDocs(ctx, links, x.ID)
			s := &docfs.Node{Name: x.Name, Path: wtRoot, IsDir: true}
			for _, d := range docs {
				s.Children = append(s.Children, &docfs.Node{Name: d.Rel, Path: d.Path, Rel: d.Rel, ModTime: d.ModTime})
				info[d.Path] = sessionDoc{session: x.Name, status: d.Status, agent: agent[d.Path]}
			}
			kids = append(kids, s)
		}
		if len(kids) > 0 {
			out[i] = sessionScan{
				group: &docfs.Node{Name: fromSessions, Path: sessionsKey(r.path), IsDir: true, Children: kids},
				info:  info,
			}
		}
	}
	return out
}

// live reports whether a session still has a running or resumable agent.
func live(x store.Session) bool {
	return x.Status != store.StatusDead && x.Status != store.StatusArchived && !x.Archived()
}

// agentDocs are the doc paths the session wrote through forge mcp.
func agentDocs(ctx context.Context, links tabs.LinkReader, id int64) map[string]bool {
	out := map[string]bool{}
	if links == nil {
		return out
	}
	ls, err := links.For(ctx, store.Ref{Kind: store.KindSession, ID: strconv.FormatInt(id, 10)})
	if err != nil {
		return out
	}
	for _, l := range ls {
		if l.Other.Kind == store.KindDoc {
			out[l.Other.ID] = true
		}
	}
	return out
}
