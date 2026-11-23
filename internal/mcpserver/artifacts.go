package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/daan3011/forge/internal/artifacts"
	"github.com/daan3011/forge/internal/store"
)

// PinArtifactIn is pin_artifact's input.
type PinArtifactIn struct {
	Path  string   `json:"path" jsonschema:"File to pin: relative to the session worktree (e.g. 'out/report.html') or absolute inside it."`
	Title string   `json:"title,omitempty" jsonschema:"Display title in forge, e.g. 'Benchmark: retries vs none'."`
	Tags  []string `json:"tags,omitempty" jsonschema:"Tags to find it by, e.g. ['benchmark']."`
}

// ArtifactOut describes an artifact.
type ArtifactOut struct {
	Path    string   `json:"path"`
	Title   string   `json:"title,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Type    string   `json:"type,omitempty"`
	Pinned  bool     `json:"pinned"`
	Session string   `json:"session,omitempty"`
	Message string   `json:"message,omitempty"`
}

func (s *Server) pinArtifact(ctx context.Context, _ *mcp.CallToolRequest, in PinArtifactIn) (*mcp.CallToolResult, ArtifactOut, error) {
	x, err := s.session()
	if err != nil {
		return nil, ArtifactOut{}, err
	}
	wt, err := s.worktree()
	if err != nil {
		return nil, ArtifactOut{}, err
	}
	src := in.Path
	if !filepath.IsAbs(src) {
		src = filepath.Join(wt, filepath.FromSlash(src))
	}
	src = filepath.Clean(src)
	if !within(wt, src) {
		return nil, ArtifactOut{}, fmt.Errorf("%s is outside this session's worktree; only files in it can be pinned", in.Path)
	}
	fi, err := os.Stat(src)
	if err != nil {
		return nil, ArtifactOut{}, fmt.Errorf("pin %s: %w", in.Path, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, ArtifactOut{}, fmt.Errorf("%s is not a regular file", in.Path)
	}
	// A worktree file is kept in the project checkout's .forge/artifacts so
	// it outlives the worktree, and synced from the original (like the
	// Artifacts tab's pin).
	dest, msg := src, "pinned in place"
	checkout := ""
	if s.hasProj {
		checkout = s.project.Path
	}
	if checkout != "" && !artifacts.IsKept(src) {
		d, _, err := artifacts.Keep(src, x.Name, checkout)
		switch {
		case errors.Is(err, artifacts.ErrAlreadyKept):
		case err != nil:
			return nil, ArtifactOut{}, fmt.Errorf("pin: keep a copy: %w", err)
		default:
			dest, msg = d, "pinned; kept a copy in the project's .forge/artifacts"
		}
	}
	info, err := os.Stat(dest)
	if err != nil {
		return nil, ArtifactOut{}, err
	}
	idx := s.d.Store.Artifacts()
	a := store.Artifact{
		Path: dest, Project: x.Project, Type: artifacts.Type(dest), Size: info.Size(),
		CreatedAt: info.ModTime(), ModifiedAt: info.ModTime(),
	}
	if dest == src {
		a.SessionID = x.ID
	}
	err = idx.Sync(ctx, nil, []store.Artifact{a})
	if err == nil && dest != src {
		err = idx.Link(ctx, store.ArtifactLink{CopyPath: dest, SourcePath: src, SessionID: x.ID, SessionName: x.Name, SyncedAt: s.d.Now()})
	}
	if err == nil && in.Title != "" {
		err = idx.SetTitle(ctx, dest, strings.TrimSpace(in.Title))
	}
	if err == nil && len(in.Tags) > 0 {
		err = idx.SetTags(ctx, dest, in.Tags)
	}
	if err == nil {
		err = idx.SetPinned(ctx, dest, x.Project, true)
	}
	if err != nil {
		return nil, ArtifactOut{}, fmt.Errorf("pin: %w", err)
	}
	title := in.Title
	if title == "" {
		title = filepath.Base(dest)
	}
	if err := s.attribute(ctx, store.ArtifactRef(dest), store.LinkMeta{Title: title}); err != nil {
		return nil, ArtifactOut{}, err
	}
	return nil, ArtifactOut{Path: dest, Title: in.Title, Tags: in.Tags, Type: a.Type, Pinned: true, Session: x.Name, Message: msg}, nil
}

// ListArtifactsIn is list_artifacts' input.
type ListArtifactsIn struct {
	ThisSessionOnly bool `json:"this_session_only,omitempty" jsonschema:"Only artifacts from this session."`
	PinnedOnly      bool `json:"pinned_only,omitempty" jsonschema:"Only pinned artifacts."`
}

// ListArtifactsOut is list_artifacts' result.
type ListArtifactsOut struct {
	Artifacts []ArtifactOut `json:"artifacts"`
}

func (s *Server) listArtifacts(ctx context.Context, _ *mcp.CallToolRequest, in ListArtifactsIn) (*mcp.CallToolResult, ListArtifactsOut, error) {
	out := ListArtifactsOut{Artifacts: []ArtifactOut{}}
	if s.projectName() == "" {
		return nil, out, nil
	}
	as, err := s.d.Store.Artifacts().List(ctx, s.projectName())
	if err != nil {
		return nil, ListArtifactsOut{}, err
	}
	for _, a := range as {
		name := a.SessionName
		if a.Link != nil && name == "" {
			name = a.Link.SessionName
		}
		if in.PinnedOnly && !a.Pinned {
			continue
		}
		if in.ThisSessionOnly && name != s.sess.Name {
			continue
		}
		out.Artifacts = append(out.Artifacts, ArtifactOut{Path: a.Path, Title: a.Title, Tags: a.Tags, Type: a.Type, Pinned: a.Pinned, Session: name})
	}
	return nil, out, nil
}

func (s *Server) addArtifactTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "pin_artifact",
		Description: "Pin a file you produced (report, screenshot, benchmark, diagram) so the user finds it on forge's Home and Artifacts tab. " +
			"Only files inside this session's worktree; a copy is kept in the project so it outlives the worktree.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false), IdempotentHint: true},
	}, s.pinArtifact)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_artifacts",
		Description: "List this project's artifacts known to forge (pinned files, outputs of sessions).",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.listArtifacts)
}
