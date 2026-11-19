package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestStubSubcommands(t *testing.T) {
	tests := []struct {
		args    []string
		feature string
	}{
		{[]string{"hook", "PreToolUse"}, "P1.4"},
		{[]string{"capture", "fix the thing"}, "P3.5"},
		{[]string{"new"}, "P1.3"},
		{[]string{"config"}, "P0.2"},
	}
	for _, tt := range tests {
		t.Run(tt.args[0], func(t *testing.T) {
			var stderr bytes.Buffer
			cmd := newRootCmd()
			cmd.SetArgs(tt.args)
			cmd.SetErr(&stderr)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			want := "forge " + tt.args[0] + ": not implemented yet (" + tt.feature + ")"
			if !strings.Contains(stderr.String(), want) {
				t.Fatalf("stderr = %q, want it to contain %q", stderr.String(), want)
			}
		})
	}
}
