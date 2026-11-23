package main

import (
	"context"
	"io"
	"os"

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

// resolveProjects replaces cfg.Projects with the zero-config project list
// (projects.Resolve) for the working directory, and returns the name of the
// project forge runs inside, "" when none.
func (d deps) resolveProjects(ctx context.Context, cfg *config.Config, st *store.Store, warn io.Writer) string {
	return projects.Resolve(ctx, cfg, st.Projects(), d.cwd(), d.home(), warn)
}
