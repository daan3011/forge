// Command forge is a keyboard-driven TUI for PRs, CI, Plane, Claude Code
// sessions, docs and artifacts.
package main

import "os"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
