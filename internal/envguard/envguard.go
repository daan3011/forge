// Package envguard keeps forge's test-install tricks away from Claude Code.
// A temp-dir XDG_DATA_HOME / XDG_CONFIG_HOME (from a test run or a "clean
// install" try-out) must never reach claude: its auto-updater and settings
// would land in a folder that soon disappears and break the real install.
// forge has its own FORGE_CONFIG_DIR / FORGE_DATA_DIR for that instead.
package envguard

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// XDGVars are the XDG base directory variables forge checks.
var XDGVars = []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"}

// TempDirs are the folders counted as temporary.
func TempDirs() []string {
	dirs := []string{"/tmp", "/private/tmp", "/var/folders", "/private/var/folders"}
	if t := os.TempDir(); t != "" && !slices.Contains(dirs, filepath.Clean(t)) {
		dirs = append(dirs, filepath.Clean(t))
	}
	return dirs
}

// InTemp reports whether path lies in one of the temp folders.
func InTemp(path string) bool {
	if path == "" || !filepath.IsAbs(path) {
		return false
	}
	path = filepath.Clean(path)
	for _, d := range TempDirs() {
		if path == d || strings.HasPrefix(path, d+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// TempXDG returns the XDG variables that point into a temp folder, with
// their values, in XDGVars order.
func TempXDG(getenv func(string) string) [][2]string {
	var out [][2]string
	for _, k := range XDGVars {
		if v := getenv(k); InTemp(v) {
			out = append(out, [2]string{k, v})
		}
	}
	return out
}

// Strip drops temp-dir XDG variables from an environment (os.Environ form).
func Strip(env []string) []string {
	return slices.DeleteFunc(slices.Clone(env), func(kv string) bool {
		k, v, ok := strings.Cut(kv, "=")
		return ok && slices.Contains(XDGVars, k) && InTemp(v)
	})
}

// Shell is a sh prefix that unsets every XDG variable pointing into a temp
// folder, for commands run in a tmux window: the window inherits the
// environment of whichever process started the tmux server, which forge
// doesn't control. It ends in "; " so a command can follow directly.
const Shell = `for forge_v in XDG_CONFIG_HOME XDG_DATA_HOME XDG_STATE_HOME XDG_CACHE_HOME; do ` +
	`eval "forge_p=\${$forge_v-}"; ` +
	`case "$forge_p" in /tmp|/tmp/*|/private/tmp|/private/tmp/*|/var/folders/*|/private/var/folders/*) unset "$forge_v";; esac; ` +
	`if [ -n "${TMPDIR-}" ]; then case "$forge_p" in "${TMPDIR%/}"|"${TMPDIR%/}"/*) unset "$forge_v";; esac; fi; ` +
	`done; unset forge_v forge_p; `

// Passthrough are forge's own variables a child forge (a hook, the MCP
// server) needs to find the same config and database.
var Passthrough = []string{"FORGE_CONFIG_DIR", "FORGE_DATA_DIR"}

// ForgeEnv returns the Passthrough variables that are set, for a tmux
// window's environment.
func ForgeEnv(getenv func(string) string) map[string]string {
	out := map[string]string{}
	for _, k := range Passthrough {
		if v := getenv(k); v != "" {
			out[k] = v
		}
	}
	return out
}
