package editor

import (
	"context"
	"slices"
	"testing"
)

func TestCmd(t *testing.T) {
	tests := []struct {
		name, visual, editor string
		want                 []string
	}{
		{"fallback", "", "", []string{"vi", "/tmp/x"}},
		{"editor", "", "nano", []string{"nano", "/tmp/x"}},
		{"visual wins", "hx", "nano", []string{"hx", "/tmp/x"}},
		{"args split", "", "code  -w", []string{"code", "-w", "/tmp/x"}},
		{"blank visual ignored", "  ", "nano", []string{"nano", "/tmp/x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("VISUAL", tt.visual)
			t.Setenv("EDITOR", tt.editor)
			got := Exec{}.Cmd(context.Background(), "/tmp/x").Args
			if !slices.Equal(got, tt.want) {
				t.Fatalf("args = %q, want %q", got, tt.want)
			}
		})
	}
}
