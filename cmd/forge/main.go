// Command forge is a keyboard-driven TUI for PRs, CI, Plane, Claude Code
// sessions, docs and artifacts.
package main

import (
	"os"

	"charm.land/huh/v2"
	"github.com/charmbracelet/x/term"

	"github.com/daan3011/forge/internal/editor"
)

func main() {
	d := deps{
		opener:    editor.Exec{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr},
		providers: defaultProviders(),
	}
	if term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()) {
		d.runForm = func(f *huh.Form) error { return f.Run() }
	}
	if err := newRootCmd(d).Execute(); err != nil {
		os.Exit(1)
	}
}
