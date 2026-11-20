package notify

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestSystemNotify(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name     string
		goos     string
		beeepErr error
		runErr   error
		wantArgv []string
		wantErr  string
	}{
		{name: "beeep works", goos: "darwin"},
		{
			name: "darwin falls back to osascript", goos: "darwin", beeepErr: boom,
			wantArgv: []string{"osascript", "-e", `display notification "agent is waiting for you" with title "forge"`},
		},
		{
			name: "linux falls back to notify-send", goos: "linux", beeepErr: boom,
			wantArgv: []string{"notify-send", "--", "forge", "agent is waiting for you"},
		},
		{
			name: "everything fails", goos: "linux", beeepErr: boom, runErr: errors.New("not found"),
			wantArgv: []string{"notify-send", "--", "forge", "agent is waiting for you"},
			wantErr:  "notify-send: not found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			s := System{
				GOOS:  tt.goos,
				Beeep: func(string, string) error { return tt.beeepErr },
				Run: func(_ context.Context, argv []string) error {
					got = argv
					return tt.runErr
				},
			}
			err := s.Notify(context.Background(), "forge", "agent is waiting for you")
			if tt.wantErr == "" && err != nil {
				t.Fatalf("Notify: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("Notify error = %v, want it to contain %q", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.wantArgv) {
				t.Fatalf("fallback argv = %q, want %q", got, tt.wantArgv)
			}
		})
	}
}

func TestSystemNotifyGivesUpWithContext(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	ran := false
	s := System{
		Beeep: func(string, string) error { <-block; return nil },
		Run:   func(context.Context, []string) error { ran = true; return nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Notify(ctx, "t", "b"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Notify = %v, want context.Canceled", err)
	}
	if ran {
		t.Fatal("fallback ran after the context was done")
	}
}

func TestAppleString(t *testing.T) {
	tests := map[string]string{
		`plain`:          `"plain"`,
		`say "hi"`:       `"say \"hi\""`,
		`back\slash`:     `"back\\slash"`,
		"two\nlines":     `"two lines"`,
		`"; do shell x"`: `"\"; do shell x\""`,
	}
	for in, want := range tests {
		if got := appleString(in); got != want {
			t.Errorf("appleString(%q) = %s, want %s", in, got, want)
		}
	}
}
