package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/doctor"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/plane"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/ui"
)

// errDoctorFailed makes `forge doctor` exit 1 without printing more.
var errDoctorFailed = errors.New("some checks failed")

func newDoctorCmd(d deps) *cobra.Command {
	var fix, asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check forge's tools, logins and session registrations, and print fixes",
		Long: `Check that everything forge drives works: git, gh (and its login), tmux
(3.2 or newer), claude, the optional delta, chafa and rsvg-convert, Plane
when it's set up, the config and database, and for every active agent
session its hooks, MCP config and skills.

--fix writes the default config and tmux config when they're missing and
re-registers hooks, MCP config and skills for sessions that need it (the
same way resuming does). It exits 1 when a check fails; warnings don't.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			if fix {
				if err := d.doctorFix(ctx, cmd.ErrOrStderr()); err != nil {
					return err
				}
			}
			checks := d.runDoctor(ctx)
			if fix {
				if err := d.fixSessions(ctx, doctor.FixSessions(checks), cmd.ErrOrStderr()); err != nil {
					return err
				}
				if len(doctor.FixSessions(checks)) > 0 {
					checks = d.runDoctor(ctx)
				}
			}
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(checks); err != nil {
					return err
				}
			} else if err := doctor.Render(out, checks, doctorStyles(out)); err != nil {
				return err
			}
			if doctor.Failed(checks) {
				cmd.SilenceErrors = true
				return errDoctorFailed
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "write missing default configs and re-register sessions' hooks, MCP config and skills")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the checks as JSON")
	return cmd
}

// runDoctor runs every check against the real environment (d's fakes in
// tests).
func (d deps) runDoctor(ctx context.Context) []doctor.Check {
	e, cleanup := d.doctorEnv(ctx)
	defer cleanup()
	return doctor.Run(ctx, e)
}

// doctorEnv builds the doctor's view of this machine. It never writes:
// the database is only opened when it exists, and tmux runs with -V.
func (d deps) doctorEnv(ctx context.Context) (doctor.Env, func()) {
	cleanup := func() {}
	home, _ := os.UserHomeDir()
	e := doctor.Env{
		GOOS:         runtime.GOOS,
		Distro:       hostDistro(),
		Getenv:       os.Getenv,
		LookPath:     exec.LookPath,
		Output:       commandOutput,
		Lstat:        os.Lstat,
		EvalSymlinks: filepath.EvalSymlinks,
		TempDirs:     []string{os.TempDir(), "/tmp", "/var/folders"},
		Home:         home,
		Version:      version(),
		Bin:          forgeBin(),
		PathDirs:     filepath.SplitList(os.Getenv("PATH")),
		StorePath:    d.dbPath(),
		GitHubViewer: githubPulls.Viewer,
		IsGitHubAuth: github.IsAuthError,
		IsPlaneAuth:  plane.IsAuthError,
	}
	if p, err := config.Path(); err == nil {
		e.ConfigPath = p
	}
	cfg, err := d.hookConfig()
	switch {
	case err != nil:
		e.ConfigErr = err
	case e.ConfigPath != "":
		if _, serr := os.Stat(e.ConfigPath); errors.Is(serr, fs.ErrNotExist) {
			e.ConfigErr = serr
		}
	}
	if p, err := config.TmuxConfPath(); err == nil {
		e.TmuxConf = p
	}
	tx := tmux.New(tmux.WithConfig(e.TmuxConf))
	e.TmuxVersion, e.TmuxServerConfig = tx.Version, tx.ServerConfigFiles

	if cfg.Plane.Workspace != "" {
		e.PlaneConfigured = true
		if c, err := d.planeClient(cfg.Plane, nil); err != nil {
			e.PlaneErr = err
		} else {
			e.PlaneMe = func(ctx context.Context) (string, error) {
				u, err := c.Me(ctx)
				if u.DisplayName == "" {
					u.DisplayName = u.Email
				}
				return u.DisplayName, err
			}
		}
	}

	e.SkillsEnabled = true
	// A fresh install has no database (and no sessions) until forge runs.
	if _, err := os.Stat(e.StorePath); errors.Is(err, fs.ErrNotExist) {
		e.StoreMissing = true
	} else {
		st, err := store.Open(ctx, e.StorePath)
		if err != nil {
			e.StoreErr = err
			e.SessionsErr = err
		} else {
			cleanup = func() { _ = st.Close() }
			e.Sessions, e.SessionsErr = activeSessions(ctx, d.doctorService(cfg, st), st)
		}
	}
	if d.doctorFake != nil {
		d.doctorFake(&e)
	}
	return e, cleanup
}

// doctorService is the session service --fix re-prepares sessions with.
// Its tmux driver never starts the server or writes the tmux config.
func (d deps) doctorService(cfg *config.Config, st *store.Store) *agent.Service {
	conf, _ := config.TmuxConfPath()
	return agent.New(sessionConfig(cfg, st, tmux.New(tmux.WithConfig(conf)), conf))
}

// activeSessions are the sessions that aren't archived, with the paths
// the doctor checks.
func activeSessions(ctx context.Context, svc *agent.Service, st *store.Store) ([]doctor.Session, error) {
	archived := false
	xs, err := st.Sessions().List(ctx, store.SessionFilter{Archived: &archived})
	if err != nil {
		return nil, err
	}
	var out []doctor.Session
	for _, x := range xs {
		if x.Status == store.StatusArchived {
			continue
		}
		if fi, err := os.Stat(x.Worktree); err != nil || !fi.IsDir() {
			continue // gone: resume offers to recreate it
		}
		out = append(out, doctor.Session{
			Name: x.Name, Worktree: x.Worktree,
			SessionDir: svc.SessionDir(x.Name), SkillsRoot: svc.SkillsRoot(x.Name),
		})
	}
	return out, nil
}

// doctorFix writes the default config and tmux config when missing.
func (d deps) doctorFix(_ context.Context, w io.Writer) error {
	_, path, created, err := config.LoadOrCreate()
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintln(w, "fixed: wrote the default config at", path)
	}
	conf, err := config.TmuxConfPath()
	if err != nil {
		return err
	}
	wrote, err := tmux.EnsureConfig(conf, tmuxColors())
	if err != nil {
		return err
	}
	if wrote {
		fmt.Fprintln(w, "fixed: wrote forge's tmux config at", conf)
	}
	return nil
}

// fixSessions re-prepares names the way resuming them would.
func (d deps) fixSessions(ctx context.Context, names []string, w io.Writer) error {
	if len(names) == 0 {
		return nil
	}
	cfg, err := d.hookConfig()
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, d.dbPath())
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	prepare := d.doctorPrepare
	if prepare == nil {
		svc := d.doctorService(cfg, st)
		prepare = func(ctx context.Context, x store.Session) error {
			p, err := svc.Prepare(ctx, x)
			return errors.Join(err, p.SkillsErr)
		}
	}
	for _, name := range names {
		x, err := st.Sessions().GetByName(ctx, name)
		if err == nil {
			err = prepare(ctx, x)
		}
		if err != nil {
			fmt.Fprintf(w, "couldn't fix %s: %v\n", name, err)
			continue
		}
		fmt.Fprintln(w, "fixed: re-registered hooks, MCP config and skills for", name)
	}
	return nil
}

// hostDistro is the Linux package manager family from /etc/os-release.
func hostDistro() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	return doctor.DetectDistro(string(b))
}

// commandOutput runs bin with args and returns its trimmed output. claude
// never updates itself here.
func commandOutput(ctx context.Context, bin string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, bin, args...)
	c.Env = append(os.Environ(), "DISABLE_AUTOUPDATER=1")
	out, err := c.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		if s != "" {
			return "", fmt.Errorf("%s: %s", filepath.Base(bin), s)
		}
		return "", err
	}
	return s, nil
}

// doctorStyles colours the report in forge's theme on a terminal, and
// prints plain text otherwise or with NO_COLOR.
func doctorStyles(w io.Writer) doctor.Styles {
	f, ok := w.(*os.File)
	if !ok || os.Getenv("NO_COLOR") != "" || !isTerminal(f) {
		return doctor.Styles{}
	}
	th := ui.NewTheme(lipgloss.HasDarkBackground(os.Stdin, f), nil)
	r := func(s lipgloss.Style) func(string) string { return func(v string) string { return s.Render(v) } }
	return doctor.Styles{
		OK: r(th.DoctorOK), Warn: r(th.DoctorWarn), Fail: r(th.DoctorFail), Skip: r(th.Hint), Hint: r(th.Hint),
	}
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
