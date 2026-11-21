package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/projects"
	"github.com/daan3011/forge/internal/store"
)

// osGetwd and osHome are the defaults of deps.getwd and deps.homeDir;
// TestMain points them at empty folders so tests never scan real repos.
var (
	osGetwd = func() string { wd, _ := os.Getwd(); return wd }
	osHome  = func() string { h, _ := os.UserHomeDir(); return h }
)

func (d deps) cwd() string {
	if d.getwd != nil {
		return d.getwd()
	}
	return osGetwd()
}

func (d deps) home() string {
	if d.homeDir != nil {
		return d.homeDir()
	}
	return osHome()
}

// resolveProjects replaces cfg.Projects with the zero-config project list:
// checkouts found under the project roots, repos the store remembers, and
// cfg.Projects as overrides. The repo containing the working directory is
// remembered as used. It returns the name of that repo's project, "" when
// forge doesn't run inside one. Store failures only warn.
func (d deps) resolveProjects(ctx context.Context, cfg *config.Config, st *store.Store, warn io.Writer) string {
	cwd, home := d.cwd(), d.home()
	found := projects.Scan(projects.Roots(cfg.ProjectRoots, cwd, home), cwd)

	here := ""
	if root, ok := projects.RepoRoot(cwd); ok {
		if repo, ok := projects.RepoOf(root); ok {
			here = root
			if err := st.Projects().Touch(ctx, repo, root); err != nil {
				fmt.Fprintln(warn, "warning: remember project:", err)
			}
		}
	}
	var remembered []projects.Remembered
	rows, err := st.Projects().List(ctx)
	if err != nil {
		fmt.Fprintln(warn, "warning: remembered projects:", err)
	}
	for _, r := range rows {
		remembered = append(remembered, projects.Remembered{
			Repo: r.Repo, Path: r.Path, LastUsed: r.LastUsed,
			PlaneProjectID: r.PlaneProjectID, PlaneDecided: r.PlaneDecided,
		})
	}
	cfg.Projects = projects.Merge(found, remembered, cfg.Projects, nil)
	if here == "" {
		return ""
	}
	for _, p := range cfg.Projects {
		if p.Path != "" && samePath(p.Path, here) {
			return p.Name
		}
	}
	return ""
}

func samePath(a, b string) bool {
	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
