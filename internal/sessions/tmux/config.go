package tmux

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"text/template"
	"time"
)

// ConfigVersion is the version of the embedded default config, written in
// its first line as "# forge-tmux-config v<N>". v2 added M-c (quick
// capture, P3.5).
const ConfigVersion = 2

// ConfigNews is what ConfigVersion added, for the "your config is old"
// notice.
const ConfigNews = "Alt+c capture"

// ConfigVersionOf reads the version header of the config at path; ok is
// false when the file is missing, unreadable or has no header.
func ConfigVersionOf(path string) (version int, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	return ParseConfigVersion(data)
}

//go:embed forge.tmux.conf.tmpl
var configTemplate string

var configTmpl = template.Must(template.New("tmux.conf").Parse(configTemplate))

// ConfigColors are the status line colours, as #rrggbb strings. They come
// from the forge theme (ui.TmuxColors) so hex values live in one place.
type ConfigColors struct {
	Bg, Fg, Accent, Muted string
}

// DefaultConfig renders the embedded config with the given colours.
func DefaultConfig(c ConfigColors) []byte {
	var b bytes.Buffer
	data := struct {
		ConfigColors
		Version int
	}{c, ConfigVersion}
	if err := configTmpl.Execute(&b, data); err != nil {
		panic(err) // static template; covered by tests
	}
	return b.Bytes()
}

var versionRe = regexp.MustCompile(`\A# forge-tmux-config v(\d+)\b`)

// ParseConfigVersion reads the version header from a config file's
// contents; ok is false when the header is missing.
func ParseConfigVersion(data []byte) (version int, ok bool) {
	m := versionRe.FindSubmatch(data)
	if m == nil {
		return 0, false
	}
	v, err := strconv.Atoi(string(m[1]))
	return v, err == nil
}

// EnsureConfig writes the default config to path unless a file is already
// there; an existing file is never touched. It reports whether it wrote.
func EnsureConfig(path string, c ConfigColors) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("tmux config: %w", err)
	}
	return true, writeConfig(path, DefaultConfig(c))
}

// ResetConfig replaces path with the default config. An existing file is
// first copied to "<path>.bak.<YYYYMMDD-HHMMSS>", whose path is returned
// (empty when there was nothing to back up).
func ResetConfig(path string, c ConfigColors, now time.Time) (backup string, err error) {
	old, err := os.ReadFile(path)
	switch {
	case err == nil:
		backup = path + ".bak." + now.Format("20060102-150405")
		if err := os.WriteFile(backup, old, 0o644); err != nil { //nolint:gosec // a config file, not a secret
			return "", fmt.Errorf("tmux config: back up: %w", err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("tmux config: %w", err)
	}
	return backup, writeConfig(path, DefaultConfig(c))
}

func writeConfig(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("tmux config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // a config file, not a secret
		return fmt.Errorf("tmux config: %w", err)
	}
	return nil
}
