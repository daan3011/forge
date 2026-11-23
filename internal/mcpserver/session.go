package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/packs"
	"github.com/daan3011/forge/internal/store"
)

const (
	maxSummary  = 4 << 10
	maxProgress = 500
)

// SetSummaryIn is set_session_summary's input.
type SetSummaryIn struct {
	Summary string `json:"summary" jsonschema:"A few sentences of markdown: what this session changed, what's left open. Shown in forge and used for standups."`
}

// SessionToolOut is the result of the session tools.
type SessionToolOut struct {
	Message string `json:"message"`
}

func (s *Server) setSummary(ctx context.Context, _ *mcp.CallToolRequest, in SetSummaryIn) (*mcp.CallToolResult, SessionToolOut, error) {
	x, err := s.session()
	if err != nil {
		return nil, SessionToolOut{}, err
	}
	sum := strings.TrimSpace(in.Summary)
	switch {
	case sum == "":
		return nil, SessionToolOut{}, errors.New("summary is required")
	case len(sum) > maxSummary:
		return nil, SessionToolOut{}, fmt.Errorf("summary is %d bytes; keep it under %d", len(sum), maxSummary)
	}
	if err := s.d.Store.Sessions().SetAgentSummary(ctx, x.ID, sum); err != nil {
		return nil, SessionToolOut{}, err
	}
	x.AgentSummary, x.Summary = sum, sum
	s.ping(ctx)
	return nil, SessionToolOut{Message: "Summary saved for session " + x.Name + "."}, nil
}

// ReportProgressIn is report_progress' input.
type ReportProgressIn struct {
	Message string `json:"message" jsonschema:"One line on where you are, e.g. 'tests written, 2 of 5 passing'. Shown next to the session in forge."`
}

func (s *Server) reportProgress(ctx context.Context, _ *mcp.CallToolRequest, in ReportProgressIn) (*mcp.CallToolResult, SessionToolOut, error) {
	x, err := s.session()
	if err != nil {
		return nil, SessionToolOut{}, err
	}
	msg := strings.Join(strings.Fields(in.Message), " ")
	switch {
	case msg == "":
		return nil, SessionToolOut{}, errors.New("message is required")
	case len(msg) > maxProgress:
		return nil, SessionToolOut{}, fmt.Errorf("message is %d characters; keep it to one line under %d", len(msg), maxProgress)
	}
	now := s.d.Now()
	if err := s.d.Store.Sessions().SetProgress(ctx, x.ID, msg, now); err != nil {
		return nil, SessionToolOut{}, err
	}
	x.Progress, x.ProgressAt = msg, now
	s.ping(ctx)
	return nil, SessionToolOut{Message: "Progress noted."}, nil
}

// ContextPackIn is get_context_pack's input.
type ContextPackIn struct {
	Name string `json:"name,omitempty" jsonschema:"A context pack to expand (see packs in the result without a name). Leave empty for the session context."`
}

// LinkOut is something linked to the session.
type LinkOut struct {
	Kind  string `json:"kind" jsonschema:"github_pr, plane_issue, proposed_issue, doc or artifact."`
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	State string `json:"state,omitempty"`
	URL   string `json:"url,omitempty"`
}

// PackOut is an expanded context pack.
type PackOut struct {
	Name     string   `json:"name"`
	Preamble string   `json:"preamble,omitempty" jsonschema:"Instructions that come with the pack."`
	Files    []string `json:"files" jsonschema:"Absolute paths to read."`
	Missing  []string `json:"missing,omitempty" jsonschema:"Pack entries that matched no file."`
}

// ContextPackOut is get_context_pack's result.
type ContextPackOut struct {
	Session    string    `json:"session,omitempty"`
	Project    string    `json:"project,omitempty"`
	Repo       string    `json:"repo,omitempty"`
	Branch     string    `json:"branch,omitempty"`
	BaseBranch string    `json:"base_branch,omitempty"`
	Worktree   string    `json:"worktree,omitempty"`
	DocsRoot   string    `json:"docs_root,omitempty"`
	Summary    string    `json:"summary,omitempty"`
	Progress   string    `json:"progress,omitempty"`
	Links      []LinkOut `json:"links,omitempty" jsonschema:"PRs, issues, proposals, docs and artifacts linked to this session."`
	Packs      []string  `json:"packs,omitempty" jsonschema:"Context packs that can be expanded by name."`
	Pack       *PackOut  `json:"pack,omitempty"`
	Note       string    `json:"note,omitempty"`
}

func (s *Server) contextPack(ctx context.Context, _ *mcp.CallToolRequest, in ContextPackIn) (*mcp.CallToolResult, ContextPackOut, error) {
	var out ContextPackOut
	if x := s.sess; x != nil {
		out = ContextPackOut{
			Session: x.Name, Project: x.Project, Repo: x.Repo, Branch: x.Branch,
			BaseBranch: x.BaseBranch, Worktree: x.Worktree, Summary: x.Summary, Progress: x.Progress,
		}
		if root, err := s.docsRoot(); err == nil {
			out.DocsRoot = root
		}
		links, err := s.d.Store.Links().For(ctx, store.SessionRef(x.ID))
		if err != nil {
			return nil, ContextPackOut{}, err
		}
		for _, l := range links {
			out.Links = append(out.Links, LinkOut{Kind: string(l.Other.Kind), ID: linkID(l.Other), Title: l.Title, State: l.State, URL: l.URL})
		}
	} else {
		out.Note = ErrNoSession.Error()
	}
	var all []packs.Pack
	if s.d.PacksDir != "" {
		all, _ = packs.Load(s.d.PacksDir) // broken packs are skipped
	}
	for _, p := range all {
		out.Packs = append(out.Packs, p.Name)
	}
	if in.Name == "" {
		return nil, out, nil
	}
	p, ok := packs.Find(all, in.Name)
	if !ok {
		return nil, ContextPackOut{}, fmt.Errorf("no context pack %q; available: %s", in.Name, strings.Join(out.Packs, ", "))
	}
	repo := out.Worktree
	if repo == "" && s.hasProj {
		repo = s.project.Path
	}
	docsRoot := out.DocsRoot
	if docsRoot == "" && repo != "" {
		docsRoot = filepath.Join(repo, filepath.FromSlash(config.DefaultDocsDir))
	}
	r := packs.Expand(p, repo, docsRoot)
	out.Pack = &PackOut{Name: r.Name, Preamble: r.Preamble, Files: r.Paths, Missing: r.Missing}
	if out.Pack.Files == nil {
		out.Pack.Files = []string{}
	}
	return nil, out, nil
}

// linkID shows proposals as P-<n>.
func linkID(r store.Ref) string {
	if r.Kind == store.KindProposal {
		var id int64
		if _, err := fmt.Sscan(r.ID, &id); err == nil {
			return store.ProposalHandle(id)
		}
	}
	return r.ID
}

func (s *Server) addSessionTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "set_session_summary",
		Description: "Save a short summary of what this session did and what's left open. Call it before you finish; " +
			"forge shows it with the session, keeps it in the archive and uses it for the user's standup.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false), IdempotentHint: true},
	}, s.setSummary)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "report_progress",
		Description: "Report one line of progress at milestones (plan made, tests passing, PR ready). forge shows the latest line next to the session.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false), IdempotentHint: true},
	}, s.reportProgress)
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_context_pack",
		Description: "Get this session's context: project, branch, worktree, docs folder, linked PRs and issues, summary and progress, " +
			"plus the user's context packs. Pass a pack name to get its preamble and the files it asks you to read. Call it when you start.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.contextPack)
}
