package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeOpener records the path it was asked to open and optionally rewrites
// the file, standing in for the user's editor.
type fakeOpener struct {
	opened string
	write  string
}

func (f *fakeOpener) Open(_ context.Context, path string) error {
	f.opened = path
	if f.write != "" {
		return os.WriteFile(path, []byte(f.write), 0o644)
	}
	return nil
}

func run(t *testing.T, d deps, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errb bytes.Buffer
	cmd := newRootCmd(d)
	cmd.SetArgs(args)
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	err = cmd.Execute()
	return out.String(), errb.String(), err
}

func TestStubSubcommands(t *testing.T) {
	tests := []struct {
		args    []string
		feature string
	}{
		{[]string{"hook", "PreToolUse"}, "P1.4"},
		{[]string{"capture", "fix the thing"}, "P3.5"},
		{[]string{"new"}, "P1.3"},
	}
	for _, tt := range tests {
		t.Run(tt.args[0], func(t *testing.T) {
			_, stderr, err := run(t, deps{}, tt.args...)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			want := "forge " + tt.args[0] + ": not implemented yet (" + tt.feature + ")"
			if !strings.Contains(stderr, want) {
				t.Fatalf("stderr = %q, want it to contain %q", stderr, want)
			}
		})
	}
}

func TestConfigPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	stdout, _, err := run(t, deps{}, "config", "path")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "forge", "config.yaml")
	if strings.TrimSpace(stdout) != want {
		t.Fatalf("path = %q, want %q", stdout, want)
	}
	if _, err := os.Stat(want); err == nil {
		t.Fatal("config path must not create the file")
	}
}

func TestConfigEdit(t *testing.T) {
	tests := []struct {
		name    string
		write   string
		wantErr string
	}{
		{name: "creates default and validates"},
		{name: "valid edit", write: "ui:\n  animations: off\n"},
		{
			name:    "broken edit",
			write:   "ui:\n  animations: fast\n",
			wantErr: `config.yaml:2: ui.animations: must be one of full, reduced, off; got "fast"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)
			want := filepath.Join(dir, "forge", "config.yaml")
			op := &fakeOpener{write: tt.write}

			_, stderr, err := run(t, deps{opener: op}, "config", "edit")
			if op.opened != want {
				t.Fatalf("opened %q, want %q", op.opened, want)
			}
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !strings.Contains(stderr, "config ok") {
					t.Fatalf("stderr = %q", stderr)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), "forge config edit") {
				t.Fatalf("err = %v, want a hint to re-run edit", err)
			}
		})
	}
}
