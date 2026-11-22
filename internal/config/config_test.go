package config

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// writeConfig writes body to a temp config file and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefault(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/data")
	d := Default()
	if d.GitHub.RefreshInterval.Duration != time.Minute {
		t.Errorf("refresh_interval = %s", d.GitHub.RefreshInterval)
	}
	want := []PRSection{
		{Title: "My PRs", Query: "is:open author:@me"},
		{Title: "Review requested", Query: "is:open review-requested:@me"},
		{Title: "Involved", Query: "is:open involves:@me"},
	}
	if !slices.Equal(d.GitHub.PRSections, want) {
		t.Errorf("pr_sections = %v", d.GitHub.PRSections)
	}
	if d.Plane.BaseURL != "https://api.plane.so" || d.Plane.APIKeyEnv != "PLANE_API_KEY" {
		t.Errorf("plane = %+v", d.Plane)
	}
	if d.Sessions.WorktreeRoot != "/data/forge/worktrees" {
		t.Errorf("worktree_root = %q", d.Sessions.WorktreeRoot)
	}
	if d.Plane.BoardDoneDays != 14 || (Plane{}).DoneDays() != 14 || (Plane{BoardDoneDays: 3}).DoneDays() != 3 {
		t.Errorf("board_done_days = %d", d.Plane.BoardDoneDays)
	}
	if d.Plane.RefreshInterval.Duration != 2*time.Minute || d.Sessions.RefreshInterval.Duration != 10*time.Second {
		t.Errorf("refresh intervals: plane %s, sessions %s", d.Plane.RefreshInterval, d.Sessions.RefreshInterval)
	}
	if d.GitHub.StaleAfterDays != 5 {
		t.Errorf("stale_after_days = %d, want 5", d.GitHub.StaleAfterDays)
	}
	if d.Sessions.NotifyAfterMinutes != 5 || !d.Sessions.NotifyOnError || !d.Sessions.NotifyOnFinish {
		t.Errorf("notify: after %d, on error %v, on finish %v",
			d.Sessions.NotifyAfterMinutes, d.Sessions.NotifyOnError, d.Sessions.NotifyOnFinish)
	}
	if d.UI.Animations != AnimationsFull {
		t.Errorf("animations = %q", d.UI.Animations)
	}
	if err := d.Validate(); err != nil {
		t.Errorf("defaults must validate: %v", err)
	}
}

func TestTemplateMatchesDefault(t *testing.T) {
	got, err := Load(writeConfig(t, string(Template())))
	if err != nil {
		t.Fatalf("template does not load: %v", err)
	}
	if want := Default(); !reflect.DeepEqual(got, want) {
		t.Fatalf("template and Default() drifted:\n got %+v\nwant %+v", got, want)
	}
}

func TestPartialFileKeepsDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, "github:\n  refresh_interval: 2m\nsessions:\n  refresh_interval: 30s\nui:\n  animations: off\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sessions.RefreshInterval.Duration != 30*time.Second {
		t.Errorf("sessions.refresh_interval = %s", cfg.Sessions.RefreshInterval)
	}
	if cfg.GitHub.RefreshInterval.Duration != 2*time.Minute {
		t.Errorf("refresh_interval = %s", cfg.GitHub.RefreshInterval)
	}
	if cfg.UI.Animations != AnimationsOff {
		t.Errorf("animations = %q", cfg.UI.Animations)
	}
	d := Default()
	if !reflect.DeepEqual(cfg.GitHub.PRSections, d.GitHub.PRSections) || !reflect.DeepEqual(cfg.Plane, d.Plane) {
		t.Errorf("omitted keys lost their defaults: %+v", cfg)
	}
}

func TestEmptyFileIsDefault(t *testing.T) {
	for _, body := range []string{"", "# just a comment\n"} {
		cfg, err := Load(writeConfig(t, body))
		if err != nil {
			t.Fatalf("%q: %v", body, err)
		}
		if !reflect.DeepEqual(cfg, Default()) {
			t.Fatalf("%q: got %+v", body, cfg)
		}
	}
}

func TestDurationRoundTrip(t *testing.T) {
	in := struct {
		D Duration `yaml:"d"`
	}{Duration{90 * time.Second}}
	b, err := yaml.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != "d: 1m30s" {
		t.Fatalf("marshal = %q", b)
	}
	var out struct {
		D Duration `yaml:"d"`
	}
	if err := yaml.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("round trip = %v, want %v", out, in)
	}
}

func TestProjectHelpers(t *testing.T) {
	p := Project{Repo: "daan/forge", Path: "/code/forge"}
	if p.Owner() != "daan" || p.RepoName() != "forge" {
		t.Errorf("owner/name = %q/%q", p.Owner(), p.RepoName())
	}
	tests := []struct{ docsDir, want string }{
		{"", "/code/forge/.forge/docs"},
		{"docs", "/code/forge/docs"},
		{"/elsewhere", "/elsewhere"},
	}
	for _, tt := range tests {
		p.DocsDir = tt.docsDir
		if got := p.DocsPath(); got != tt.want {
			t.Errorf("DocsPath(%q) = %q, want %q", tt.docsDir, got, tt.want)
		}
	}
}

func TestPlaneIssueURL(t *testing.T) {
	tests := []struct {
		p    Plane
		key  string
		want string
	}{
		{Plane{BaseURL: "https://plane.example.com/", Workspace: "acme"}, "PROJ-12", "https://plane.example.com/acme/browse/PROJ-12/"},
		{Plane{BaseURL: "https://app.plane.so", Workspace: "acme"}, "", ""},
		{Plane{BaseURL: "", Workspace: "acme"}, "PROJ-12", ""},
		{Plane{BaseURL: "https://app.plane.so"}, "PROJ-12", ""},
	}
	for _, tt := range tests {
		if got := tt.p.IssueURL(tt.key); got != tt.want {
			t.Errorf("IssueURL(%+v, %q) = %q, want %q", tt.p, tt.key, got, tt.want)
		}
	}
}

func TestPlaneIsBlocked(t *testing.T) {
	d := Default().Plane
	tests := []struct {
		name   string
		p      Plane
		labels []string
		state  string
		want   bool
	}{
		{"default label", d, []string{"ui", "blocked"}, "Todo", true},
		{"default label, other case", d, []string{"Blocked"}, "Todo", true},
		{"default state", d, nil, "blocked", true},
		{"neither", d, []string{"bug"}, "In Progress", false},
		{"no config", Plane{}, []string{"blocked"}, "Blocked", false},
		{"empty state never matches", Plane{BlockedStates: []string{""}}, nil, "", false},
		{"custom", Plane{BlockedLabels: []string{"waiting on review "}, BlockedStates: []string{"On hold"}}, []string{"Waiting on review"}, "", true},
		{"custom state", Plane{BlockedStates: []string{"On hold"}}, nil, "on HOLD", true},
	}
	for _, tt := range tests {
		if got := tt.p.IsBlocked(tt.labels, tt.state); got != tt.want {
			t.Errorf("%s: IsBlocked(%v, %q) = %v, want %v", tt.name, tt.labels, tt.state, got, tt.want)
		}
	}
}

func TestPlaneBlockedFromYAML(t *testing.T) {
	cfg, err := Load(writeConfig(t, "plane:\n  blocked_labels: [waiting, blocked-by-infra]\n  blocked_states: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Plane.BlockedLabels, []string{"waiting", "blocked-by-infra"}) || len(cfg.Plane.BlockedStates) != 0 {
		t.Fatalf("blocked = %v / %v", cfg.Plane.BlockedLabels, cfg.Plane.BlockedStates)
	}
	if d := Default().Plane; !reflect.DeepEqual(d.BlockedLabels, []string{"blocked"}) || !reflect.DeepEqual(d.BlockedStates, []string{"Blocked"}) {
		t.Fatalf("defaults = %v / %v", d.BlockedLabels, d.BlockedStates)
	}
}

// TestUsagePricesMerge: prices from the file are added to the shipped
// placeholders.
func TestUsagePricesMerge(t *testing.T) {
	cfg, err := Load(writeConfig(t, "usage:\n  prices:\n    claude-opus-5-5: {input: 1.5, output: 7}\n    mine: {cache_read: 0.1}\n"))
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Usage.Prices
	if p["claude-opus-5-5"] != (Price{Input: 1.5, Output: 7}) || p["mine"] != (Price{CacheRead: 0.1}) || !p["claude-haiku-5-5"].IsZero() {
		t.Errorf("prices = %+v", p)
	}
	if cfg.Usage.Days != DefaultUsageDays || cfg.Usage.TopSessions != DefaultUsageTopSessions {
		t.Errorf("usage = %+v", cfg.Usage)
	}
}
