package setup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/daan3011/forge/internal/config"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/claude"
	"github.com/daan3011/forge/internal/sessions/hooks"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/sessions/worktree"
	"github.com/daan3011/forge/internal/slashcmd"
	"github.com/daan3011/forge/internal/store"
)

// Smoke test timing.
const (
	// SmokeStall is how long without progress (before claude got the
	// prompt) until the test says claude is probably asking something.
	SmokeStall = 10 * time.Second
	// SmokeTimeout ends the test.
	SmokeTimeout = 90 * time.Second
	// SmokePoll is how often the session's status is read.
	SmokePoll = 500 * time.Millisecond
	// SmokePrompt is the throwaway session's first prompt.
	SmokePrompt = "This is forge's setup smoke test. Reply with just: ok"
)

// SmokePhase is where the smoke test is.
type SmokePhase int

// The phases.
const (
	SmokeWaiting    SmokePhase = iota // waiting for the hooks
	SmokeNeedsInput                   // claude seems to ask something (folder trust)
	SmokePassed                       // starting → working → idle
	SmokeTimedOut
	SmokeFailed // the session errored or died
)

// Observation is one look at the throwaway session.
type Observation struct {
	Status store.Status
	// Event is the last hook event forge recorded ("" before any).
	Event string
}

// SmokeState is the smoke test's state machine; Advance is pure.
type SmokeState struct {
	Phase      SmokePhase
	Started    time.Time
	Progress   time.Time // last time the status or event changed
	Status     store.Status
	Event      string
	SawWorking bool
}

// NewSmokeState starts the clock at now.
func NewSmokeState(now time.Time) SmokeState {
	return SmokeState{Phase: SmokeWaiting, Started: now, Progress: now, Status: store.StatusStarting}
}

// Done reports whether the test is over.
func (s SmokeState) Done() bool {
	return s.Phase == SmokePassed || s.Phase == SmokeTimedOut || s.Phase == SmokeFailed
}

// Advance folds in an observation made at now. It passes once the session
// worked on the prompt and went idle again (Stop), so every hook in the
// chain fired.
func (s SmokeState) Advance(o Observation, now time.Time) SmokeState {
	if s.Done() {
		return s
	}
	if o.Status != "" && (o.Status != s.Status || o.Event != s.Event) {
		s.Progress = now
		s.Status, s.Event = o.Status, o.Event
	}
	if s.Status == store.StatusWorking {
		s.SawWorking = true
	}
	switch {
	case s.SawWorking && s.Status == store.StatusIdle:
		s.Phase = SmokePassed
	case s.Status == store.StatusError || s.Status == store.StatusDead:
		s.Phase = SmokeFailed
	case now.Sub(s.Started) >= SmokeTimeout:
		s.Phase = SmokeTimedOut
	case s.Status == store.StatusWaiting:
		s.Phase = SmokeNeedsInput
	case !s.SawWorking && now.Sub(s.Progress) >= SmokeStall:
		s.Phase = SmokeNeedsInput
	default:
		s.Phase = SmokeWaiting
	}
	return s
}

// LastSeen describes the last thing the test saw, for a timeout report.
func (s SmokeState) LastSeen() string {
	ev := s.Event
	if ev == "" {
		ev = "no hook event yet"
	}
	return fmt.Sprintf("status %s, last event: %s", s.Status, ev)
}

// Result is the step result for a finished test; extra are the
// registration checks.
func (s SmokeState) Result(extra []string) Result {
	switch s.Phase {
	case SmokePassed:
		return Result{Status: Done, Detail: "hooks flipped the session starting → working → idle", Lines: extra}
	case SmokeFailed:
		return Result{
			Status: Failed, Detail: "the session ended with " + string(s.Status) + " (" + s.LastSeen() + ")",
			Fix: "forge doctor", Lines: extra,
		}
	case SmokeTimedOut:
		return Result{
			Status: Failed, Detail: "timed out after " + SmokeTimeout.String() + ": " + s.LastSeen(),
			Fix: "forge doctor; then check ~/.local/share/forge/hook.log", Lines: extra,
		}
	}
	return Result{Status: Pending, Detail: s.LastSeen()}
}

// SmokeRunner starts a smoke test run.
type SmokeRunner interface {
	Start(ctx context.Context) (SmokeRun, error)
}

// SmokeRun is a running throwaway session.
type SmokeRun interface {
	Observe(ctx context.Context) (Observation, error)
	// Preview is the bottom of the session's pane.
	Preview(ctx context.Context, lines int) (string, error)
	// Attach attaches to the session (to answer a prompt).
	Attach(ctx context.Context) (tmux.Attachment, error)
	// Checks reports forge's registrations for the session: hooks, the MCP
	// server and the skills root.
	Checks(ctx context.Context) []string
	// Cleanup removes the session, its tmux server and the temp repo.
	Cleanup(ctx context.Context) error
}

// SmokeSocket is the throwaway tmux socket of the smoke test.
func SmokeSocket(pid int) string { return "forge-smoke-" + strconv.Itoa(pid) }

// LiveSmoke runs the smoke test for real: a temp git repo, a forge session
// on a throwaway tmux server, real claude and real hooks into the forge
// database.
type LiveSmoke struct {
	Store      *store.Store
	ForgeBin   string
	ClaudeBin  string
	TmuxConf   string
	TmuxColors tmux.ConfigColors
	// DataDir is forge's data dir (session files).
	DataDir string
	// Skills syncs forge's skills (nil: none, and the skills check fails).
	Skills agent.SkillSyncer
	// NewTmux builds the driver on socket; nil means the tmux binary.
	NewTmux func(socket, conf string) tmux.Driver
	// Git runs git in dir; nil runs the git binary.
	Git func(ctx context.Context, dir string, args ...string) error
	// TempDir is where the repo goes; empty means os.TempDir().
	TempDir string
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// PID names the socket; 0 means os.Getpid().
	PID int
}

// smokeProject is the throwaway project's name.
const smokeProject = "forge-smoke"

// Start implements SmokeRunner.
func (l LiveSmoke) Start(ctx context.Context) (SmokeRun, error) {
	if l.Store == nil {
		return nil, errors.New("smoke test: no database")
	}
	now := time.Now
	if l.Now != nil {
		now = l.Now
	}
	pid := l.PID
	if pid == 0 {
		pid = os.Getpid()
	}
	dir, err := os.MkdirTemp(l.TempDir, "forge-smoke-")
	if err != nil {
		return nil, fmt.Errorf("smoke test: %w", err)
	}
	r := &liveRun{dir: dir, repo: filepath.Join(dir, "repo"), st: l.Store}
	ok := false
	defer func() {
		if !ok {
			_ = r.Cleanup(context.WithoutCancel(ctx))
		}
	}()
	if err := os.MkdirAll(r.repo, 0o755); err != nil {
		return nil, err
	}
	git := l.Git
	if git == nil {
		git = runGit
	}
	if err := git(ctx, r.repo, "init", "-q", "-b", "main"); err != nil {
		return nil, fmt.Errorf("smoke test: git init: %w", err)
	}
	if err := git(ctx, r.repo, "-c", "user.name=forge", "-c", "user.email=forge@localhost",
		"commit", "-q", "--allow-empty", "-m", "forge setup smoke test"); err != nil {
		return nil, fmt.Errorf("smoke test: git commit: %w", err)
	}

	newTmux := l.NewTmux
	if newTmux == nil {
		newTmux = func(socket, conf string) tmux.Driver {
			return tmux.New(tmux.WithSocket(socket), tmux.WithConfig(conf))
		}
	}
	r.socket = SmokeSocket(pid)
	r.drv = &recordingDriver{Driver: newTmux(r.socket, l.TmuxConf)}
	r.svc = agent.New(agent.Config{
		Tmux: r.drv, TmuxConf: l.TmuxConf, TmuxColors: l.TmuxColors,
		Worktrees: worktree.New(filepath.Join(dir, "worktrees"), worktree.NewExec()),
		Store:     agent.StoreFrom(l.Store),
		Projects:  []config.Project{{Name: smokeProject, Repo: "forge/smoke-test", Path: r.repo}},
		DataDir:   l.DataDir, ClaudeBin: l.ClaudeBin, ForgeBin: l.ForgeBin, Skills: l.Skills,
		Now: now,
	})
	r.name = "setup-smoke-" + strconv.FormatInt(now().Unix(), 10)
	res, err := r.svc.Start(ctx, agent.StartOpts{
		Name: r.name, Project: smokeProject, Prompt: SmokePrompt, Worktree: r.repo, Branch: "main",
	})
	if err != nil {
		return nil, fmt.Errorf("smoke test: start the session: %w", err)
	}
	r.x = res.Session
	ok = true
	return r, nil
}

func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// recordingDriver remembers the command of the window it starts, so the
// smoke test can check what claude was launched with.
type recordingDriver struct {
	tmux.Driver
	mu  sync.Mutex
	cmd []string
}

func (d *recordingDriver) NewWindow(ctx context.Context, session, name, dir string, cmd []string, env map[string]string) (tmux.Target, error) {
	d.mu.Lock()
	d.cmd = slices.Clone(cmd)
	d.mu.Unlock()
	return d.Driver.NewWindow(ctx, session, name, dir, cmd, env)
}

func (d *recordingDriver) launched() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return strings.Join(d.cmd, " ")
}

type liveRun struct {
	dir, repo, name, socket string
	st                      *store.Store
	drv                     *recordingDriver
	svc                     *agent.Service
	x                       store.Session
	once                    sync.Once
	cleanupErr              error
}

func (r *liveRun) Observe(ctx context.Context) (Observation, error) {
	x, err := r.st.Sessions().GetByName(ctx, r.name)
	if err != nil {
		return Observation{}, err
	}
	o := Observation{Status: x.Status}
	if ev, err := r.st.Events().Latest(ctx, x.ClaudeSessionID); err == nil {
		o.Event = ev.Event
	}
	return o, nil
}

func (r *liveRun) target() (tmux.Target, error) { return agent.Target(r.x) }

func (r *liveRun) Preview(ctx context.Context, lines int) (string, error) {
	t, err := r.target()
	if err != nil {
		return "", err
	}
	return r.drv.CapturePane(ctx, t, lines)
}

func (r *liveRun) Attach(ctx context.Context) (tmux.Attachment, error) {
	t, err := r.target()
	if err != nil {
		return tmux.Attachment{}, err
	}
	return r.drv.Attach(ctx, t)
}

func (r *liveRun) Checks(_ context.Context) []string {
	return RegistrationChecks(r.repo, r.svc.SessionDir(r.name), r.svc.SkillsRoot(r.name), r.drv.launched())
}

// RegistrationChecks reports what forge registered for a session: its
// hooks in the worktree, the MCP config (written, and passed with
// --mcp-config in launched, the window's command) and the synced skills
// root (passed with --add-dir).
func RegistrationChecks(worktreeDir, sessionDir, skillsRoot, launched string) []string {
	var out []string
	reg, err := hooks.Registered(worktreeDir)
	switch {
	case err != nil:
		out = append(out, "✗ hooks: "+err.Error())
	case len(reg) < len(hooks.Events):
		out = append(out, fmt.Sprintf("✗ hooks: %d of %d events registered", len(reg), len(hooks.Events)))
	default:
		out = append(out, fmt.Sprintf("✓ hooks: all %d events registered", len(hooks.Events)))
	}
	mcp := filepath.Join(sessionDir, claude.MCPConfigFile)
	_, statErr := os.Stat(mcp)
	switch {
	case errors.Is(statErr, fs.ErrNotExist):
		out = append(out, "✗ forge MCP server: no "+claude.MCPConfigFile+" for the session")
	case !strings.Contains(launched, "--mcp-config"):
		out = append(out, "✗ forge MCP server: claude wasn't started with --mcp-config")
	default:
		out = append(out, "✓ forge MCP server: registered (--mcp-config "+claude.MCPConfigFile+")")
	}
	_, statErr = os.Stat(filepath.Join(skillsRoot, slashcmd.ManifestFile))
	switch {
	case statErr != nil:
		out = append(out, "✗ skills: not synced to the session")
	case !strings.Contains(launched, "--add-dir"):
		out = append(out, "✗ skills: claude wasn't started with --add-dir")
	default:
		out = append(out, "✓ skills: synced (/forge:… commands)")
	}
	return out
}

// RegistrationsOK reports whether every line of RegistrationChecks passed.
func RegistrationsOK(lines []string) bool {
	return !slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, "✗") })
}

// Cleanup removes the session row, its files, the throwaway tmux server
// and the temp repo. It is safe to call more than once.
func (r *liveRun) Cleanup(ctx context.Context) error {
	r.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		var errs []error
		if r.drv != nil {
			errs = append(errs, discard(ctx, r.drv.Driver))
		}
		if r.svc != nil && r.name != "" {
			if x, err := r.st.Sessions().GetByName(ctx, r.name); err == nil {
				errs = append(errs, r.st.Sessions().Delete(ctx, x.ID))
			}
			errs = append(errs, os.RemoveAll(r.svc.SessionDir(r.name)))
		}
		errs = append(errs, os.RemoveAll(r.dir))
		r.cleanupErr = errors.Join(errs...)
	})
	return r.cleanupErr
}
