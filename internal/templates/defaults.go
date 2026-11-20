package templates

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/daan3011/forge/internal/config"
)

//go:embed defaults/*.md
var defaults embed.FS

// Dir returns the templates folder: <config dir>/templates.
func Dir() (string, error) {
	d, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "templates"), nil
}

// Defaults returns the built-in templates.
func Defaults() []Template {
	entries, _ := fs.ReadDir(defaults, "defaults")
	out := make([]Template, 0, len(entries))
	for _, e := range entries {
		data, _ := defaults.ReadFile("defaults/" + e.Name())
		t, err := Parse(e.Name(), data)
		if err != nil {
			panic(err) // embedded; covered by tests
		}
		out = append(out, t)
	}
	return out
}

// EnsureDefaults creates dir with the built-in templates if it doesn't
// exist yet. An existing dir is never touched, so edited templates are kept
// and deleted ones stay deleted. It reports whether it wrote.
func EnsureDefaults(dir string) (bool, error) {
	if _, err := os.Stat(dir); err == nil {
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("templates: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // config folder, not secret
		return false, fmt.Errorf("templates: %w", err)
	}
	entries, _ := fs.ReadDir(defaults, "defaults")
	for _, e := range entries {
		data, _ := defaults.ReadFile("defaults/" + e.Name())
		if err := os.WriteFile(filepath.Join(dir, e.Name()), data, 0o644); err != nil { //nolint:gosec // config file
			return true, fmt.Errorf("templates: %w", err)
		}
	}
	return true, nil
}
