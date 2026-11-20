package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPermissionPromptVisible(t *testing.T) {
	tests := []struct {
		name string
		pane string
		want bool
	}{
		{"bash prompt", "perm_bash.txt", true},
		{"write prompt", "perm_write.txt", true},
		{"after approval", "after_approve.txt", false},
		{"empty", "", false},
		{"question without choices", "Do you want to proceed?\nsure\n", false},
		{"choices too far below", "Do you want to proceed?\na\nb\nc\nd\ne\n 1. Yes\n", false},
		{"agent prose", "⏺ Do you want to proceed with the refactor? 1. Yes 2. No\n", false},
		{"unselected yes", " Do you want to make this edit to main.go?\n   1. Yes\n ❯ 2. No\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pane := tt.pane
			if filepath.Ext(pane) == ".txt" {
				b, err := os.ReadFile(filepath.Join("testdata", pane))
				if err != nil {
					t.Fatal(err)
				}
				pane = string(b)
			}
			if got := PermissionPromptVisible(pane); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
