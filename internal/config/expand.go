package config

import (
	"os"
	"path/filepath"
	"strings"
)

// expand resolves $VAR / ${VAR} and a leading ~/ in path-like fields. It runs
// per field (never on the raw YAML) so env values cannot inject YAML.
func (c *Config) expand() {
	for i, r := range c.ProjectRoots {
		c.ProjectRoots[i] = expandPath(r)
	}
	for i := range c.Projects {
		p := &c.Projects[i]
		p.Path = expandPath(p.Path)
		p.DocsDir = expandPath(p.DocsDir)
	}
	c.Plane.BaseURL = os.ExpandEnv(c.Plane.BaseURL)
	c.Plane.APIKey = os.ExpandEnv(c.Plane.APIKey)
	c.Sessions.WorktreeRoot = expandPath(c.Sessions.WorktreeRoot)
	for i, g := range c.Artifacts.Watch {
		c.Artifacts.Watch[i] = expandPath(g)
	}
}

// expandPath expands environment variables, then a leading "~" or "~/".
func expandPath(s string) string {
	s = os.ExpandEnv(s)
	if s == "~" {
		return homeDir()
	}
	if rest, ok := strings.CutPrefix(s, "~/"); ok {
		return filepath.Join(homeDir(), rest)
	}
	return s
}
