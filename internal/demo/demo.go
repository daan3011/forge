// Package demo runs forge on fictional, in-memory data: GitHub, Plane and
// agent sessions are fakes, and the projects, docs, artifacts and database
// live in one temp folder. `forge demo app` uses it, and so does the README
// GIF (docs/demo.tape), so a recording never shows real work.
package demo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/daan3011/forge/internal/app"
	"github.com/daan3011/forge/internal/browser"
	"github.com/daan3011/forge/internal/chafa"
	"github.com/daan3011/forge/internal/checkout"
	"github.com/daan3011/forge/internal/clipboard"
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/delta"
	"github.com/daan3011/forge/internal/doctor"
	"github.com/daan3011/forge/internal/proposals"
	"github.com/daan3011/forge/internal/rsvg"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/tmux/tmuxtest"
	"github.com/daan3011/forge/internal/sessions/worktree"
	"github.com/daan3011/forge/internal/slashcmd"
	"github.com/daan3011/forge/internal/store"
)

// Home is the folder under dir that stands in for $HOME: point HOME (and
// the XDG folders) there so nothing of the real user's shows up.
func Home(dir string) string { return filepath.Join(dir, "home") }

// Env are the environment variables that keep a demo run off the real
// home folder.
func Env(dir string) map[string]string {
	home := Home(dir)
	return map[string]string{
		"HOME":            home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_DATA_HOME":   filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME":  filepath.Join(home, ".local", "state"),
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		// forge's own overrides would win over the XDG folders.
		"FORGE_CONFIG_DIR": "",
		"FORGE_DATA_DIR":   "",
	}
}

// New lays the demo out in dir (an empty folder the caller removes) and
// returns its config and the app options that wire the fakes. Callers set
// Env(dir) first. close releases the database.
func New(ctx context.Context, dir string) (cfg *config.Config, opts []app.Option, closeFn func() error, err error) {
	now := time.Now()
	if err := writeFiles(dir, Home(dir)); err != nil {
		return nil, nil, nil, fmt.Errorf("demo: %w", err)
	}

	cfg = config.Default()
	cfg.Projects = []config.Project{
		{Name: "anvil", Repo: repoAnvil, Path: filepath.Join(dir, "anvil"), PlaneProjectID: planeProject, PlaneDecided: true},
		{Name: "bellows", Repo: repoBellows, Path: filepath.Join(dir, "bellows"), PlaneDecided: true},
	}
	cfg.Plane.Workspace = planeWorkspace

	st, err := store.Open(ctx, filepath.Join(dir, "forge.db"))
	if err != nil {
		return nil, nil, nil, err
	}
	gh := newGitHub(now)
	pl := newPlane(now)
	svc, err := newSessions(ctx, dir, cfg, st, gh, now)
	if err != nil {
		return nil, nil, nil, errors.Join(err, st.Close())
	}

	opts = []app.Option{
		app.WithStore(st.Cache()), app.WithProjectStore(st.Projects()),
		app.WithActiveProject(""), // all projects
		app.WithGitHub(gh), app.WithRuns(gh), app.WithPulls(gh),
		app.WithPlane(pl, nil),
		app.WithSessions(svc), app.WithLinks(st.Links()), app.WithArtifacts(st.Artifacts()),
		app.WithProposals(proposals.Queue{Store: st, Plane: pl, Projects: func() []config.Project { return cfg.Projects }}),
		app.WithBrowser(&browser.Fake{}), app.WithFiles(&browser.Fake{}), app.WithClipboard(&clipboard.Fake{}),
		app.WithCheckout(&checkout.Fake{Err: errDemo}),
		app.WithDelta(&delta.Fake{Missing: true}),
		app.WithPreviewTools(&chafa.Fake{Missing: true}, &rsvg.Fake{Missing: true}),
		app.WithSkills(slashcmd.NewCatalog()),
		app.WithNotes(filepath.Join(dir, "notes")),
		app.WithDoctor(doctorReport),
	}
	return cfg, opts, st.Close, nil
}

// session is one fictional agent.
type session struct {
	name, project, branch string
	status                store.Status
	idle                  time.Duration
	pane                  string
	prompt                string
	turns                 [][2]string
	pr                    int
	issue                 string
}

var sessions = []session{
	{
		name: "quench-retry", project: "anvil", branch: "quench-retry", status: store.StatusWorking, idle: 0, pr: 142, issue: "ANV-41",
		prompt: "Fix ANV-41: the quench step times out on cold mornings. Retry with backoff.",
		turns: [][2]string{
			{"I'll look at how `Quench` handles `ErrTooCold` first.", "go test ./temper/..."},
			{"The test reproduces it: three attempts, still too cold. I'll widen the backoff and add a case for it.", "go test ./temper/ -run Quench"},
		},
		pane: "● Running go test ./temper/ -run Quench…\n  ⎿ --- FAIL: TestQuenchRetriesWhenTooCold (0.31s)\n\n✻ Widening the backoff… (esc to interrupt)",
	},
	{
		name: "bellows-stream", project: "anvil", branch: "bellows-stream", status: store.StatusWaiting, idle: 6 * time.Minute, pr: 139, issue: "ANV-42",
		prompt: "Stream heat readings over the bellows API (ANV-42, see ADR 0002).",
		turns: [][2]string{
			{"The stream endpoint is in. Before I change the bellows client: may I run the integration suite? It needs Docker.", "make integration"},
		},
		pane: "Bash(make integration)\n  Runs the bellows integration suite (needs Docker)\n\n Do you want to proceed?\n ❯ 1. Yes\n   2. No, and tell Claude what to do differently",
	},
	{
		name: "airflow-presets", project: "bellows", branch: "forge/57", status: store.StatusIdle, idle: 48 * time.Minute, pr: 57, issue: "ANV-43",
		prompt: "Add airflow presets per alloy, one file per alloy in presets/.",
		turns: [][2]string{
			{"Presets for 1095 and 5160 are in `presets/`, with a table in .forge/docs/presets.md. Draft PR #57 is open.", ""},
		},
		pane: "✓ Draft PR ember-labs/bellows#57 opened.\n\n> ",
	},
}

// newSessions is the real session service over a fake tmux server, with
// three sessions in the database and their transcripts on disk.
func newSessions(ctx context.Context, dir string, cfg *config.Config, st *store.Store, gh *gitHub, now time.Time) (*agent.Service, error) {
	fake := tmuxtest.NewFake()
	projectsDir := filepath.Join(dir, "claude-projects")
	for i, s := range sessions {
		tsess, err := fake.EnsureSession(ctx, s.project)
		if err != nil {
			return nil, err
		}
		target, err := fake.NewWindow(ctx, tsess, s.name, "", []string{"claude"}, nil)
		if err != nil {
			return nil, err
		}
		fake.SetOutput(target, s.pane)
		wt := filepath.Join(dir, "worktrees", s.name)
		if err := os.MkdirAll(wt, 0o700); err != nil {
			return nil, err
		}
		id := fmt.Sprintf("0d3e%04d-5a1b-4c2d-8e9f-%012d", i, i+1)
		tpath := filepath.Join(projectsDir, s.name, id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(tpath), 0o700); err != nil {
			return nil, err
		}
		start := now.Add(-s.idle - 20*time.Minute)
		if err := os.WriteFile(tpath, []byte(transcript(id, start, s.prompt, s.turns)), 0o600); err != nil {
			return nil, err
		}
		repo := repoAnvil
		if s.project == "bellows" {
			repo = repoBellows
		}
		x, err := st.Sessions().Create(ctx, store.Session{
			Name: s.name, ClaudeSessionID: id, Project: s.project, Repo: repo, BaseBranch: "main",
			Branch: s.branch, Worktree: wt, TmuxSession: target.Session, TmuxWindow: target.Window,
			Status: s.status, StatusChangedAt: now.Add(-s.idle), LastActivityAt: now.Add(-s.idle),
			TranscriptPath: tpath, BranchCreated: true,
		})
		if err != nil {
			return nil, err
		}
		if p, ok := gh.find(repo, s.pr); ok {
			if err := st.Links().Add(ctx, store.SessionRef(x.ID), store.PRRef(repo, s.pr),
				store.LinkMeta{URL: p.URL, Title: p.Title, State: p.State}); err != nil {
				return nil, err
			}
		}
		if err := st.Links().Add(ctx, store.SessionRef(x.ID), store.IssueRef(s.issue),
			store.LinkMeta{Title: s.prompt, State: "In Progress"}); err != nil {
			return nil, err
		}
	}
	return agent.New(agent.Config{
		Tmux:        fake,
		TmuxConf:    filepath.Join(dir, "tmux.conf"),
		Worktrees:   worktree.New(filepath.Join(dir, "worktrees"), worktree.NewExec()),
		Store:       agent.StoreFrom(st),
		Projects:    cfg.Projects,
		DataDir:     filepath.Join(dir, "data"),
		ProjectsDir: projectsDir,
		Pulls:       gh,
	}), nil
}

// doctorReport is a healthy machine's `forge doctor`.
func doctorReport(context.Context) (string, bool) {
	var b strings.Builder
	_ = doctor.Render(&b, []doctor.Check{
		{Name: "forge", Detail: "v0.1.0 at ~/.local/bin/forge"},
		{Name: "config", Detail: "~/.config/forge/config.yaml"},
		{Name: "database", Detail: "~/.local/share/forge/forge.db"},
		{Name: "git", Detail: "git version 2.47.0"},
		{Name: "gh", Detail: "logged in as " + viewer},
		{Name: "tmux", Detail: "tmux 3.5"},
		{Name: "claude", Detail: "2.1.0 (Claude Code)"},
		{Name: "delta", Detail: "/usr/local/bin/delta"},
		{Name: "chafa", Detail: "/usr/local/bin/chafa"},
		{Name: "plane", Detail: "connected as " + viewer},
		{Name: "hooks", Detail: "3 active session(s) up to date"},
	}, doctor.Styles{})
	return b.String(), false
}
