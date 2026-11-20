package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daan3011/forge/internal/config"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/templates"
)

// fakeSessions records Start calls.
type fakeSessions struct {
	agent.API
	got []agent.StartOpts
}

func (f *fakeSessions) Start(_ context.Context, o agent.StartOpts) (agent.Started, error) {
	f.got = append(f.got, o)
	return agent.Started{Session: store.Session{
		Name: o.Name, Branch: "agent/" + o.Name, Worktree: "/wt/" + o.Name,
		TmuxSession: o.Project, TmuxWindow: o.Name,
	}}, nil
}

func newEnvCLI(t *testing.T, projects string) (deps, *fakeSessions) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_DATA_HOME", dir)
	cfgDir := filepath.Join(dir, "forge")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(projects), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := &fakeSessions{}
	return deps{
		db:          filepath.Join(dir, "forge.db"),
		newSessions: func(*config.Config, *store.Store) agent.API { return fake },
	}, fake
}

const twoProjects = `projects:
  - {name: forge, repo: daan/forge, path: /tmp}
  - {name: other, repo: daan/other, path: /tmp}
`

func TestNewWithFlags(t *testing.T) {
	d, fake := newEnvCLI(t, twoProjects)
	stdout, _, err := run(t, d, "new", "--name", "fix-ci", "--project", "other", "--base", "dev", "--prompt", "fix \"it\"\n$HOME")
	if err != nil {
		t.Fatal(err)
	}
	want := agent.StartOpts{Name: "fix-ci", Project: "other", Base: "dev", Prompt: "fix \"it\"\n$HOME"}
	if len(fake.got) != 1 || fake.got[0] != want {
		t.Fatalf("Start = %+v, want %+v", fake.got, want)
	}
	if !strings.Contains(stdout, "started fix-ci on agent/fix-ci") || !strings.Contains(stdout, "tmux -L forge attach -t =other:=fix-ci") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestNewPromptFromStdinAndSingleProject(t *testing.T) {
	d, fake := newEnvCLI(t, "projects:\n  - {name: forge, repo: daan/forge, path: /tmp}\n")
	cmd := newRootCmd(d)
	cmd.SetArgs([]string{"new", "--name", "p", "--prompt", "-"})
	cmd.SetIn(strings.NewReader("multi\nline\n"))
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(fake.got) != 1 || fake.got[0].Project != "forge" || fake.got[0].Prompt != "multi\nline\n" {
		t.Fatalf("Start = %+v", fake.got)
	}
}

func TestNewWithoutTerminalNeedsFlags(t *testing.T) {
	d, fake := newEnvCLI(t, twoProjects)
	_, _, err := run(t, d, "new", "--name", "x")
	if err == nil || !strings.Contains(err.Error(), "--name and --project are required") {
		t.Fatalf("err = %v", err)
	}
	if len(fake.got) != 0 {
		t.Errorf("started: %+v", fake.got)
	}
}

func TestNewWithTemplateAndVars(t *testing.T) {
	d, fake := newEnvCLI(t, twoProjects)
	_, _, err := run(t, d, "new", "--name", "q", "--project", "forge", "--template", "explore",
		"--var", "question=how does resume work?")
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.got) != 1 {
		t.Fatalf("Start = %+v", fake.got)
	}
	got := fake.got[0]
	if got.Template != "explore" || !strings.HasPrefix(got.Prompt, "Explore daan/forge and answer: how does resume work?") {
		t.Errorf("Start = %+v", got)
	}
}

func TestNewWithTemplateMissingVarsWithoutTerminal(t *testing.T) {
	d, fake := newEnvCLI(t, twoProjects)
	_, _, err := run(t, d, "new", "--name", "i", "--project", "forge", "--template", "implement-issue", "--issue", "PROJ-1")
	if err == nil || !strings.Contains(err.Error(), "needs issue_title, issue_body") {
		t.Fatalf("err = %v", err)
	}
	if len(fake.got) != 0 {
		t.Errorf("started: %+v", fake.got)
	}
}

func TestNewWithTemplateProvider(t *testing.T) {
	d, fake := newEnvCLI(t, twoProjects)
	d.providers = templates.NewRegistry()
	d.providers.Register(store.KindPlaneIssue, templates.ProviderFunc(func(_ context.Context, ref string) (map[string]string, error) {
		return map[string]string{"issue_title": "Title " + ref, "issue_body": "Body"}, nil
	}))
	if _, _, err := run(t, d, "new", "--name", "i", "--project", "forge", "--template", "implement-issue", "--issue", "PROJ-1"); err != nil {
		t.Fatal(err)
	}
	if len(fake.got) != 1 || !strings.HasPrefix(fake.got[0].Prompt, "Implement issue PROJ-1: Title PROJ-1\n\nBody") {
		t.Fatalf("Start = %+v", fake.got)
	}
}

func TestNewTemplateErrors(t *testing.T) {
	d, _ := newEnvCLI(t, twoProjects)
	if _, _, err := run(t, d, "new", "--name", "x", "--project", "forge", "--template", "nope"); err == nil ||
		!strings.Contains(err.Error(), `no template called "nope"`) {
		t.Errorf("unknown template: %v", err)
	}
	if _, _, err := run(t, d, "new", "--name", "x", "--project", "forge", "--var", "novalue"); err == nil ||
		!strings.Contains(err.Error(), "use name=value") {
		t.Errorf("bad --var: %v", err)
	}
}

func TestNewPromptWinsOverTemplate(t *testing.T) {
	d, fake := newEnvCLI(t, twoProjects)
	if _, _, err := run(t, d, "new", "--name", "x", "--project", "forge", "--template", "explore", "--prompt", "mine"); err != nil {
		t.Fatal(err)
	}
	if len(fake.got) != 1 || fake.got[0].Prompt != "mine" || fake.got[0].Template != "explore" {
		t.Fatalf("Start = %+v", fake.got)
	}
}

func TestNewUnknownTemplateWithPrompt(t *testing.T) {
	d, _ := newEnvCLI(t, twoProjects)
	if _, _, err := run(t, d, "new", "--name", "x", "--project", "forge", "--template", "nope", "--prompt", "p"); err == nil {
		t.Error("unknown template accepted")
	}
}
