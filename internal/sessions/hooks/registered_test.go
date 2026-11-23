package hooks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistered(t *testing.T) {
	tests := []struct {
		name    string
		file    *string // nil: no file
		install string  // forge bin to Install with first; "" none
		want    map[string]string
		wantErr bool
	}{
		{name: "no file", want: map[string]string{}},
		{name: "empty file", file: ptr("  \n"), want: map[string]string{}},
		{name: "bad json", file: ptr("{"), wantErr: true},
		{
			name: "other hooks only", file: ptr(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"say hi"}]}]}}`),
			want: map[string]string{},
		},
		{
			name: "partial", file: ptr(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"'/old/forge' hook Stop"}]}]}}`),
			want: map[string]string{Stop: "/old/forge"},
		},
		{name: "installed", install: "/opt/my bin/forge", want: allEvents("/opt/my bin/forge")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.file != nil {
				p := filepath.Join(dir, SettingsRel)
				if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(*tt.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tt.install != "" {
				if err := Install(dir, tt.install); err != nil {
					t.Fatal(err)
				}
			}
			got, err := Registered(dir)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for ev, bin := range tt.want {
				if got[ev] != bin {
					t.Errorf("%s: got %q, want %q", ev, got[ev], bin)
				}
			}
		})
	}
}

func ptr(s string) *string { return &s }

func allEvents(bin string) map[string]string {
	m := map[string]string{}
	for _, ev := range Events {
		m[ev] = bin
	}
	return m
}
