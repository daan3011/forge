package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/daan3011/forge/internal/app"
	"github.com/daan3011/forge/internal/config"
)

// newKeysCmd is the hidden `forge keys` command (P6.8): `--dump` prints
// every key binding, generated from the keymap, with its palette command.
func newKeysCmd(d deps) *cobra.Command {
	var dump, defaults bool
	var format string
	cmd := &cobra.Command{
		Use:    "keys",
		Short:  "List every key binding",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !dump {
				return errors.New("nothing to do: pass --dump")
			}
			if format != "md" && format != "json" {
				return fmt.Errorf("--format: want md or json, got %q", format)
			}
			cfg := config.Default()
			if !defaults {
				var err error
				if cfg, err = d.keysConfig(); err != nil {
					return err
				}
			}
			entries, err := app.KeyInventory(cfg)
			if err != nil {
				return err
			}
			out := app.RenderKeysMarkdown(entries)
			if format == "json" {
				if out, err = app.RenderKeysJSON(entries); err != nil {
					return err
				}
			}
			_, err = cmd.OutOrStdout().Write(out)
			return err
		},
	}
	cmd.Flags().BoolVar(&dump, "dump", false, "print every binding")
	cmd.Flags().StringVar(&format, "format", "md", "output format: md or json")
	cmd.Flags().BoolVar(&defaults, "defaults", false, "ignore config.yaml: the built-in bindings only")
	return cmd
}

// keysConfig is the user's config, or the defaults when there is none yet.
func (d deps) keysConfig() (*config.Config, error) {
	if d.loadConfig != nil {
		return d.loadConfig()
	}
	path, err := config.Path()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return config.Default(), nil
	}
	return config.Load(path)
}
