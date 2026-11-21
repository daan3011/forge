package main

import (
	"fmt"
	"os"
	"testing"
)

// TestMain keeps project detection away from the real working directory
// (inside the forge repo) and home folder: by default it sees empty
// folders. Tests that exercise detection set deps.getwd / deps.homeDir.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "forge-cmd-test")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	osGetwd = func() string { return dir }
	osHome = func() string { return dir }
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
