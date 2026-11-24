// Package testutil holds helpers shared by tests across packages.
package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// IsolateHome points HOME and the XDG base directories at fresh
// directories under t.TempDir(), so a test never reads or writes the real
// config, data, state or cache folders. It returns the fake home.
func IsolateHome(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	dirs := map[string]string{
		"HOME":            home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_DATA_HOME":   filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME":  filepath.Join(home, ".local", "state"),
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
	}
	// forge's own overrides would win over the XDG dirs: clear them.
	t.Setenv("FORGE_CONFIG_DIR", "")
	t.Setenv("FORGE_DATA_DIR", "")
	for k, v := range dirs {
		if err := os.MkdirAll(v, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv(k, v)
	}
	return home
}
