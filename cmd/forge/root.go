package main

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/daan3011/forge/internal/app"
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/editor"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/store"
)

// deps are the external collaborators of the CLI, swapped for fakes in tests.
type deps struct {
	opener editor.Opener
	// newSessions builds the session service; nil means the real one.
	newSessions func(*config.Config, *store.Store) agent.API
	// runForm runs a Huh form on the terminal; nil means no terminal.
	runForm func(*huh.Form) error
	// db is the database path; empty means store.DefaultPath().
	db string
	// newTmux builds the tmux driver for a config path; nil means the real
	// one on the forge socket.
	newTmux func(conf string) tmux.Driver
	// ask replaces the huh yes/no prompt; now replaces the clock.
	ask func(title, desc string) (bool, error)
	now func() time.Time
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
			svc, err := d.sessions(cfg, st)
			if err != nil {
				return err
			}
			opts := []app.Option{app.WithStore(st.Cache()), app.WithSplash(), app.WithSessions(svc)}
			switch name, found, err := app.LoadActiveProject(cmd.Context(), st.Cache()); {
			case err != nil:
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", err)
			case found:
				opts = append(opts, app.WithActiveProject(name))
			}
			model, err := app.New(cfg, opts...)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			_, err = tea.NewProgram(model, tea.WithContext(cmd.Context())).Run()
			return err
		},
	}
	root.AddCommand(
		stubCmd("hook <event>", "Ingest a Claude Code hook event", "P1.4", cobra.ArbitraryArgs),
		stubCmd("capture [title]", "Quick-capture a Plane issue", "P3.5", cobra.ArbitraryArgs),
		newNewCmd(d),
		newConfigCmd(d),
		newTmuxCmd(d),
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
