package projects

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
)

func TestParseGitHubRemote(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"https://github.com/daan3011/forge.git", "daan3011/forge", true},
		{"https://github.com/daan3011/forge", "daan3011/forge", true},
		{"https://user:tok@github.com/daan3011/forge/", "daan3011/forge", true},
		{"git@github.com:daan3011/forge.git", "daan3011/forge", true},
		{"git@github.com-work:acme/app.git", "acme/app", true},
		{"ssh://git@github.com/acme/app.git", "acme/app", true},
		{"ssh://git@github.com:22/acme/app", "acme/app", true},
		{"https://gitlab.com/acme/app.git", "", false},
		{"git@bitbucket.org:acme/app.git", "", false},
		{"https://github.com/acme", "", false},
		{"https://github.com/acme/app/extra", "", false},
		{"/local/path/repo.git", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := ParseGitHubRemote(tt.in)
			if got != tt.want || ok != tt.ok {
				t.Errorf("got %q %v, want %q %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

// mkRepo makes a fake checkout at dir with the given .git/config body.
func mkRepo(t *testing.T, dir, gitConfig string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(gitConfig), 0o644); err != nil {
		t.Fatal(err)
	}
}

func remoteConfig(remotes ...string) string {
	s := "[core]\n\tbare = false\n"
	for i := 0; i+1 < len(remotes); i += 2 {
		s += "[remote \"" + remotes[i] + "\"]\n\turl = " + remotes[i+1] + "\n\tfetch = +refs/heads/*:refs/remotes/" + remotes[i] + "/*\n"
	}
	return s
}

func TestRepoOf(t *testing.T) {
	tests := []struct {
		name, config, want string
		ok                 bool
	}{
		{"origin", remoteConfig("origin", "git@github.com:a/b.git"), "a/b", true},
		{"origin wins", remoteConfig("upstream", "https://github.com/up/b", "origin", "https://github.com/me/b"), "me/b", true},
		{"first github remote", remoteConfig("origin", "https://gitlab.com/a/b", "gh", "https://github.com/c/d"), "c/d", true},
		{"quoted url", "[remote \"origin\"]\n\turl = \"https://github.com/a/b.git\"\n", "a/b", true},
		{"url outside remote", "[url \"git@github.com:\"]\n\tinsteadOf = https://github.com/\n", "", false},
		{"no github", remoteConfig("origin", "https://gitlab.com/a/b"), "", false},
		{"no remote", "[core]\n\tbare = false\n", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			mkRepo(t, dir, tt.config)
			got, ok := RepoOf(dir)
			if got != tt.want || ok != tt.ok {
				t.Errorf("got %q %v, want %q %v", got, ok, tt.want, tt.ok)
			}
		})
	}
	if _, ok := RepoOf(t.TempDir()); ok {
		t.Error("a plain folder has a repo")
	}
}

func TestRepoOfLinkedWorktree(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main")
	mkRepo(t, main, remoteConfig("origin", "https://github.com/a/b.git"))
	wtGit := filepath.Join(main, ".git", "worktrees", "feat")
	if err := os.MkdirAll(wtGit, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtGit, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(root, "feat")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+wtGit+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := RepoOf(wt); !ok || got != "a/b" {
		t.Errorf("got %q %v", got, ok)
	}
}

func TestRootsAndScan(t *testing.T) {
	home := t.TempDir()
	projects := filepath.Join(home, "Documents", "Projects")
	code := filepath.Join(home, "code")
	mkRepo(t, filepath.Join(projects, "forge"), remoteConfig("origin", "git@github.com:daan/forge.git"))
	mkRepo(t, filepath.Join(projects, "notes"), remoteConfig("origin", "https://gitlab.com/daan/notes"))
	mkRepo(t, filepath.Join(projects, ".hidden"), remoteConfig("origin", "git@github.com:daan/hidden.git"))
	if err := os.MkdirAll(filepath.Join(projects, "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	mkRepo(t, filepath.Join(code, "app"), remoteConfig("origin", "https://github.com/acme/app"))
	elsewhere := filepath.Join(home, "elsewhere", "lib")
	mkRepo(t, elsewhere, remoteConfig("origin", "https://github.com/acme/lib"))
	cwd := filepath.Join(elsewhere, "internal", "x")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	roots := Roots(nil, cwd, home)
	want := []string{filepath.Join(home, "elsewhere"), projects, code}
	if !reflect.DeepEqual(roots, want) {
		t.Fatalf("roots = %v, want %v", roots, want)
	}
	if got := Roots([]string{"/a", "/a/", "/b"}, cwd, home); !reflect.DeepEqual(got, []string{"/a", "/b"}) {
		t.Errorf("configured roots = %v", got)
	}

	found := Scan(roots, cwd)
	wantFound := []Found{ // by path: "Documents" sorts before "code"
		{Path: filepath.Join(projects, "forge"), Repo: "daan/forge"},
		{Path: filepath.Join(code, "app"), Repo: "acme/app"},
		{Path: elsewhere, Repo: "acme/lib"},
	}
	if !reflect.DeepEqual(found, wantFound) {
		t.Errorf("found = %+v\nwant  %+v", found, wantFound)
	}
	if root, ok := RepoRoot(cwd); !ok || root != elsewhere {
		t.Errorf("RepoRoot = %q %v", root, ok)
	}
}

func TestMerge(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	exists := func(p string) bool { return p != "/gone/old" }
	tests := []struct {
		name       string
		found      []Found
		remembered []Remembered
		overrides  []config.Project
		want       []config.Project
	}{
		{
			name:  "detected only, sorted by name",
			found: []Found{{"/c/zeta", "a/zeta"}, {"/c/alpha", "a/Alpha"}},
			want:  []config.Project{{Name: "Alpha", Repo: "a/Alpha", Path: "/c/alpha"}, {Name: "zeta", Repo: "a/zeta", Path: "/c/zeta"}},
		},
		{
			name:  "most recently used first",
			found: []Found{{"/c/a", "o/a"}, {"/c/b", "o/b"}, {"/c/c", "o/c"}},
			remembered: []Remembered{
				{Repo: "o/c", LastUsed: t0},
				{Repo: "O/B", LastUsed: t0.Add(time.Hour)},
			},
			want: []config.Project{
				{Name: "b", Repo: "o/b", Path: "/c/b"},
				{Name: "c", Repo: "o/c", Path: "/c/c"},
				{Name: "a", Repo: "o/a", Path: "/c/a"},
			},
		},
		{
			name:  "detected path wins; remembered path kept when not scanned; gone path dropped",
			found: []Found{{"/c/a", "o/a"}},
			remembered: []Remembered{
				{Repo: "o/a", Path: "/old/a"},
				{Repo: "o/b", Path: "/else/b"},
				{Repo: "o/old", Path: "/gone/old"},
			},
			want: []config.Project{
				{Name: "a", Repo: "o/a", Path: "/c/a"},
				{Name: "b", Repo: "o/b", Path: "/else/b"},
				{Name: "old", Repo: "o/old"},
			},
		},
		{
			name:  "two clones of one repo: the folder named after it wins",
			found: []Found{{"/c/site-blade", "o/site"}, {"/c/site", "o/site"}, {"/c/x1", "o/x"}, {"/c/x2", "o/x"}},
			want:  []config.Project{{Name: "site", Repo: "o/site", Path: "/c/site"}, {Name: "x", Repo: "o/x", Path: "/c/x1"}},
		},
		{
			name:  "name clash uses owner/name",
			found: []Found{{"/c/app", "acme/app"}, {"/d/app", "me/app"}},
			want:  []config.Project{{Name: "acme/app", Repo: "acme/app", Path: "/c/app"}, {Name: "me/app", Repo: "me/app", Path: "/d/app"}},
		},
		{
			name:      "config name wins a clash",
			found:     []Found{{"/c/app", "acme/app"}, {"/d/app", "me/app"}},
			overrides: []config.Project{{Repo: "me/app", Name: "app"}},
			want:      []config.Project{{Name: "acme/app", Repo: "acme/app", Path: "/c/app"}, {Name: "app", Repo: "me/app", Path: "/d/app"}},
		},
		{
			name:       "plane link from the database",
			found:      []Found{{"/c/a", "o/a"}, {"/c/b", "o/b"}},
			remembered: []Remembered{{Repo: "o/a", PlaneProjectID: "p-a", PlaneDecided: true}, {Repo: "o/b", PlaneDecided: true}},
			want: []config.Project{
				{Name: "a", Repo: "o/a", Path: "/c/a", PlaneProjectID: "p-a", PlaneDecided: true},
				{Name: "b", Repo: "o/b", Path: "/c/b", PlaneDecided: true},
			},
		},
		{
			name:       "config overrides field by field and beats the database",
			found:      []Found{{"/c/a", "o/a"}},
			remembered: []Remembered{{Repo: "o/a", PlaneProjectID: "p-db", PlaneDecided: true}},
			overrides: []config.Project{
				{Repo: "O/A", PlaneProjectID: "p-cfg", DocsDir: "docs", WorktreeCopy: []string{".env"}},
				{Repo: "o/remote-only", Name: "ro"},
			},
			want: []config.Project{
				{Name: "a", Repo: "o/a", Path: "/c/a", PlaneProjectID: "p-cfg", PlaneDecided: true, DocsDir: "docs", WorktreeCopy: []string{".env"}},
				{Name: "ro", Repo: "o/remote-only"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Merge(tt.found, tt.remembered, tt.overrides, exists)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestPlaneMatch(t *testing.T) {
	ps := []plane.Project{
		{ID: "1", Identifier: "FORGE", Name: "Forge"},
		{ID: "2", Identifier: "WEB", Name: "Web App"},
		{ID: "3", Identifier: "API", Name: "api"},
		{ID: "4", Identifier: "OLD", Name: "Legacy", ArchivedAt: plane.Date{Time: time.Now()}},
		{ID: "5", Identifier: "DUP", Name: "twin"},
		{ID: "6", Identifier: "TWIN", Name: "Other"},
	}
	tests := []struct {
		repo, want string
		ok         bool
	}{
		{"daan/forge", "1", true},
		{"acme/web-app", "2", true},
		{"acme/web_app", "2", true},
		{"acme/API", "3", true}, // identifier and name match the same project
		{"acme/legacy", "", false},
		{"acme/twin", "", false}, // two projects match
		{"acme/unknown", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.repo, func(t *testing.T) {
			got, ok := PlaneMatch(tt.repo, ps)
			if got != tt.want || ok != tt.ok {
				t.Errorf("got %q %v, want %q %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}
