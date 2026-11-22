// Package rsvg rasterises SVG files to PNG through rsvg-convert (librsvg)
// when it is installed, for inline previews (P4.7).
package rsvg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// Rasterizer turns an SVG into PNG bytes.
type Rasterizer interface {
	// Available reports whether the rasterizer can be used at all.
	Available() bool
	// PNG renders the SVG at path to fit inside width x height pixels,
	// keeping its aspect ratio.
	PNG(ctx context.Context, path string, width, height int) ([]byte, error)
}

// ErrNotInstalled means rsvg-convert isn't on the PATH.
var ErrNotInstalled = errors.New("rsvg-convert: not installed")

// Exec runs rsvg-convert. Argv only, never a shell.
type Exec struct {
	// LookPath finds executables (exec.LookPath when nil); tests replace it.
	LookPath func(string) (string, error)
}

var _ Rasterizer = Exec{}

func (e Exec) look() (string, error) {
	look := e.LookPath
	if look == nil {
		look = exec.LookPath
	}
	return look("rsvg-convert")
}

// Available implements Rasterizer.
func (e Exec) Available() bool {
	_, err := e.look()
	return err == nil
}

// Args is the argv after "rsvg-convert".
func Args(path string, width, height int) []string {
	return []string{
		"--format=png",
		"--width=" + strconv.Itoa(max(width, 1)),
		"--height=" + strconv.Itoa(max(height, 1)),
		"--keep-aspect-ratio",
		"--", path,
	}
}

// PNG implements Rasterizer.
func (e Exec) PNG(ctx context.Context, path string, width, height int) ([]byte, error) {
	bin, err := e.look()
	if err != nil {
		return nil, ErrNotInstalled
	}
	cmd := exec.CommandContext(ctx, bin, Args(path, width, height)...) //nolint:gosec // fixed binary, fixed flags, path after --
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("rsvg-convert: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}

// Fake rasterises with a function, for tests.
type Fake struct {
	// Missing makes the fake unavailable.
	Missing bool
	// Func rasterises; nil is an error.
	Func func(path string, width, height int) ([]byte, error)

	mu    sync.Mutex
	calls []string
}

var _ Rasterizer = (*Fake)(nil)

// Available implements Rasterizer.
func (f *Fake) Available() bool { return !f.Missing }

// PNG implements Rasterizer.
func (f *Fake) PNG(_ context.Context, path string, width, height int) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, path)
	f.mu.Unlock()
	if f.Missing {
		return nil, ErrNotInstalled
	}
	if f.Func == nil {
		return nil, errors.New("rsvg fake: no Func")
	}
	return f.Func(path, width, height)
}

// Calls are the paths PNG was called with.
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}
