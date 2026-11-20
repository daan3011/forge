package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindTranscript(t *testing.T) {
	dir := t.TempDir()
	const other = "11111111-2222-4333-8444-555555555555"
	proj := filepath.Join(dir, "-Users-me-wt-forge-fix-ci")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(proj, id+".jsonl")
	if err := os.WriteFile(want, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A directory with a transcript's name is not a transcript.
	if err := os.MkdirAll(filepath.Join(dir, "x", other+".jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, dir, id string
		want          string
		ok            bool
	}{
		{"found", dir, id, want, true},
		{"missing", dir, "99999999-2222-4333-8444-555555555555", "", false},
		{"directory is not a file", dir, other, "", false},
		{"invalid id", dir, "*", "", false},
		{"no projects dir", "", id, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := FindTranscript(tt.dir, tt.id)
			if got != tt.want || ok != tt.ok {
				t.Errorf("FindTranscript = %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestUUIDInUse(t *testing.T) {
	tests := []struct {
		name, pane string
		want       bool
	}{
		{"claude error", "Error: Session ID " + id + " is already in use.\n", true},
		{"amid other output", "$ claude\nError: Session ID " + id + " is already in use.\n\nPane is dead (status 1)", true},
		{"running session", "╭───╮\n│ ✻ Welcome to Claude Code │\n> ", false},
		{"port in use", "callback port 8080 is already in use.", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UUIDInUse(tt.pane); got != tt.want {
				t.Errorf("UUIDInUse(%q) = %v, want %v", tt.pane, got, tt.want)
			}
		})
	}
}
