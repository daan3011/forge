// Command forge is a keyboard-driven TUI for PRs, CI, Plane, Claude Code
// sessions, docs and artifacts.
package main

import (
	"os"

	"github.com/daan3011/forge/internal/editor"
)

func main() {
	if err := newRootCmd(deps{opener: editor.Exec{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}}).Execute(); err != nil {
		os.Exit(1)
	}
}
