package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultDocsDir is where a project's docs live, relative to its path, when
// docs_dir is not set.
const DefaultDocsDir = ".forge/docs"

// Config is the whole forge config file.
type Config struct {
	// ProjectRoots are folders scanned one level deep for git checkouts
	// with a GitHub remote; empty uses the defaults (internal/projects).
	ProjectRoots []string `yaml:"project_roots"`
	// Projects are optional overrides of detected projects, matched by
	// repo; entries for repos that aren't detected are added as is.
	Projects       []Project           `yaml:"projects"`
	GitHub         GitHub              `yaml:"github"`
	Plane          Plane               `yaml:"plane"`
	Sessions       Sessions            `yaml:"sessions"`
	Artifacts      Artifacts           `yaml:"artifacts"`
	Context        Context             `yaml:"context"`
	Usage          Usage               `yaml:"usage"`
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
	// PlaneDecided is set (never from YAML) once the Plane link is known:
	// PlaneProjectID set, or chosen to be none.
	PlaneDecided bool `yaml:"-"`
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
	// StaleAfterDays marks PRs not updated in this many days as stale (twice
	// as long: very stale). 0 turns the markers off.
	StaleAfterDays int `yaml:"stale_after_days"`
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
	// BoardDoneDays is how many days back the board's completed and
	// cancelled columns reach (by updated time).
	BoardDoneDays int `yaml:"board_done_days"`
	// BlockedLabels and BlockedStates mark open work items as blocked in
	// the cycle summary, by label or state name (case-insensitive).
	BlockedLabels []string `yaml:"blocked_labels"`
	BlockedStates []string `yaml:"blocked_states"`
	// CommentOnPRLink posts a comment with the PR link on a work item when
	// forge first links an open PR to it (P3.7).
	CommentOnPRLink bool `yaml:"comment_on_pr_link"`
}

// IsBlocked reports whether an item with these label names, in this state,
// counts as blocked: one of BlockedLabels or BlockedStates, ignoring case.
func (p Plane) IsBlocked(labels []string, state string) bool {
	fold := func(names []string, v string) bool {
		return v != "" && slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(strings.TrimSpace(n), v) })
	}
	if fold(p.BlockedStates, state) {
		return true
	}
	return slices.ContainsFunc(labels, func(l string) bool { return fold(p.BlockedLabels, l) })
}

// DefaultBoardDoneDays is plane.board_done_days when unset.
const DefaultBoardDoneDays = 14

// DoneDays is BoardDoneDays, or DefaultBoardDoneDays when unset.
func (p Plane) DoneDays() int {
	if p.BoardDoneDays <= 0 {
		return DefaultBoardDoneDays
	}
	return p.BoardDoneDays
}

// IssueURL is the web URL of the work item with the given key (e.g.
// PROJ-123), or "" when base_url or workspace_slug isn't configured.
func (p Plane) IssueURL(key string) string {
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" || p.Workspace == "" || key == "" {
		return ""
	}
	return base + "/" + url.PathEscape(p.Workspace) + "/browse/" + url.PathEscape(key) + "/"
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
	// NotifyOnError and NotifyOnFinish send a desktop notification when a
	// session hits an error or finishes its turn, while forge isn't focused.
	NotifyOnError  bool `yaml:"notify_on_error"`
	NotifyOnFinish bool `yaml:"notify_on_finish"`
	// RefreshInterval is how often session status refreshes in the background.
	RefreshInterval Duration `yaml:"refresh_interval"`
	// ArchiveCleanup says whether archiving a session (P1.10) also removes
	// its worktree and tmux window: ask, always or never.
	ArchiveCleanup ArchiveCleanup `yaml:"archive_cleanup"`
}

// ArchiveCleanup is what happens to an archived session's worktree and tmux
// window.
type ArchiveCleanup string

// Archive cleanup modes.
const (
	CleanupAsk    ArchiveCleanup = "ask"
	CleanupAlways ArchiveCleanup = "always"
	CleanupNever  ArchiveCleanup = "never"
)

// Artifacts holds the artifact gallery settings.
type Artifacts struct {
	Watch []string `yaml:"watch"`
	// Preview picks how the detail pane previews images (P4.7): auto
	// detects kitty graphics, then sixel, then chafa; kitty / sixel / chafa
	// force one; text shows HTML summaries and metadata only; off shows
	// metadata only. Empty means auto.
	Preview string `yaml:"preview"`
}

// Context holds the Context tab settings (P5.1).
type Context struct {
	// TokenBudget is the rough token count (chars/4) of startup memory
	// files above which the Context tab warns; 0 turns the warning off.
	TokenBudget int `yaml:"token_budget"`
}

// Usage holds the usage view settings (P5.4).
type Usage struct {
	// Days is how many days the usage view covers (the bar chart range).
	Days int `yaml:"days"`
	// TopSessions is how many sessions the top sessions list shows.
	TopSessions int `yaml:"top_sessions"`
	// Prices maps a model id (or id prefix) to its price. forge ships
	// placeholders only; a model whose prices are all 0 is unpriced.
	Prices map[string]Price `yaml:"prices"`
}

// Price is a model's price in USD per million tokens.
type Price struct {
	Input      float64 `yaml:"input"`
	Output     float64 `yaml:"output"`
	CacheRead  float64 `yaml:"cache_read"`
	CacheWrite float64 `yaml:"cache_write"`
}

// IsZero reports whether no price was filled in.
func (p Price) IsZero() bool { return p == Price{} }

// Usage defaults.
const (
	DefaultUsageDays        = 14
	DefaultUsageTopSessions = 10
	// MaxUsageDays caps usage.days.
	MaxUsageDays = 90
)

// DefaultTokenBudget is context.token_budget's default.
const DefaultTokenBudget = 8000

// PreviewModes are the valid artifacts.preview values.
var PreviewModes = []string{"auto", "kitty", "sixel", "chafa", "text", "off"}

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
				{Title: "My PRs", Query: "is:open author:@me"},
				{Title: "Review requested", Query: "is:open review-requested:@me"},
				{Title: "Involved", Query: "is:open involves:@me"},
			},
			StaleAfterDays: 5,
		},
		Plane: Plane{
			BaseURL:         "https://api.plane.so",
			APIKeyEnv:       "PLANE_API_KEY",
			RefreshInterval: Duration{2 * time.Minute},
			BoardDoneDays:   DefaultBoardDoneDays,
			BlockedLabels:   []string{"blocked"},
			BlockedStates:   []string{"Blocked"},
		},
		Sessions: Sessions{
			WorktreeRoot:       filepath.Join(DataHome(), "forge", "worktrees"),
			NotifyAfterMinutes: 5,
			NotifyOnError:      true,
			NotifyOnFinish:     true,
			RefreshInterval:    Duration{10 * time.Second},
			ArchiveCleanup:     CleanupAsk,
		},
		Artifacts: Artifacts{Preview: "auto"},
		Context:   Context{TokenBudget: DefaultTokenBudget},
		Usage: Usage{
			Days:        DefaultUsageDays,
			TopSessions: DefaultUsageTopSessions,
			Prices: map[string]Price{
				"claude-opus-5-5":   {},
				"claude-sonnet-5-5": {},
				"claude-haiku-5-5":  {},
			},
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

// NotesDir is where the daily notes live (P6.2): DataHome()/forge/notes,
// one YYYY-MM-DD.md per day.
func NotesDir() string { return filepath.Join(DataHome(), "forge", "notes") }

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "~"
}
