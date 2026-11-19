package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if p, _ := Path(); p != "/xdg/forge/config.yaml" {
		t.Errorf("with XDG: %q", p)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/me")
	if p, _ := Path(); p != "/home/me/.config/forge/config.yaml" {
		t.Errorf("without XDG: %q", p)
	}
}

func TestLoadOrCreate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg, path, created, err := LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	if !created || path != filepath.Join(dir, "forge", "config.yaml") || cfg == nil {
		t.Fatalf("first run: created=%v path=%q cfg=%v", created, path, cfg)
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), "# forge config") {
		t.Fatalf("default file is not the commented template:\n%s", data)
	}

	if err := os.WriteFile(path, []byte("ui:\n  animations: reduced\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, created, err = LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	if created || cfg.UI.Animations != AnimationsReduced {
		t.Fatalf("second run overwrote the file: created=%v animations=%q", created, cfg.UI.Animations)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string // substrings of the error, each must appear
	}{
		{
			name: "syntax error",
			body: "github:\n  refresh_interval: 60s\n bad: [\n",
			want: []string{"config.yaml:2: invalid YAML: did not find expected key"},
		},
		{
			name: "unknown top-level key",
			body: "ui:\n  animations: full\nthemes: {}\n",
			want: []string{"config.yaml:3: themes: unknown key (valid keys: projects, github,"},
		},
		{
			name: "unknown nested key",
			body: "plane:\n  base_url: https://plane.example\n  apikey: x\n",
			want: []string{"config.yaml:3: plane.apikey: unknown key", "api_key_env"},
		},
		{
			name: "unknown key in list item",
			body: "projects:\n  - name: forge\n    repo: a/b\n    path: /x\n    dir: docs\n",
			want: []string{"config.yaml:5: projects[0].dir: unknown key"},
		},
		{
			name: "wrong type",
			body: "sessions:\n  notify_after_minutes: soon\n",
			want: []string{"config.yaml:2: sessions.notify_after_minutes: cannot unmarshal"},
		},
		{
			name: "bad duration",
			body: "github:\n  refresh_interval: often\n",
			want: []string{`config.yaml:2: github.refresh_interval: must be a duration like "60s" or "5m", got "often"`},
		},
		{
			name: "several problems, sorted by line",
			body: "ui:\n  animations: fast\ngithub:\n  refresh_interval: 1s\n",
			want: []string{
				"2 problems in config:",
				"config.yaml:2: ui.animations",
				"config.yaml:4: github.refresh_interval: must be at least 5s",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.body))
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error does not contain %q:\n%v", w, err)
				}
			}
			if i := strings.Index(err.Error(), "ui.animations"); i >= 0 {
				if j := strings.Index(err.Error(), "github.refresh_interval"); j >= 0 && j < i {
					t.Errorf("errors not sorted by line:\n%v", err)
				}
			}
		})
	}
}
