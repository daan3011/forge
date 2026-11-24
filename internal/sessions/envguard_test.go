package sessions

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daan3011/forge/internal/envguard"
	"github.com/daan3011/forge/internal/sessions/tmux"
)

// The window claude runs in gets forge's own dir overrides but never a
// temp-dir XDG variable, and its command drops any it inherits from the
// tmux server.
func TestStartWindowEnvHasNoTempXDG(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	t.Setenv("FORGE_DATA_DIR", "/clean/data")
	e := newEnv(t)
	if _, err := e.svc.Start(context.Background(), StartOpts{Name: "env", Project: "forge", Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
	_, argv, env, ok := e.tmux.Started(tmux.Target{Session: "forge", Window: "env"})
	if !ok {
		t.Fatal("no window")
	}
	for k, v := range env {
		if strings.HasPrefix(k, "XDG_") {
			t.Errorf("window env passes %s=%s", k, v)
		}
	}
	if env["FORGE_DATA_DIR"] != "/clean/data" || env[EnvSessionName] != "env" {
		t.Errorf("env = %v", env)
	}
	if len(argv) != 3 || !strings.HasPrefix(argv[2], envguard.Shell) {
		t.Errorf("launch lacks the XDG guard: %q", argv)
	}
}
