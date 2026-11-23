package projects

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/store"
)

// Remembering is the part of the store Resolve uses: the repos forge has
// seen used.
type Remembering interface {
	Touch(ctx context.Context, repo, path string) error
	List(ctx context.Context) ([]store.Project, error)
}

// Resolve replaces cfg.Projects with the zero-config project list:
// checkouts found under the project roots, repos the store remembers, and
// cfg.Projects as overrides. The repo containing cwd is remembered as used.
// It returns the name of that repo's project, "" when cwd isn't inside one.
// Store failures only warn. Shared by the TUI, the CLI and forge mcp.
func Resolve(ctx context.Context, cfg *config.Config, st Remembering, cwd, home string, warn io.Writer) string {
	found := Scan(Roots(cfg.ProjectRoots, cwd, home), cwd)

	here := ""
	if root, ok := RepoRoot(cwd); ok {
		if repo, ok := RepoOf(root); ok {
			here = root
			if err := st.Touch(ctx, repo, root); err != nil {
				fmt.Fprintln(warn, "warning: remember project:", err)
			}
		}
	}
	var remembered []Remembered
	rows, err := st.List(ctx)
	if err != nil {
		fmt.Fprintln(warn, "warning: remembered projects:", err)
	}
	for _, r := range rows {
		remembered = append(remembered, Remembered{
			Repo: r.Repo, Path: r.Path, LastUsed: r.LastUsed,
			PlaneProjectID: r.PlaneProjectID, PlaneDecided: r.PlaneDecided,
		})
	}
	cfg.Projects = Merge(found, remembered, cfg.Projects, nil)
	if here == "" {
		return ""
	}
	for _, p := range cfg.Projects {
		if p.Path != "" && SamePath(p.Path, here) {
			return p.Name
		}
	}
	return ""
}

// ByName returns the project called name in ps.
func ByName(ps []config.Project, name string) (config.Project, bool) {
	for _, p := range ps {
		if p.Name == name {
			return p, true
		}
	}
	return config.Project{}, false
}

// SamePath compares paths with symlinks resolved.
func SamePath(a, b string) bool {
	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
