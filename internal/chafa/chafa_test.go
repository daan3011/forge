package chafa

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
		path       string
		cols, rows int
		want       []string
	}{
		{"/a.png", 40, 20, []string{"--format=symbols", "--size=40x20", "--animate=off", "--polite=on", "--colors=full", "--", "/a.png"}},
		{"-rf.png", 0, 0, []string{"--format=symbols", "--size=1x1", "--animate=off", "--polite=on", "--colors=full", "--", "-rf.png"}},
	}
	for _, tt := range tests {
		if got := Args(tt.path, tt.cols, tt.rows); !slices.Equal(got, tt.want) {
			t.Errorf("Args(%q, %d, %d) = %v, want %v", tt.path, tt.cols, tt.rows, got, tt.want)
		}
	}
}

func TestNotInstalled(t *testing.T) {
	e := Exec{LookPath: func(string) (string, error) { return "", errors.New("nope") }}
	if e.Available() {
		t.Error("Available with no binary")
	}
	if _, err := e.Render(context.Background(), "/a.png", 10, 5); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("err = %v", err)
	}
}

func TestRenderRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	bin := filepath.Join(t.TempDir(), "chafa")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"args: $*\"\n"), 0o755); err != nil { //nolint:gosec // test script
		t.Fatal(err)
	}
	e := Exec{LookPath: func(string) (string, error) { return bin, nil }}
	out, err := e.Render(context.Background(), "/x y.png", 30, 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := "args: --format=symbols --size=30x10 --animate=off --polite=on --colors=full -- /x y.png"; out != want {
		t.Errorf("out = %q, want %q", out, want)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho boom >&2\nexit 2\n"), 0o755); err != nil { //nolint:gosec // test script
		t.Fatal(err)
	}
	if _, err := e.Render(context.Background(), "/a.png", 1, 1); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want stderr in it", err)
	}
}

func TestFake(t *testing.T) {
	f := &Fake{}
	out, err := f.Render(context.Background(), "/a.png", 4, 2)
	if err != nil || out != "chafa:/a.png:4x2" {
		t.Errorf("Render = %q, %v", out, err)
	}
	if !slices.Equal(f.Calls(), []string{"/a.png"}) {
		t.Errorf("calls = %v", f.Calls())
	}
	f.Missing = true
	if f.Available() {
		t.Error("missing fake available")
	}
}
