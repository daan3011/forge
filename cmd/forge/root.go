package main

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/daan3011/forge/internal/app"
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/editor"
)

// deps are the external collaborators of the CLI, swapped for fakes in tests.
type deps struct {
	opener editor.Opener
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
			_, err = tea.NewProgram(app.New(cfg), tea.WithContext(cmd.Context())).Run()
			return err
		},
	}
	root.AddCommand(
		stubCmd("hook <event>", "Ingest a Claude Code hook event", "P1.4", cobra.ArbitraryArgs),
		stubCmd("capture [title]", "Quick-capture a Plane issue", "P3.5", cobra.ArbitraryArgs),
		stubCmd("new", "Start a new named agent session", "P1.3", cobra.NoArgs),
		newConfigCmd(d),
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
