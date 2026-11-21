package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/daan3011/forge/internal/app"
	"github.com/daan3011/forge/internal/browser"
	"github.com/daan3011/forge/internal/clipboard"
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/delta"
	"github.com/daan3011/forge/internal/editor"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/notify"
	"github.com/daan3011/forge/internal/plane"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/alert"
	"github.com/daan3011/forge/internal/sessions/live"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/templates"
)

// deps are the external collaborators of the CLI, swapped for fakes in tests.
type deps struct {
	opener editor.Opener
	// newSessions builds the session service; nil means the real one.
	newSessions func(*config.Config, *store.Store) agent.API
	// runForm runs a Huh form on the terminal; nil means no terminal.
	runForm func(*huh.Form) error
	// providers resolve {issue_*} / {pr_*} template variables; nil has
	// none (main sets the GitHub and Plane ones).
	providers *templates.Registry
	// db is the database path; empty means store.DefaultPath().
	db string
	// newTmux builds the tmux driver for a config path; nil means the real
	// one on the forge socket.
	newTmux func(conf string) tmux.Driver
	// ask replaces the huh yes/no prompt; now replaces the clock.
	ask func(title, desc string) (bool, error)
	now func() time.Time
	// getenv replaces os.Getenv and sender the live socket, for `forge hook`.
	getenv func(string) string
	sender live.Sender
	// listen replaces live.Listen for the TUI; nil means the real socket.
	listen func() (pingSource, error)
	// notifier shows desktop notifications; nil means notify.System.
	notifier notify.Notifier
	// loadConfig replaces loading the config in `forge hook`.
	loadConfig func() (*config.Config, error)
	// newPlane builds the Plane client; nil means the real one.
	newPlane func(config.Plane, plane.Cache) (plane.API, error)
	// getwd and homeDir replace os.Getwd and os.UserHomeDir for project
	// detection.
	getwd   func() string
	homeDir func() string
}

func (d deps) sessions(cfg *config.Config, st *store.Store) (agent.API, error) {
	if d.newSessions != nil {
		return d.newSessions(cfg, st), nil
	}
	return newSessionService(cfg, st)
}

func (d deps) dbPath() string {
	if d.db != "" {
		return d.db
	}
	return store.DefaultPath()
}

// pingSource is the TUI's end of the live socket (*live.Listener).
type pingSource interface {
	Pings() <-chan live.Ping
	Close() error
}

func (d deps) listenLive(ctx context.Context) (pingSource, error) {
	if d.listen != nil {
		return d.listen()
	}
	return live.Listen(ctx, live.DefaultPath())
}

func newRootCmd(d deps) *cobra.Command {
	root := &cobra.Command{
		Use:           "forge",
		Short:         "One TUI for PRs, CI, Plane, agent sessions, docs and artifacts",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, path, created, err := config.LoadOrCreate()
			if err != nil {
				return err
			}
			if created {
				fmt.Fprintln(cmd.ErrOrStderr(), "created default config at", path)
			}
			st, err := store.Open(cmd.Context(), d.dbPath())
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			here := d.resolveProjects(cmd.Context(), cfg, st, cmd.ErrOrStderr())
			svc, err := d.sessions(cfg, st)
			if err != nil {
				return err
			}
			planeAPI, planeErr := d.planeClient(cfg.Plane, st.Cache())
			opts := []app.Option{
				app.WithPlane(planeAPI, planeErr), app.WithProjectStore(st.Projects()),
				app.WithStore(st.Cache()), app.WithSplash(), app.WithSessions(svc),
				app.WithBrowser(browser.Exec{}), app.WithGitHub(githubPulls), app.WithRuns(githubPulls),
				app.WithPulls(githubPulls),
				app.WithClipboard(clipboard.Exec{}), app.WithCheckout(newCheckout(cfg)),
				app.WithDelta(delta.Exec{Dark: true}),
				app.WithAlerts(alert.Checker{
					Store: st.Sessions(), Notifier: d.notifications(), Watcher: d.tmuxWatcher(),
					Policy: alert.PolicyFrom(cfg.Sessions),
				}),
			}
			switch src, err := d.listenLive(cmd.Context()); {
			case errors.Is(err, live.ErrRunning):
				fmt.Fprintln(cmd.ErrOrStderr(), "note: another forge is running and gets live session updates; this one refreshes every few seconds")
			case err != nil:
				fmt.Fprintln(cmd.ErrOrStderr(), "warning: no live session updates:", err)
			default:
				defer func() { _ = src.Close() }()
				opts = append(opts, app.WithLive(src.Pings()))
			}
			// Starting inside a repo opens its project; else the last one.
			switch name, found, err := app.LoadActiveProject(cmd.Context(), st.Cache()); {
			case here != "":
				opts = append(opts, app.WithActiveProject(here))
			case err != nil:
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", err)
			case found:
				opts = append(opts, app.WithActiveProject(name))
			}
			if dir, err := templates.Dir(); err == nil {
				opts = append(opts, app.WithTemplates(dir, d.providers))
			}
			model, err := app.New(cfg, opts...)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			for _, w := range model.Warnings() {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", w)
			}
			p := tea.NewProgram(model, tea.WithContext(cmd.Context()))
			githubPulls.OnResponse = func(resp *http.Response) {
				if msg := github.Observe(resp, time.Now()); msg != nil {
					p.Send(msg)
				}
			}
			_, err = p.Run()
			return err
		},
	}
	root.AddCommand(
		newHookCmd(d),
		stubCmd("capture [title]", "Quick-capture a Plane issue", "P3.5", cobra.ArbitraryArgs),
		newNewCmd(d),
		newConfigCmd(d),
		newTmuxCmd(d),
		newPlaneCmd(d),
		newDemoCmd(),
	)
	return root
}

// stubCmd returns a placeholder subcommand until its feature is built.
func stubCmd(use, short, feature string, args cobra.PositionalArgs) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  args,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.ErrOrStderr(), "forge %s: not implemented yet (%s)\n", cmd.Name(), feature)
			return err
		},
	}
}
