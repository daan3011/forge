package tmux

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestParseWindows(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []Window
		wantErr bool
	}{
		{name: "empty", in: ""},
		{name: "placeholder only", in: "@1\t1\t_forge\t0\t\tzsh\n"},
		{
			name: "alive and dead",
			in:   "@1\t1\t_forge\t0\t\tzsh\n@4\t2\tagent\t0\t\tclaude\n@7\t3\told\t1\t130\tbash\n",
			want: []Window{
				{ID: "@4", Index: 2, Name: "agent", Command: "claude"},
				{ID: "@7", Index: 3, Name: "old", Dead: true, ExitStatus: 130, Command: "bash"},
			},
		},
		{
			name: "tab in command kept",
			in:   "@2\t2\tw\t0\t\tweird\tcmd\n",
			want: []Window{{ID: "@2", Index: 2, Name: "w", Command: "weird\tcmd"}},
		},
		{name: "too few fields", in: "@2\t2\tw\n", wantErr: true},
		{name: "bad index", in: "@2\tx\tw\t0\t\tsh\n", wantErr: true},
		{name: "bad status", in: "@2\t2\tw\t1\tnope\tsh\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseWindows(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestOnSocket(t *testing.T) {
	tests := []struct {
		env  string
		want bool
	}{
		{"", false},
		{"/private/tmp/tmux-501/forge,1234,0", true},
		{"/private/tmp/tmux-501/default,1234,0", false},
		{"/tmp/tmux-1000/forge-test,1,2", false},
		{"/tmp/forge", true},
		{",1,2", false},
	}
	for _, tt := range tests {
		if got := onSocket(tt.env, "forge"); got != tt.want {
			t.Errorf("onSocket(%q) = %v, want %v", tt.env, got, tt.want)
		}
	}
}

func TestErrorIsNotFound(t *testing.T) {
	tests := []struct {
		stderr string
		want   bool
	}{
		{"can't find session: x\n", true},
		{"can't find window: y", true},
		{"no server running on /private/tmp/tmux-501/forge", true},
		{"error connecting to /private/tmp/tmux-501/forge (No such file or directory)", true},
		{"duplicate session: x", false},
		{"unknown option -- z", false},
	}
	for _, tt := range tests {
		err := fmt.Errorf("wrapped: %w", &Error{Args: []string{"x"}, Stderr: tt.stderr, Err: errors.New("exit status 1")})
		if got := errors.Is(err, ErrNotFound); got != tt.want {
			t.Errorf("Is(%q, ErrNotFound) = %v, want %v", tt.stderr, got, tt.want)
		}
	}
	e := &Error{Args: []string{"-L", "forge", "kill-server"}, Stderr: "boom\n", Err: errors.New("exit status 1")}
	if got := e.Error(); got != "tmux -L forge kill-server: boom" {
		t.Errorf("Error() = %q", got)
	}
}
