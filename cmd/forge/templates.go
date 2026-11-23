package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/daan3011/forge/internal/templates"
)

func newTemplatesCmd(_ deps) *cobra.Command {
	group := &cobra.Command{
		Use:   "templates",
		Short: "Manage session templates",
	}
	var dryRun bool
	sync := &cobra.Command{
		Use:   "sync",
		Short: "Add the forge tools section to your session templates that lack it",
		Long: `Appends the "forge tools" section (which tells the agent about forge's MCP
tools) to every template in forge's templates folder that doesn't have it yet,
and prints what it added. Templates that have it are left alone, so running it
again changes nothing.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := templates.Dir()
			if err != nil {
				return err
			}
			changes, err := templates.Sync(dir, dryRun)
			printSync(cmd.OutOrStdout(), changes, dryRun)
			return err
		},
	}
	sync.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without writing")
	group.AddCommand(sync)
	return group
}

func printSync(w io.Writer, changes []templates.Change, dryRun bool) {
	if len(changes) == 0 {
		_, _ = fmt.Fprintln(w, "all templates up to date")
		return
	}
	for _, c := range changes {
		_, _ = fmt.Fprint(w, c.Diff(), "\n")
	}
	verb := "updated"
	if dryRun {
		verb = "would update"
	}
	_, _ = fmt.Fprintf(w, "%s %d template(s)\n", verb, len(changes))
}
