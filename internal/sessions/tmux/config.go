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
	"strings"
	"text/template"
	"time"
)

// ConfigVersion is the version of the embedded default config, written in
// its first line as "# forge-tmux-config v<N>". v2 added M-c (quick
// capture, P3.5), v3 allow-passthrough (image previews, P4.7).
const ConfigVersion = 3

// DetachKey is the key, in tmux notation, that takes you from an agent
// window back to forge (detach-client, or closing the popup forge opened).
// The default config binds it and its status line names it; DetachKeyOf
// reads what a user's config actually binds.
const DetachKey = "M-d"

// CaptureKey is the quick capture key (forge capture in a popup), and
// CaptureSnippet the line that binds it in your own ~/.tmux.conf (forge
// never edits that file).
const (
	CaptureKey     = "M-c"
	CaptureSnippet = `bind -n ` + CaptureKey + ` display-popup -E -w 76 -h 8 "forge capture"`
)

// KeyLabel spells a tmux key for people: "M-d" is "Alt+d", "C-M-x"
// "Ctrl+Alt+x".
func KeyLabel(k string) string {
	var mods []string
	for len(k) > 2 && k[1] == '-' {
		switch k[0] {
		case 'M':
			mods = append(mods, "Alt")
		case 'C':
			mods = append(mods, "Ctrl")
		case 'S':
			mods = append(mods, "Shift")
		default:
			return strings.Join(append(mods, k), "+")
		}
		k = k[2:]
	}
	return strings.Join(append(mods, k), "+")
}

var detachBindRe = regexp.MustCompile(`(?m)^\s*bind(?:-key)?\s+(?:-[a-zA-Z]*n[a-zA-Z]*|-T\s+root)\s+(\S+)\s+detach-client\b`)

// DetachKeyOf returns the root-table key bound to detach-client in the
// config at path (the last such binding wins, as in tmux), or DetachKey
// when the file is missing or binds none.
func DetachKeyOf(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return DetachKey
	}
	return ParseDetachKey(data)
}

// ParseDetachKey is DetachKeyOf on a config's contents.
func ParseDetachKey(data []byte) string {
	k := DetachKey
	for _, m := range detachBindRe.FindAllSubmatch(data, -1) {
		k = strings.Trim(string(m[1]), `"'`)
	}
	return k
}

// ConfigNews is what ConfigVersion added, for the "your config is old"
// notice.
const ConfigNews = "image previews (allow-passthrough)"

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
		Version                        int
		DetachKey, CaptureKey, Capture string
	}{c, ConfigVersion, DetachKey, CaptureKey, CaptureSnippet}
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
