package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeBytes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		sets []Set
		want string
	}{
		{
			name: "replace scalar keeps comments",
			in:   "# top\nplane:\n  # the slug\n  workspace_slug: \"\" # inline\n  api_key_env: PLANE_API_KEY\n",
			sets: []Set{{"plane.workspace_slug", "acme"}},
			want: "# top\nplane:\n  # the slug\n  workspace_slug: acme # inline\n  api_key_env: PLANE_API_KEY\n",
		},
		{
			name: "insert into section",
			in:   "plane:\n  base_url: https://api.plane.so\n\n# next\nui:\n  animations: full\n",
			sets: []Set{{"plane.workspace_slug", "acme"}},
			want: "plane:\n  base_url: https://api.plane.so\n  workspace_slug: acme\n\n# next\nui:\n  animations: full\n",
		},
		{
			name: "new section at the end",
			in:   "# only a comment\ngithub:\n  stale_after_days: 5\n\n",
			sets: []Set{{"plane.workspace_slug", "acme"}},
			want: "# only a comment\ngithub:\n  stale_after_days: 5\nplane:\n  workspace_slug: acme\n\n",
		},
		{
			name: "commented-out top-level list is added for real",
			in:   "# project_roots:\n#   - ~/code\n\nui:\n  animations: full\n",
			sets: []Set{{"project_roots", []string{"~/src", "~/work"}}},
			want: "# project_roots:\n#   - ~/code\n\nui:\n  animations: full\nproject_roots:\n  - ~/src\n  - ~/work\n",
		},
		{
			name: "replace block list",
			in:   "project_roots:\n  - ~/a\n  - ~/b\n# after\nui:\n  animations: full\n",
			sets: []Set{{"project_roots", []string{"~/c"}}},
			want: "project_roots:\n  - ~/c\n# after\nui:\n  animations: full\n",
		},
		{
			name: "flow list stays flow",
			in:   "plane:\n  blocked_labels: [blocked]\n",
			sets: []Set{{"plane.blocked_labels", []string{"blocked", "waiting"}}},
			want: "plane:\n  blocked_labels: [blocked, waiting]\n",
		},
		{
			name: "null section with comments",
			in:   "ui:\n  # theme:\n  #   ember: x\nother: 1\n",
			sets: []Set{{"ui.animations", "off"}},
			want: "ui:\n  animations: \"off\"\n  # theme:\n  #   ember: x\nother: 1\n",
		},
		{
			name: "empty file",
			in:   "",
			sets: []Set{{"ui.animations", "reduced"}},
			want: "ui:\n  animations: reduced\n",
		},
		{
			name: "several sets",
			in:   "plane:\n  base_url: https://api.plane.so\n  workspace_slug: \"\"\n",
			sets: []Set{{"plane.base_url", "https://plane.example.com"}, {"plane.workspace_slug", "acme"}, {"plane.api_key_env", "MY_KEY"}},
			want: "plane:\n  base_url: https://plane.example.com\n  workspace_slug: acme\n  api_key_env: MY_KEY\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MergeBytes([]byte(tt.in), tt.sets...)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestMergeBytesErrors(t *testing.T) {
	if _, err := MergeBytes([]byte("plane: 3\n"), Set{"plane.workspace_slug", "x"}); err == nil {
		t.Error("setting below a scalar should fail")
	}
	if _, err := MergeBytes([]byte("- a\n"), Set{"x", "y"}); err == nil {
		t.Error("a list at the top level should fail")
	}
	if _, err := MergeBytes([]byte("a: 1\n"), Set{"a..b", "y"}); err == nil {
		t.Error("an empty key should fail")
	}
}

// The default template keeps every line but the ones set.
func TestMergeTemplate(t *testing.T) {
	out, err := MergeBytes(Template(),
		Set{"plane.workspace_slug", "acme"}, Set{"ui.animations", "reduced"},
		Set{"project_roots", []string{"~/code"}})
	if err != nil {
		t.Fatal(err)
	}
	in := strings.Split(string(Template()), "\n")
	got := strings.Split(string(out), "\n")
	changed := 0
	for _, l := range in {
		if !contains(got, l) {
			changed++
			if !strings.Contains(l, "workspace_slug") && !strings.Contains(l, "animations: full") {
				t.Errorf("line lost: %q", l)
			}
		}
	}
	if changed != 2 {
		t.Errorf("changed %d lines, want 2", changed)
	}
	cfg, err := parse("config.yaml", out)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Plane.Workspace != "acme" || cfg.UI.Animations != AnimationsReduced || len(cfg.ProjectRoots) != 1 {
		t.Errorf("parsed %+v %v %v", cfg.Plane.Workspace, cfg.UI.Animations, cfg.ProjectRoots)
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func TestMergeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	// A missing file starts from the template.
	if err := Merge(path, Set{"plane.workspace_slug", "acme"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Plane.Workspace != "acme" {
		t.Errorf("workspace = %q", cfg.Plane.Workspace)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "# forge config") {
		t.Error("template comments missing")
	}

	// An invalid result is refused and the file left alone.
	if err := Merge(path, Set{"ui.animations", "sometimes"}); err == nil {
		t.Fatal("invalid animations accepted")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(data) {
		t.Error("file changed by a refused merge")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temp files left behind: %v", entries)
	}
}

func TestForgeDirOverrides(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg/config")
	t.Setenv("XDG_DATA_HOME", "/xdg/data")
	t.Setenv(EnvConfigDir, "")
	t.Setenv(EnvDataDir, "")
	if d, _ := Dir(); d != "/xdg/config/forge" {
		t.Errorf("Dir = %q", d)
	}
	if d := DataDir(); d != "/xdg/data/forge" {
		t.Errorf("DataDir = %q", d)
	}
	t.Setenv(EnvConfigDir, "/clean/config")
	t.Setenv(EnvDataDir, "/clean/data")
	if d, _ := Dir(); d != "/clean/config" {
		t.Errorf("Dir with override = %q", d)
	}
	if d := DataDir(); d != "/clean/data" {
		t.Errorf("DataDir with override = %q", d)
	}
	if d := NotesDir(); d != "/clean/data/notes" {
		t.Errorf("NotesDir = %q", d)
	}
	if d := Default().Sessions.WorktreeRoot; d != "/clean/data/worktrees" {
		t.Errorf("worktree root = %q", d)
	}
}
