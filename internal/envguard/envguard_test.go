package envguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestInTemp(t *testing.T) {
	tests := map[string]bool{
		"/tmp":                           true,
		"/tmp/x/data":                    true,
		"/private/tmp/x":                 true,
		"/var/folders/ab/T/x":            true,
		"/private/var/folders/ab/T/x":    true,
		"/Users/me/.local/share":         false,
		"/tmpfoo":                        false,
		"relative/tmp":                   false,
		"":                               false,
		filepath.Join(os.TempDir(), "x"): true,
	}
	for p, want := range tests {
		if got := InTemp(p); got != want {
			t.Errorf("InTemp(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestTempXDGAndStrip(t *testing.T) {
	env := map[string]string{"XDG_DATA_HOME": "/tmp/x", "XDG_CONFIG_HOME": "/Users/me/.config", "XDG_CACHE_HOME": "/private/tmp/c"}
	got := TempXDG(func(k string) string { return env[k] })
	want := [][2]string{{"XDG_DATA_HOME", "/tmp/x"}, {"XDG_CACHE_HOME", "/private/tmp/c"}}
	if !slices.Equal(got, want) {
		t.Errorf("TempXDG = %v", got)
	}
	in := []string{"HOME=/Users/me", "XDG_DATA_HOME=/tmp/x", "XDG_CONFIG_HOME=/Users/me/.config", "FOO=/tmp/x"}
	if out := Strip(in); !slices.Equal(out, []string{"HOME=/Users/me", "XDG_CONFIG_HOME=/Users/me/.config", "FOO=/tmp/x"}) {
		t.Errorf("Strip = %v", out)
	}
	if len(in) != 4 {
		t.Error("Strip changed its input")
	}
}

// The shell guard really unsets temp-dir XDG variables and keeps others.
func TestShell(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	cmd := exec.CommandContext(t.Context(), "sh", "-c", Shell+`printf '%s|%s|%s|%s' "${XDG_DATA_HOME-unset}" "${XDG_CONFIG_HOME-unset}" "${XDG_STATE_HOME-unset}" "${forge_v-gone}"`)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"TMPDIR=/var/tmpx/",
		"XDG_DATA_HOME=/tmp/forge-test/data",
		"XDG_CONFIG_HOME=/Users/me/.config",
		"XDG_STATE_HOME=/var/tmpx/state",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "unset|/Users/me/.config|unset|gone" {
		t.Errorf("got %q", got)
	}
}

func TestForgeEnv(t *testing.T) {
	env := map[string]string{"FORGE_DATA_DIR": "/clean/data"}
	got := ForgeEnv(func(k string) string { return env[k] })
	if len(got) != 1 || got["FORGE_DATA_DIR"] != "/clean/data" {
		t.Errorf("ForgeEnv = %v", got)
	}
}
