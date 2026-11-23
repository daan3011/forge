package testutil

import (
	"os"
	"strings"
	"testing"
)

func TestIsolateHome(t *testing.T) {
	home := IsolateHome(t)
	for _, k := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		v := os.Getenv(k)
		if !strings.HasPrefix(v, home) {
			t.Errorf("%s = %q, want under %q", k, v, home)
		}
		if fi, err := os.Stat(v); err != nil || !fi.IsDir() {
			t.Errorf("%s: %v", k, err)
		}
	}
}
