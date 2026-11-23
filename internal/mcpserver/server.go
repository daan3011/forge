package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/projects"
	"github.com/daan3011/forge/internal/proposals"
	"github.com/daan3011/forge/internal/sessions/live"
	"github.com/daan3011/forge/internal/store"
)

// Name is the server's MCP name; its tools reach Claude as mcp__forge__*.
const Name = "forge"

// ErrNoSession is returned by write tools outside a forge session.
var ErrNoSession = errors.New("not inside a forge session (FORGE_SESSION is unset or unknown); " +
	"forge tools that write only work in sessions started by forge")

// Deps are the server's collaborators; tests pass fakes.
type Deps struct {
	Store *store.Store
	// Plane is nil when Plane isn't configured; PlaneErr then says why.
	Plane    plane.API
	PlaneErr error
	// Config holds the resolved project list (projects.Resolve).
	Config *config.Config
	// SessionName is FORGE_SESSION; Cwd is the server's working directory,
	// the fallback for finding the session.
	SessionName string
	Cwd         string
	// PacksDir is where context packs live (packs.Dir()).
	PacksDir string
	Version  string
	Now      func() time.Time
	// Ping tells a running forge TUI that something changed; nil does
	// nothing.
	Ping func(ctx context.Context, p live.Ping)
	// Logger receives the SDK's and the server's logs; nil discards them.
	// It must never write to stdout.
	Logger *slog.Logger
}

// Server serves one session's forge tools.
type Server struct {
	d Deps
	// sess is the forge session; nil outside one.
	sess    *store.Session
	project config.Project
	hasProj bool
}

// New resolves the session and returns the server. A missing session is
// not an error: read tools still work, write tools return ErrNoSession.
func New(ctx context.Context, d Deps) (*Server, error) {
	if d.Store == nil || d.Config == nil {
		return nil, errors.New("mcpserver: store and config are required")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	s := &Server{d: d}
	x, err := findSession(ctx, d.Store, d.SessionName, d.Cwd)
	if err != nil {
		return nil, err
	}
	if x != nil {
		s.sess = x
		s.project, s.hasProj = projects.ByName(d.Config.Projects, x.Project)
	}
	return s, nil
}

// Session returns the resolved session, nil outside one.
func (s *Server) Session() *store.Session { return s.sess }

// findSession looks the session up by name, else by working directory.
func findSession(ctx context.Context, st *store.Store, name, cwd string) (*store.Session, error) {
	if name != "" {
		x, err := st.Sessions().GetByName(ctx, name)
		switch {
		case err == nil:
			return &x, nil
		case !errors.Is(err, store.ErrNotFound):
			return nil, err
		}
	}
	if cwd == "" {
		return nil, nil
	}
	xs, err := st.Sessions().List(ctx, store.SessionFilter{})
	if err != nil {
		return nil, err
	}
	for i := range xs {
		if xs[i].Worktree != "" && within(xs[i].Worktree, cwd) {
			return &xs[i], nil
		}
	}
	return nil, nil
}

// within reports whether p is root or below it, symlinks resolved.
func within(root, p string) bool {
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// MCP builds the MCP server with every forge tool.
func (s *Server) MCP() *mcp.Server {
	v := s.d.Version
	if v == "" {
		v = "dev"
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: Name, Title: "forge", Version: v}, &mcp.ServerOptions{
		Instructions: instructions,
		Logger:       s.d.Logger,
	})
	s.addPlaneTools(srv)
	s.addDocTools(srv)
	s.addArtifactTools(srv)
	s.addSessionTools(srv)
	return srv
}

// Run serves over t until the client disconnects or ctx ends.
func (s *Server) Run(ctx context.Context, t mcp.Transport) error {
	return s.MCP().Run(ctx, t)
}

const instructions = `forge tools for this agent session. Use them to hand work back to the user's forge dashboard:
file follow-up work with create_issue (it becomes a proposal the user triages), record decisions with
log_decision, keep notes in the project docs with write_doc, pin useful outputs with pin_artifact, and
report_progress at milestones. Call get_context_pack at the start for session context. Everything you
write is attributed to this session, its branch and its PR.`

// session returns the session or ErrNoSession.
func (s *Server) session() (*store.Session, error) {
	if s.sess == nil {
		return nil, ErrNoSession
	}
	return s.sess, nil
}

// worktree returns the session's worktree, which must exist.
func (s *Server) worktree() (string, error) {
	x, err := s.session()
	if err != nil {
		return "", err
	}
	if x.Worktree == "" {
		return "", fmt.Errorf("session %s has no worktree", x.Name)
	}
	if fi, err := os.Stat(x.Worktree); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("session %s: worktree %s is missing", x.Name, x.Worktree)
	}
	return x.Worktree, nil
}

// projectName is the session's forge project name.
func (s *Server) projectName() string {
	if s.sess == nil {
		return ""
	}
	return s.sess.Project
}

// attribute links the session to target and pings the TUI.
func (s *Server) attribute(ctx context.Context, target store.Ref, meta store.LinkMeta) error {
	x, err := s.session()
	if err != nil {
		return err
	}
	if err := s.d.Store.Links().Add(ctx, store.SessionRef(x.ID), target, meta); err != nil {
		return fmt.Errorf("link to session: %w", err)
	}
	s.ping(ctx)
	return nil
}

// ping tells a running TUI to refresh; never fatal.
func (s *Server) ping(ctx context.Context) {
	if s.d.Ping == nil || s.sess == nil {
		return
	}
	s.d.Ping(ctx, live.Ping{Session: s.sess.Name, ClaudeSessionID: s.sess.ClaudeSessionID, Event: PingEvent})
}

// PingEvent is the live ping event forge mcp sends after a write.
const PingEvent = "mcp"

// proposalDeps are the deps for proposals.Accept / PlaneProject.
func (s *Server) proposalDeps() proposals.Deps {
	return proposals.Deps{Store: s.d.Store, Plane: s.d.Plane, Projects: s.d.Config.Projects}
}

// planeAPI returns the Plane client or why there is none.
func (s *Server) planeAPI() (plane.API, error) {
	if s.d.Plane != nil {
		return s.d.Plane, nil
	}
	if s.d.PlaneErr != nil {
		return nil, fmt.Errorf("plane is not available: %w", s.d.PlaneErr)
	}
	return nil, errors.New("plane is not configured in forge")
}
