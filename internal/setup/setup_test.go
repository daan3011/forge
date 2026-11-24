package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/daan3011/forge/internal/clipboard"
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/doctor"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/projects"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/sessions/tmux/tmuxtest"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/testutil"
)

func TestParseStep(t *testing.T) {
	for _, s := range StepIDs() {
		if id, err := ParseStep(s); err != nil || string(id) != s {
			t.Errorf("ParseStep(%q) = %q, %v", s, id, err)
		}
	}
	if id, err := ParseStep(" Plane "); err != nil || id != StepPlane {
		t.Errorf("case/space: %q %v", id, err)
	}
	if _, err := ParseStep("nope"); err == nil || !strings.Contains(err.Error(), "deps, github, plane") {
		t.Errorf("unknown step: %v", err)
	}
}

// memStore is an in-memory Store.
type memStore struct {
	kv        map[string]string
	completed bool
}

func newMemStore() *memStore { return &memStore{kv: map[string]string{}} }

func (m *memStore) Get(_ context.Context, k string) (string, error) {
	v, ok := m.kv[k]
	if !ok {
		return "", store.ErrNotFound
	}
	return v, nil
}
func (m *memStore) Set(_ context.Context, k, v string) error { m.kv[k] = v; return nil }
func (m *memStore) MarkSetupCompleted(context.Context) error { m.completed = true; return nil }

func TestResultsRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := newMemStore()
	want := Result{Status: Failed, Detail: "no tmux", Fix: "brew install tmux", Lines: []string{"a"}}
	if err := SaveResult(ctx, st, StepDeps, want); err != nil {
		t.Fatal(err)
	}
	got := LoadResults(ctx, st)
	if len(got) != 1 || got[StepDeps].Status != Failed || got[StepDeps].Fix != want.Fix || got[StepDeps].Lines[0] != "a" {
		t.Errorf("LoadResults = %+v", got)
	}
	if _, ok := st.kv[store.SettingSetupStepPrefix+"deps"]; !ok {
		t.Errorf("key doesn't match the store's prefix: %v", st.kv)
	}
}

func TestSummary(t *testing.T) {
	rs := Results{
		StepDeps:  {Status: Done},
		StepPlane: {Status: Skipped},
		StepTmux:  {Status: Failed, Fix: "forge tmux config --reset"},
	}
	lines := Summary(rs, nil)
	var ids []StepID
	for _, l := range lines {
		ids = append(ids, l.Step.ID)
	}
	// Steps never run count as not done.
	want := []StepID{StepGitHub, StepPlane, StepProjects, StepTmux, StepAnimations, StepCapture, StepSmoke}
	if !slices.Equal(ids, want) {
		t.Errorf("ids = %v", ids)
	}
	for _, l := range lines {
		switch l.Step.ID {
		case StepPlane:
			if l.Fix != "forge setup plane" {
				t.Errorf("plane fix = %q", l.Fix)
			}
		case StepTmux:
			if l.Fix != "forge tmux config --reset  (then: forge setup tmux)" {
				t.Errorf("tmux fix = %q", l.Fix)
			}
		}
	}
	if only := Summary(rs, []StepID{StepPlane, StepDeps}); len(only) != 1 || only[0].Step.ID != StepPlane {
		t.Errorf("only = %+v", only)
	}
}

func TestDepsResult(t *testing.T) {
	tests := []struct {
		name   string
		checks []doctor.Check
		want   Status
		fix    string
	}{
		{"all ok", []doctor.Check{{Name: "git"}, {Name: "tmux"}}, Done, ""},
		{"optional missing", []doctor.Check{{Name: "git"}, {Name: "chafa", Status: doctor.Warn, Fix: "brew install chafa"}}, Done, ""},
		{"tmux old", []doctor.Check{
			{Name: "git"},
			{Name: "tmux", Status: doctor.Fail, Fix: "brew install tmux"},
			{Name: "claude", Status: doctor.Fail, Fix: "curl x"},
		}, Failed, "brew install tmux; curl x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := DepsResult(tt.checks)
			if r.Status != tt.want || r.Fix != tt.fix {
				t.Errorf("DepsResult = %+v", r)
			}
		})
	}
}

// The deps step runs the doctor's dependency checks, with per-OS hints.
func TestDepsWithDoctor(t *testing.T) {
	e := doctor.Env{
		GOOS: "linux", Distro: doctor.DistroFedora,
		LookPath: func(bin string) (string, error) {
			if bin == "tmux" || bin == "rsvg-convert" {
				return "", errors.New("missing")
			}
			return "/usr/bin/" + bin, nil
		},
		Output:       func(context.Context, string, ...string) (string, error) { return "v1", nil },
		Lstat:        os.Lstat,
		EvalSymlinks: filepath.EvalSymlinks,
	}
	cs := doctor.RunOnly(context.Background(), e, DepChecks...)
	if len(cs) != len(DepChecks) {
		t.Fatalf("ran %d checks", len(cs))
	}
	r := DepsResult(cs)
	if r.Status != Failed || !strings.Contains(r.Fix, "sudo dnf install tmux") {
		t.Errorf("result = %+v", r)
	}
	for _, c := range cs {
		if c.Name == doctor.NameRsvg && c.Fix != "sudo dnf install librsvg2-tools" {
			t.Errorf("rsvg fix = %q", c.Fix)
		}
	}
}

func TestGitHubResult(t *testing.T) {
	notLogged := []doctor.Check{{Name: "gh", Status: doctor.Fail, Detail: "not logged in", Fix: "gh auth login"}}
	if r := GitHubResult(notLogged); r.Status != Failed || !NeedsLogin(notLogged) {
		t.Errorf("not logged in: %+v", r)
	}
	missing := []doctor.Check{{Name: "gh", Status: doctor.Fail, Fix: "brew install gh"}}
	if NeedsLogin(missing) {
		t.Error("offers login without gh")
	}
	ok := []doctor.Check{{Name: "gh", Detail: "logged in as octo"}}
	if r := GitHubResult(ok); r.Status != Done || r.Detail != "logged in as octo" {
		t.Errorf("ok: %+v", r)
	}
	if r := GitHubResult(nil); r.Status != Failed {
		t.Errorf("no check: %+v", r)
	}
}

func TestPlaneInput(t *testing.T) {
	in := PlaneInputFrom(config.Default().Plane)
	if !in.Cloud || in.KeyEnv != "PLANE_API_KEY" {
		t.Errorf("defaults = %+v", in)
	}
	self := PlaneInputFrom(config.Plane{BaseURL: "https://plane.example.com/", APIKeyEnv: "K"})
	if self.Cloud || self.Base() != "https://plane.example.com" {
		t.Errorf("self-hosted = %+v", self)
	}
	tests := []struct {
		in PlaneInput
		ok bool
	}{
		{PlaneInput{Cloud: true, Workspace: "acme", KeyEnv: "PLANE_API_KEY"}, true},
		{PlaneInput{BaseURL: "https://plane.example.com", Workspace: "acme-co", KeyEnv: "K"}, true},
		{PlaneInput{BaseURL: "plane.example.com", Workspace: "acme", KeyEnv: "K"}, false},
		{PlaneInput{Cloud: true, Workspace: "", KeyEnv: "K"}, false},
		{PlaneInput{Cloud: true, Workspace: "Acme Co", KeyEnv: "K"}, false},
		{PlaneInput{Cloud: true, Workspace: "acme", KeyEnv: "1BAD"}, false},
		{PlaneInput{Cloud: true, Workspace: "acme", KeyEnv: "sk-123abc"}, false}, // a key, not a name
	}
	for _, tt := range tests {
		if err := tt.in.Validate(); (err == nil) != tt.ok {
			t.Errorf("Validate(%+v) = %v", tt.in, err)
		}
	}
	sets := PlaneInput{Cloud: true, Workspace: "acme", KeyEnv: "MY_KEY"}.Sets()
	for _, s := range sets {
		if s.Key == "plane.api_key" {
			t.Error("the key itself is written")
		}
	}
	if len(sets) != 3 || sets[0].Value != PlaneCloudURL || sets[2].Value != "MY_KEY" {
		t.Errorf("sets = %+v", sets)
	}
}

func TestTestPlane(t *testing.T) {
	fake := &testutil.FakePlane{MeUser: plane.User{DisplayName: "daan"}, ProjectList: []plane.Project{{ID: "p1", Name: "Forge"}}}
	var gotKey string
	var gotCfg config.Plane
	newPlane := func(c config.Plane, key string) (plane.API, error) { gotCfg, gotKey = c, key; return fake, nil }
	env := map[string]string{"MY_KEY": "secret"}
	getenv := func(k string) string { return env[k] }
	in := PlaneInput{BaseURL: "https://plane.example.com", Workspace: "acme", KeyEnv: "MY_KEY"}

	got, err := TestPlane(context.Background(), getenv, newPlane, in, config.Default().Plane)
	if err != nil {
		t.Fatal(err)
	}
	if got.User != "daan" || len(got.Projects) != 1 || gotKey != "secret" ||
		gotCfg.BaseURL != "https://plane.example.com" || gotCfg.Workspace != "acme" {
		t.Errorf("got %+v, key %q, cfg %+v", got, gotKey, gotCfg)
	}

	delete(env, "MY_KEY")
	if _, err := TestPlane(context.Background(), getenv, newPlane, in, config.Default().Plane); !errors.Is(err, ErrNoKey) {
		t.Errorf("missing key: %v", err)
	}
	bad := in
	bad.Workspace = ""
	if _, err := TestPlane(context.Background(), getenv, newPlane, bad, config.Default().Plane); err == nil || errors.Is(err, ErrNoKey) {
		t.Errorf("invalid input: %v", err)
	}
}

func TestShellLine(t *testing.T) {
	tests := []struct{ shell, rc, line string }{
		{"/bin/zsh", "~/.zshrc", `export PLANE_API_KEY="<your Plane API key>"`},
		{"/opt/homebrew/bin/bash", "~/.bashrc", `export PLANE_API_KEY="<your Plane API key>"`},
		{"/usr/bin/fish", "~/.config/fish/config.fish", `set -gx PLANE_API_KEY "<your Plane API key>"`},
		{"", "~/.zshrc", `export PLANE_API_KEY="<your Plane API key>"`},
	}
	for _, tt := range tests {
		rc, line := ShellLine(tt.shell, "PLANE_API_KEY")
		if rc != tt.rc || line != tt.line {
			t.Errorf("ShellLine(%q) = %q, %q", tt.shell, rc, line)
		}
	}
}

func TestRoots(t *testing.T) {
	home := "/Users/me"
	roots := AddRoot(nil, "/Users/me/code", home)
	roots = AddRoot(roots, "~/code", home) // same folder
	roots = AddRoot(roots, "/srv/repos/", home)
	if !slices.Equal(roots, []string{"~/code", "/srv/repos"}) {
		t.Errorf("AddRoot = %v", roots)
	}
	roots = RemoveRoot(roots, "/Users/me/code", home)
	if !slices.Equal(roots, []string{"/srv/repos"}) {
		t.Errorf("RemoveRoot = %v", roots)
	}
	if s := RootSets(nil); s[0].Key != "project_roots" || s[0].Value == nil {
		t.Errorf("RootSets(nil) = %+v", s)
	}
	if ContractHome("/Users/me", home) != "~" || ExpandHome("~", home) != home || ContractHome("/Users/mel/x", home) != "/Users/mel/x" {
		t.Error("home spelling")
	}
}

type fakeProjects struct {
	rows []store.Project
	set  map[string]string
}

func (f *fakeProjects) List(context.Context) ([]store.Project, error) { return f.rows, nil }
func (f *fakeProjects) SetPlane(_ context.Context, repo, id string) error {
	if f.set == nil {
		f.set = map[string]string{}
	}
	f.set[repo] = id
	return nil
}

// makeRepo writes a checkout with a GitHub origin under root.
func makeRepo(t *testing.T, root, name, repo string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	conf := "[remote \"origin\"]\n\turl = git@github.com:" + repo + ".git\n"
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDiscover(t *testing.T) {
	home := testutil.IsolateHome(t)
	root := filepath.Join(home, "work")
	makeRepo(t, root, "forge", "daan/forge")
	makeRepo(t, root, "api", "daan/api")
	makeRepo(t, root, "web", "daan/web")
	ps := []plane.Project{{ID: "p-forge", Name: "Forge", Identifier: "FORGE"}, {ID: "p-site", Name: "Site"}}
	st := &fakeProjects{rows: []store.Project{{Repo: "daan/web", PlaneProjectID: "p-site", PlaneDecided: true}}}

	d := Discover(context.Background(), []string{"~/work"}, nil, st, ps, home, home)
	if !d.Configured || len(d.Roots) != 1 || d.Roots[0] != root {
		t.Fatalf("roots = %v (configured %v)", d.Roots, d.Configured)
	}
	by := map[string]ProjectRow{}
	for _, r := range d.Rows {
		by[r.Repo] = r
	}
	if r := by["daan/forge"]; r.Source != projects.PlaneMatched || r.PlaneID != "p-forge" || r.LinkLabel() != "Forge (matched by name)" {
		t.Errorf("forge = %+v %q", r, r.LinkLabel())
	}
	if r := by["daan/web"]; r.Source != projects.PlaneLinked || r.LinkLabel() != "Site (linked)" {
		t.Errorf("web = %+v %q", r, r.LinkLabel())
	}
	if r := by["daan/api"]; r.Source != projects.PlaneUnresolved || r.LinkLabel() != "not linked" {
		t.Errorf("api = %+v", r)
	}

	n, err := SaveLinks(context.Background(), st, d.Rows)
	if err != nil || n != 1 || st.set["daan/forge"] != "p-forge" || len(st.set) != 1 {
		t.Errorf("SaveLinks = %d, %v, %v", n, err, st.set)
	}

	// Without configured roots the defaults are scanned.
	d = Discover(context.Background(), nil, nil, st, nil, home, home)
	if d.Configured {
		t.Error("defaults reported as configured")
	}
}

func TestTmuxConfState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tmux.conf")
	if s, _ := TmuxConfState(path); s != ConfMissing {
		t.Errorf("missing: %v", s)
	}
	colors := tmux.ConfigColors{Bg: "#000000", Fg: "#ffffff", Accent: "#ff7a1a", Muted: "#888888"}
	if _, err := tmux.EnsureConfig(path, colors); err != nil {
		t.Fatal(err)
	}
	if s, v := TmuxConfState(path); s != ConfCurrent || v != tmux.ConfigVersion {
		t.Errorf("current: %v %d", s, v)
	}
	if err := os.WriteFile(path, []byte("# forge-tmux-config v1\nset -g mouse on\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, v := TmuxConfState(path)
	if s != ConfOutdated || v != 1 || !strings.Contains(s.Describe(path, v), "forge ships v") {
		t.Errorf("outdated: %v %d", s, v)
	}
	if err := os.WriteFile(path, []byte("set -g mouse on\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, _ := TmuxConfState(path); s != ConfCustom {
		t.Errorf("custom: %v", s)
	}
}

func TestPopupTest(t *testing.T) {
	f := tmuxtest.NewFake()
	ctx := context.Background()
	a, err := StartPopupTest(ctx, f, "M-q")
	if err != nil {
		t.Fatal(err)
	}
	if a.Mode != tmux.AttachExec || a.Cmd == nil {
		t.Errorf("attachment = %+v", a)
	}
	target := tmux.Target{Session: "forge-setup", Window: "key-test"}
	_, cmd, _, ok := f.Started(target)
	if !ok || !strings.Contains(strings.Join(cmd, " "), "Press Alt+q") {
		t.Errorf("window cmd = %q", cmd)
	}
	if got := f.Attached(); len(got) != 1 || got[0] != target {
		t.Errorf("attached = %v", got)
	}
	// Starting again replaces the window.
	if _, err := StartPopupTest(ctx, f, "M-d"); err != nil {
		t.Fatal(err)
	}
	if err := StopPopupTest(ctx, f); err != nil {
		t.Fatal(err)
	}
	if ts, _ := f.ListAll(ctx); len(ts) != 0 {
		t.Errorf("windows left: %v", ts)
	}
	if s := PopupSocket(42); s != "forge-setup-42" {
		t.Errorf("socket = %q", s)
	}
}

func TestAnimationSets(t *testing.T) {
	for _, l := range AnimationLevels {
		s, err := AnimationSets(l)
		if err != nil || s[0].Key != "ui.animations" || s[0].Value != string(l) {
			t.Errorf("AnimationSets(%s) = %+v, %v", l, s, err)
		}
	}
	if _, err := AnimationSets("sometimes"); err == nil {
		t.Error("bad level accepted")
	}
}

func TestCopyCapture(t *testing.T) {
	f := &clipboard.Fake{}
	if err := CopyCapture(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if got := f.Copied(); len(got) != 1 || got[0] != `bind -n M-c display-popup -E -w 76 -h 8 "forge capture"` {
		t.Errorf("copied %q", got)
	}
	f.Err = clipboard.ErrNoTool
	if err := CopyCapture(context.Background(), f); !errors.Is(err, ErrNoClipboardTool) {
		t.Errorf("no tool: %v", err)
	}
	if err := CopyCapture(context.Background(), nil); !errors.Is(err, ErrNoClipboardTool) {
		t.Errorf("nil copier: %v", err)
	}
}
