package rsvg

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestArgs(t *testing.T) {
	got := Args("/a.svg", 320, 0)
	want := []string{"--format=png", "--width=320", "--height=1", "--keep-aspect-ratio", "--", "/a.svg"}
	if !slices.Equal(got, want) {
		t.Errorf("Args = %v, want %v", got, want)
	}
}

func TestNotInstalled(t *testing.T) {
	e := Exec{LookPath: func(string) (string, error) { return "", errors.New("nope") }}
	if e.Available() {
		t.Error("Available with no binary")
	}
	if _, err := e.PNG(context.Background(), "/a.svg", 10, 10); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("err = %v", err)
	}
}

func TestPNGRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	bin := filepath.Join(t.TempDir(), "rsvg-convert")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf 'args: %s' \"$*\"\n"), 0o755); err != nil { //nolint:gosec // test script
		t.Fatal(err)
	}
	e := Exec{LookPath: func(string) (string, error) { return bin, nil }}
	out, err := e.PNG(context.Background(), "/a.svg", 100, 50)
	if err != nil {
		t.Fatal(err)
	}
	if want := "args: --format=png --width=100 --height=50 --keep-aspect-ratio -- /a.svg"; string(out) != want {
		t.Errorf("out = %q, want %q", out, want)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho bad svg >&2\nexit 1\n"), 0o755); err != nil { //nolint:gosec // test script
		t.Fatal(err)
	}
	if _, err := e.PNG(context.Background(), "/a.svg", 1, 1); err == nil || !strings.Contains(err.Error(), "bad svg") {
		t.Errorf("err = %v", err)
	}
}

// TestRealRsvg runs the real rsvg-convert when it is installed.
func TestRealRsvg(t *testing.T) {
	if _, err := exec.LookPath("rsvg-convert"); err != nil {
		t.Skip("rsvg-convert not installed")
	}
	svg := filepath.Join(t.TempDir(), "a.svg")
	data := `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="100"><rect width="200" height="100" fill="red"/></svg>`
	if err := os.WriteFile(svg, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	png, err := Exec{}.PNG(context.Background(), svg, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(png, []byte("\x89PNG")) {
		t.Fatalf("not a PNG: %q", png[:min(len(png), 8)])
	}
}
