package browser

import (
	"context"
	"errors"
	"testing"
)

func TestCheck(t *testing.T) {
	tests := []struct {
		url string
		ok  bool
	}{
		{"https://github.com/daan/forge/pull/1", true},
		{"http://plane.local/ws/browse/PROJ-1/", true},
		{"file:///etc/passwd", false},
		{"-a Calculator", false},
		{"github.com/x", false},
		{"", false},
	}
	for _, tt := range tests {
		if err := Check(tt.url); (err == nil) != tt.ok {
			t.Errorf("Check(%q) = %v", tt.url, err)
		}
	}
}

func TestExecRefusesBadURL(t *testing.T) {
	e := Exec{Bin: "/nonexistent/opener"}
	if err := e.Open(context.Background(), "--help"); !errors.Is(err, ErrBadURL) {
		t.Errorf("err = %v", err)
	}
	if argv := e.Command("https://x.y"); len(argv) != 2 || argv[0] != "/nonexistent/opener" {
		t.Errorf("argv = %v", argv)
	}
}

func TestExecRunsOpener(t *testing.T) {
	if err := (Exec{Bin: "true"}).Open(context.Background(), "https://example.com"); err != nil {
		t.Errorf("err = %v", err)
	}
	if err := (Exec{Bin: "false"}).Open(context.Background(), "https://example.com"); err == nil {
		t.Error("want error from failing opener")
	}
}
