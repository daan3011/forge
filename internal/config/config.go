package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultDocsDir is where a project's docs live, relative to its path, when
// docs_dir is not set.
const DefaultDocsDir = ".forge/docs"

// Config is the whole forge config file.
type Config struct {
	Projects       []Project           `yaml:"projects"`
	GitHub         GitHub              `yaml:"github"`
	Plane          Plane               `yaml:"plane"`
	Sessions       Sessions            `yaml:"sessions"`
	Artifacts      Artifacts           `yaml:"artifacts"`
	UI             UI                  `yaml:"ui"`
	Keybinds       map[string][]string `yaml:"keybinds"`
	CustomCommands []CustomCommand     `yaml:"custom_commands"`
}

// Project ties a GitHub repo, a local checkout, a Plane project and a docs
// folder together.
type Project struct {
	Name           string `yaml:"name"`
	Repo           string `yaml:"repo"`
	Path           string `yaml:"path"`
	PlaneProjectID string `yaml:"plane_project_id"`
	DocsDir        string `yaml:"docs_dir"`
	// WorktreeCopy lists files (globs relative to Path, e.g. ".env.local")
	// copied from the checkout into every new agent worktree.
	WorktreeCopy []string `yaml:"worktree_copy"`
}

// Owner returns the owner half of Repo ("owner/name").
func (p Project) Owner() string {
	owner, _, _ := strings.Cut(p.Repo, "/")
	return owner
}

// RepoName returns the name half of Repo ("owner/name").
func (p Project) RepoName() string {
	_, name, _ := strings.Cut(p.Repo, "/")
	return name
}

// DocsPath returns the absolute docs folder: DocsDir resolved against Path,
// or DefaultDocsDir when DocsDir is empty.
func (p Project) DocsPath() string {
	dir := p.DocsDir
	if dir == "" {
		dir = DefaultDocsDir
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(p.Path, dir)
}

// GitHub holds PR list settings.
type GitHub struct {
	RefreshInterval Duration    `yaml:"refresh_interval"`
	PRSections      []PRSection `yaml:"pr_sections"`
}

// PRSection is one titled PR list backed by a GitHub search query.
type PRSection struct {
	Title string `yaml:"title"`
	Query string `yaml:"query"`
}

// Plane holds Plane API settings.
type Plane struct {
	BaseURL   string `yaml:"base_url"`
	Workspace string `yaml:"workspace_slug"`
	APIKey    string `yaml:"api_key"`
	APIKeyEnv string `yaml:"api_key_env"`
	// RefreshInterval is how often Plane lists refresh in the background.
	RefreshInterval Duration `yaml:"refresh_interval"`
}

// ResolveAPIKey returns APIKey if set, otherwise the value of the environment
// variable named by APIKeyEnv. It is called lazily so an unused Plane setup
// never breaks startup.
func (p Plane) ResolveAPIKey() (string, error) {
	if p.APIKey != "" {
		return p.APIKey, nil
	}
	if p.APIKeyEnv == "" {
		return "", errors.New("plane: no API key configured; set plane.api_key or plane.api_key_env")
	}
	if v := os.Getenv(p.APIKeyEnv); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("plane: environment variable %s is not set (from plane.api_key_env)", p.APIKeyEnv)
}

// Sessions holds agent session settings.
type Sessions struct {
	WorktreeRoot       string `yaml:"worktree_root"`
	DefaultTemplate    string `yaml:"default_template"`
	NotifyAfterMinutes int    `yaml:"notify_after_minutes"`
	// RefreshInterval is how often session status refreshes in the background.
	RefreshInterval Duration `yaml:"refresh_interval"`
}

// Artifacts holds the artifact gallery settings.
type Artifacts struct {
	Watch []string `yaml:"watch"`
}

// Animations is the animation level.
type Animations string

// Animation levels.
const (
	AnimationsFull    Animations = "full"
	AnimationsReduced Animations = "reduced"
	AnimationsOff     Animations = "off"
)

// UI holds look-and-feel settings.
type UI struct {
	Animations Animations        `yaml:"animations"`
	Theme      map[string]string `yaml:"theme"`
}

// CustomCommand is a user-defined shell command, available in the palette and
// optionally bound to a key.
type CustomCommand struct {
	Name        string `yaml:"name"`
	Key         string `yaml:"key"`
	Command     string `yaml:"command"`
	Description string `yaml:"description"`
}

// Duration is a time.Duration written as a string like "60s" or "5m".
type Duration struct{ time.Duration }

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil || n.Kind != yaml.ScalarNode {
		return &yaml.TypeError{Errors: []string{
			fmt.Sprintf("line %d: must be a duration like \"60s\" or \"5m\", got %q", n.Line, n.Value),
		}}
	}
	d.Duration = v
	return nil
}

// MarshalYAML implements yaml.Marshaler.
func (d Duration) MarshalYAML() (any, error) { return d.String(), nil }

// Default returns the built-in defaults. Values in the config file override
// them key by key.
func Default() *Config {
	return &Config{
		GitHub: GitHub{
			RefreshInterval: Duration{60 * time.Second},
			PRSections: []PRSection{
				{Title: "Needs my review", Query: "is:open review-requested:@me"},
				{Title: "Mine", Query: "is:open author:@me"},
			},
		},
		Plane: Plane{
			BaseURL:         "https://api.plane.so",
			APIKeyEnv:       "PLANE_API_KEY",
			RefreshInterval: Duration{2 * time.Minute},
		},
		Sessions: Sessions{
			WorktreeRoot:       filepath.Join(DataHome(), "forge", "worktrees"),
			NotifyAfterMinutes: 5,
			RefreshInterval:    Duration{10 * time.Second},
		},
		UI: UI{Animations: AnimationsFull},
	}
}

// DataHome returns $XDG_DATA_HOME, or ~/.local/share.
func DataHome() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return d
	}
	return filepath.Join(homeDir(), ".local", "share")
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "~"
}
