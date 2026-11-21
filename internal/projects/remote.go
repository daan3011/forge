package projects

import (
	"bufio"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ParseGitHubRemote returns "owner/name" for a GitHub remote URL:
// https://github.com/o/n(.git), git@github.com:o/n.git,
// ssh://git@github.com/o/n.git, and ssh host aliases containing "github"
// (git@github.com-work:o/n.git). ok is false for anything else.
func ParseGitHubRemote(raw string) (repo string, ok bool) {
	raw = strings.TrimSpace(raw)
	var host, path string
	switch {
	case strings.Contains(raw, "://"):
		u, err := url.Parse(raw)
		if err != nil {
			return "", false
		}
		host, path = u.Hostname(), u.Path
	case strings.Contains(raw, ":"):
		// scp-like: [user@]host:owner/name
		h, p, _ := strings.Cut(raw, ":")
		if _, after, found := strings.Cut(h, "@"); found {
			h = after
		}
		host, path = h, p
	default:
		return "", false
	}
	if !strings.Contains(strings.ToLower(host), "github") {
		return "", false
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	owner, name, found := strings.Cut(path, "/")
	if !found || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", false
	}
	return owner + "/" + name, true
}

// gitDir is the folder holding the repo's config for a checkout at dir:
// dir/.git, or for a linked worktree (.git is a file) the common dir.
// ok is false when dir isn't a checkout.
func gitDir(dir string) (string, bool) {
	dot := filepath.Join(dir, ".git")
	fi, err := os.Stat(dot)
	if err != nil {
		return "", false
	}
	if fi.IsDir() {
		return dot, true
	}
	b, err := os.ReadFile(dot)
	if err != nil {
		return "", false
	}
	gd, found := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !found {
		return "", false
	}
	gd = strings.TrimSpace(gd)
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(dir, gd)
	}
	if c, err := os.ReadFile(filepath.Join(gd, "commondir")); err == nil {
		common := strings.TrimSpace(string(c))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gd, common)
		}
		return filepath.Clean(common), true
	}
	return gd, true
}

// RepoOf returns the GitHub repo of the checkout at dir, from its origin
// remote or else the first remote on GitHub. It reads .git/config instead
// of running git.
func RepoOf(dir string) (string, bool) {
	gd, ok := gitDir(dir)
	if !ok {
		return "", false
	}
	f, err := os.Open(filepath.Join(gd, "config"))
	if err != nil {
		return "", false
	}
	defer f.Close()
	var (
		remote string // the remote section we're in, "" outside one
		origin string
		first  string
	)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			remote = remoteSection(line)
			continue
		}
		if remote == "" {
			continue
		}
		k, v, found := strings.Cut(line, "=")
		if !found || !strings.EqualFold(strings.TrimSpace(k), "url") {
			continue
		}
		repo, ok := ParseGitHubRemote(strings.Trim(strings.TrimSpace(v), `"`))
		if !ok {
			continue
		}
		if remote == "origin" && origin == "" {
			origin = repo
		}
		if first == "" {
			first = repo
		}
	}
	switch {
	case origin != "":
		return origin, true
	case first != "":
		return first, true
	}
	return "", false
}

// remoteSection returns the remote name of a `[remote "name"]` header, or
// "" for any other section.
func remoteSection(line string) string {
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
	kind, name, found := strings.Cut(inner, " ")
	if !found || !strings.EqualFold(kind, "remote") {
		return ""
	}
	return strings.Trim(strings.TrimSpace(name), `"`)
}
