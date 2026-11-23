package sessions

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/daan3011/forge/internal/sessions/claude"
	"github.com/daan3011/forge/internal/sessions/hooks"
	"github.com/daan3011/forge/internal/store"
)

// Prepared is what Prepare wrote for a session's claude launch.
type Prepared struct {
	// MCPConfig is the --mcp-config file; empty without a forge binary.
	MCPConfig string
	// AddDir is the --add-dir skills folder; empty when there is none.
	AddDir string
	// SkillsErr is set when forge's skills couldn't all be written; the
	// session still starts with what could be.
	SkillsErr error
}

// Prepare (re)writes everything forge gives a session's claude outside
// the conversation itself: the hooks in x.Worktree (git-ignored), the
// per-session MCP config and the forge skills in the session dir. Start,
// Resume and `forge doctor --fix` all go through it, so a moved forge
// binary or a removed session dir is repaired the same way everywhere.
// x needs Name, Project, Repo, Branch and Worktree.
func (s *Service) Prepare(ctx context.Context, x store.Session) (Prepared, error) {
	var p Prepared
	if s.c.ForgeBin != "" {
		if err := hooks.Install(x.Worktree, s.c.ForgeBin); err != nil {
			return p, err
		}
		if err := s.c.Worktrees.EnsureExcluded(ctx, x.Worktree, hooks.SettingsRel); err != nil {
			return p, fmt.Errorf("git-ignore hook settings: %w", err)
		}
	}
	if s.c.ForgeBin == "" && s.c.Skills == nil {
		return p, nil
	}
	if err := os.MkdirAll(s.SessionDir(x.Name), 0o700); err != nil {
		return p, fmt.Errorf("session dir: %w", err)
	}
	if s.c.ForgeBin != "" {
		// forge's MCP server for this session only (P6.5): passed with
		// --mcp-config from the session dir, never in any Claude config.
		p.MCPConfig = filepath.Join(s.SessionDir(x.Name), claude.MCPConfigFile)
		if err := claude.WriteMCPConfig(p.MCPConfig, s.c.ForgeBin, x.Name); err != nil {
			return p, err
		}
	}
	if s.c.Skills != nil {
		// forge's skills (P6.7) in the session dir, never in the worktree.
		p.AddDir, p.SkillsErr = s.syncSkills(x)
	}
	return p, nil
}

// ForgeBin is the forge binary the service registers in hooks and MCP
// configs; empty when it registers none.
func (s *Service) ForgeBin() string { return s.c.ForgeBin }
