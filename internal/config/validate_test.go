package config

import (
	"errors"
	"strings"
	"testing"
)

const validProject = "projects:\n  - name: forge\n    repo: daan/forge\n    path: /code/forge\n"

func TestValidation(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		key     string
		line    int
		msgPart string
	}{
		{"relative project root", "project_roots: [code]\n", "project_roots[0]", 1, "must be absolute"},
		{"duplicate project", validProject + "  - name: forge\n    repo: a/b\n    path: /y\n", "projects[1].name", 5, `duplicate project name "forge"`},
		{"bad repo", "projects:\n  - name: f\n    repo: forge\n    path: /x\n", "projects[0].repo", 3, `must be "owner/name", got "forge"`},
		{"missing repo points at item", "projects:\n  - name: f\n    path: /x\n", "projects[0].repo", 2, "owner/name"},
		{"relative path", "projects:\n  - name: f\n    repo: a/b\n    path: code/f\n", "projects[0].path", 4, "must be absolute"},
		{"copy absolute", validProject + "    worktree_copy: [/etc/passwd]\n", "projects[0].worktree_copy[0]", 5, "must be relative"},
		{"copy escapes", validProject + "    worktree_copy: [a/../../x]\n", "projects[0].worktree_copy[0]", 5, `must not contain ".."`},
		{"copy bad glob", validProject + "    worktree_copy: [\"[a\"]\n", "projects[0].worktree_copy[0]", 5, "invalid glob"},
		{"refresh too fast", "github:\n  refresh_interval: 2s\n", "github.refresh_interval", 2, "at least 5s"},
		{"plane refresh too fast", "plane:\n  refresh_interval: 1s\n", "plane.refresh_interval", 2, "at least 5s"},
		{"sessions refresh too fast", "sessions:\n  refresh_interval: 3s\n", "sessions.refresh_interval", 2, "at least 5s"},
		{"section title", "github:\n  pr_sections:\n    - query: is:open\n", "github.pr_sections[0].title", 3, "is required"},
		{"section query", "github:\n  pr_sections:\n    - title: All\n", "github.pr_sections[0].query", 3, "is required"},
		{"plane board days", "plane:\n  board_done_days: 0\n", "plane.board_done_days", 2, "1 or more"},
		{"plane url", "plane:\n  base_url: plane.local\n", "plane.base_url", 2, "http(s) URL"},
		{"relative worktree root", "sessions:\n  worktree_root: wt\n", "sessions.worktree_root", 2, "must be absolute"},
		{"negative stale", "github:\n  stale_after_days: -1\n", "github.stale_after_days", 2, "0 or more"},
		{"negative notify", "sessions:\n  notify_after_minutes: -1\n", "sessions.notify_after_minutes", 2, "0 or more"},
		{"archive cleanup enum", "sessions:\n  archive_cleanup: sometimes\n", "sessions.archive_cleanup", 2, `must be one of ask, always, never; got "sometimes"`},
		{"bad glob", "artifacts:\n  watch:\n    - /a/[b\n", "artifacts.watch[0]", 3, "invalid glob"},
		{"preview enum", "artifacts:\n  preview: iterm\n", "artifacts.preview", 2, `must be one of auto, kitty, sixel, chafa, text, off; got "iterm"`},
		{"animations enum", "ui:\n  animations: fast\n", "ui.animations", 2, `must be one of full, reduced, off; got "fast"`},
		{"theme colour", "ui:\n  theme:\n    ember: orange\n", "ui.theme.ember", 3, "must be a colour"},
		{"keybind empty list", "keybinds:\n  quit: []\n", "keybinds.quit", 2, "at least one key"},
		{"keybind empty key", "keybinds:\n  quit: [q, \"\"]\n", "keybinds.quit[1]", 2, "must not be empty"},
		{"command name", "custom_commands:\n  - command: ls\n", "custom_commands[0].name", 2, "is required"},
		{"command body", "custom_commands:\n  - name: ls\n", "custom_commands[0].command", 2, "is required"},
		{"duplicate command", "custom_commands:\n  - name: a\n    command: x\n  - name: a\n    command: y\n", "custom_commands[1].name", 4, "duplicate command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.body))
			var es Errors
			if !errors.As(err, &es) || len(es) != 1 {
				t.Fatalf("want exactly one ValidationError, got %v", err)
			}
			e := es[0]
			if e.Key != tt.key || e.Line != tt.line || !strings.Contains(e.Msg, tt.msgPart) {
				t.Fatalf("got %s:%d %q, want %s:%d containing %q", e.Key, e.Line, e.Msg, tt.key, tt.line, tt.msgPart)
			}
			if !strings.HasSuffix(e.File, "config.yaml") {
				t.Fatalf("file = %q", e.File)
			}
		})
	}
}

func TestValidConfig(t *testing.T) {
	body := validProject + `github:
  refresh_interval: 30s
plane:
  base_url: https://plane.example.com
  workspace_slug: me
sessions:
  notify_after_minutes: 0
  archive_cleanup: always
ui:
  animations: reduced
  theme:
    ember: "#f80"
keybinds:
  quit: [q]
custom_commands:
  - name: open
    key: ctrl+e
    command: code {worktree}
`
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Projects) != 1 || cfg.Projects[0].Repo != "daan/forge" || len(cfg.CustomCommands) != 1 {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestValidateInMemoryHasNoFile(t *testing.T) {
	c := Default()
	c.UI.Animations = "loud"
	err := c.Validate()
	if err == nil || err.Error() != `ui.animations: must be one of full, reduced, off; got "loud"` {
		t.Fatalf("err = %v", err)
	}
}

// TestProjectOverrides: projects are detected, so a config entry only needs
// its repo; project_roots expand ~ like other paths.
func TestProjectOverrides(t *testing.T) {
	t.Setenv("HOME", "/home/me")
	body := "project_roots: [~/code, /srv/git]\nprojects:\n  - repo: daan/forge\n    plane_project_id: abc\n  - repo: daan/lib\n"
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.ProjectRoots; len(got) != 2 || got[0] != "/home/me/code" || got[1] != "/srv/git" {
		t.Errorf("roots = %v", got)
	}
	if len(cfg.Projects) != 2 || cfg.Projects[0].Name != "" || cfg.Projects[0].PlaneProjectID != "abc" {
		t.Errorf("projects = %+v", cfg.Projects)
	}
}
