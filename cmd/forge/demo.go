package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/sessions/tmux/tmuxdemo"
	"github.com/daan3011/forge/internal/ui/anim"
	"github.com/daan3011/forge/internal/ui/anim/animdemo"
)

// newDemoCmd is the hidden `forge demo` group of development showcases.
func newDemoCmd() *cobra.Command {
	demo := &cobra.Command{
		Use:    "demo",
		Short:  "Development showcases",
		Hidden: true,
		Args:   cobra.NoArgs,
	}
	var level string
	animCmd := &cobra.Command{
		Use:   "anim",
		Short: "Show every forge animation side by side",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			lvl, err := demoLevel(level)
			if err != nil {
				return err
			}
			m := animdemo.New(anim.Resolve(lvl, os.Getenv))
			_, err = tea.NewProgram(m, tea.WithContext(cmd.Context())).Run()
			return err
		},
	}
	animCmd.Flags().StringVar(&level, "level", "", "full, reduced or off (default: ui.animations from the config)")
	tmuxCmd := &cobra.Command{
		Use:   "tmux",
		Short: "Run bash on forge's tmux server; attach with enter, detach with Alt+d",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			_, err = tea.NewProgram(tmuxdemo.New(tmux.New(), dir), tea.WithContext(cmd.Context())).Run()
			return err
		},
	}
	demo.AddCommand(animCmd, tmuxCmd)
	return demo
}

// demoLevel is the --level flag, or the configured level if the config can
// be read, or full. It never creates a config file.
func demoLevel(flag string) (config.Animations, error) {
	switch l := config.Animations(flag); l {
	case config.AnimationsFull, config.AnimationsReduced, config.AnimationsOff:
		return l, nil
	case "":
	default:
		return "", fmt.Errorf("--level: must be one of full, reduced, off; got %q", flag)
	}
	if p, err := config.Path(); err == nil {
		if cfg, err := config.Load(p); err == nil {
			return cfg.UI.Animations, nil
		}
	}
	return config.AnimationsFull, nil
}
