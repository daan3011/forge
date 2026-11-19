package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/daan3011/forge/internal/config"
)

func newConfigCmd(d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show or edit the forge config",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "path",
			Short: "Print the config file path",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				p, err := config.Path()
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), p)
				return err
			},
		},
		&cobra.Command{
			Use:   "edit",
			Short: "Open the config file in $EDITOR and validate it afterwards",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				p, err := config.Path()
				if err != nil {
					return err
				}
				if _, err := config.EnsureExists(p); err != nil {
					return err
				}
				if err := d.opener.Open(cmd.Context(), p); err != nil {
					return err
				}
				if _, err := config.Load(p); err != nil {
					return fmt.Errorf("%w\nrun `forge config edit` again to fix it", err)
				}
				_, err = fmt.Fprintln(cmd.ErrOrStderr(), "config ok:", p)
				return err
			},
		},
	)
	return cmd
}
