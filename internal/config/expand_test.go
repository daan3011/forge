package config

import (
	"strings"
	"testing"
)

func TestExpansion(t *testing.T) {
	t.Setenv("HOME", "/home/me")
	t.Setenv("CODE", "/src")
	t.Setenv("PLANE_HOST", "plane.example.com")
	t.Setenv("SECRET", "s3cret")

	body := `projects:
  - name: a
    repo: o/a
    path: ~/code/a
    docs_dir: ${CODE}/docs
  - name: b
    repo: o/b
    path: $CODE/b
plane:
  base_url: https://${PLANE_HOST}
  workspace_slug: $NOT_EXPANDED
  api_key: $SECRET
sessions:
  worktree_root: ~/wt
artifacts:
  watch:
    - ~/art/*.html
    - $CODE/**
custom_commands:
  - name: c
    command: echo $HOME
`
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct{ name, got, want string }{
		{"project path ~", cfg.Projects[0].Path, "/home/me/code/a"},
		{"docs_dir ${VAR}", cfg.Projects[0].DocsDir, "/src/docs"},
		{"project path $VAR", cfg.Projects[1].Path, "/src/b"},
		{"plane base_url", cfg.Plane.BaseURL, "https://plane.example.com"},
		{"plane api_key", cfg.Plane.APIKey, "s3cret"},
		{"workspace untouched", cfg.Plane.Workspace, "$NOT_EXPANDED"},
		{"worktree_root", cfg.Sessions.WorktreeRoot, "/home/me/wt"},
		{"watch ~", cfg.Artifacts.Watch[0], "/home/me/art/*.html"},
		{"watch $VAR", cfg.Artifacts.Watch[1], "/src/**"},
		{"command untouched", cfg.CustomCommands[0].Command, "echo $HOME"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestExpandPath(t *testing.T) {
	t.Setenv("HOME", "/home/me")
	tests := []struct{ in, want string }{
		{"~", "/home/me"},
		{"~/x", "/home/me/x"},
		{"~other/x", "~other/x"},
		{"/abs", "/abs"},
		{"$HOME/y", "/home/me/y"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := expandPath(tt.in); got != tt.want {
			t.Errorf("expandPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestResolveAPIKey(t *testing.T) {
	tests := []struct {
		name    string
		plane   Plane
		env     string
		want    string
		wantErr string
	}{
		{"literal wins", Plane{APIKey: "lit", APIKeyEnv: "FORGE_TEST_KEY"}, "env", "lit", ""},
		{"from env", Plane{APIKeyEnv: "FORGE_TEST_KEY"}, "env", "env", ""},
		{"env unset", Plane{APIKeyEnv: "FORGE_TEST_KEY"}, "", "", "FORGE_TEST_KEY is not set"},
		{"nothing configured", Plane{}, "", "", "no API key configured"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("FORGE_TEST_KEY", tt.env)
			got, err := tt.plane.ResolveAPIKey()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}
