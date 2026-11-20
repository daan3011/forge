package delta

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestArgs(t *testing.T) {
	tests := []struct {
		dark  bool
		width int
		want  []string
	}{
		{true, 120, []string{"--paging=never", "--width=120", "--dark"}},
		{false, 80, []string{"--paging=never", "--width=80", "--light"}},
		{true, 0, []string{"--paging=never", "--width=1", "--dark"}},
	}
	for _, tt := range tests {
		if got := (Exec{Dark: tt.dark}).Args(tt.width); !slices.Equal(got, tt.want) {
			t.Errorf("Args(%v, %d) = %v, want %v", tt.dark, tt.width, got, tt.want)
		}
	}
}

func TestNotInstalled(t *testing.T) {
	e := Exec{LookPath: func(string) (string, error) { return "", errors.New("nope") }}
	if e.Available() {
		t.Error("Available with no binary")
	}
	if _, err := e.Render(context.Background(), "x", 80); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("err = %v", err)
	}
}

// TestRenderPipesStdin runs a stand-in "delta" script that echoes its args
// and stdin, to check the argv and that the diff goes in on stdin.
func TestRenderPipesStdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "delta")
	script := "#!/bin/sh\necho \"args: $*\"\ncat\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil { //nolint:gosec // test script
		t.Fatal(err)
	}
	e := Exec{Dark: true, LookPath: func(string) (string, error) { return bin, nil }}
	if !e.Available() {
		t.Fatal("not available")
	}
	out, err := e.Render(context.Background(), "diff --git a/x b/x\n+y\n", 99)
	if err != nil {
		t.Fatal(err)
	}
	want := "args: --paging=never --width=99 --dark\ndiff --git a/x b/x\n+y"
	if out != want {
		t.Errorf("out = %q, want %q", out, want)
	}

	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho boom >&2\nexit 3\n"), 0o755); err != nil { //nolint:gosec // test script
		t.Fatal(err)
	}
	if _, err := e.Render(context.Background(), "x", 80); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want stderr in it", err)
	}
}

func TestFake(t *testing.T) {
	f := &Fake{}
	out, err := f.Render(context.Background(), "a\nb", 40)
	if err != nil || out != "Δ a\nΔ b" {
		t.Errorf("Render = %q, %v", out, err)
	}
	if !slices.Equal(f.Calls(), []int{40}) {
		t.Errorf("calls = %v", f.Calls())
	}
	f.Missing = true
	if f.Available() {
		t.Error("missing fake available")
	}
}
