package browser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestOpenFile(t *testing.T) {
	ctx := context.Background()
	f := filepath.Join(t.TempDir(), "mock.html")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "mock.html", "-a", filepath.Join(t.TempDir(), "missing.html")} {
		if err := CheckFile(bad); !errors.Is(err, ErrBadPath) {
			t.Errorf("CheckFile(%q) = %v", bad, err)
		}
		if err := (Exec{Bin: "/nonexistent/opener"}).OpenFile(ctx, bad); !errors.Is(err, ErrBadPath) {
			t.Errorf("OpenFile(%q) = %v", bad, err)
		}
	}
	if err := (Exec{Bin: "true"}).OpenFile(ctx, f); err != nil {
		t.Errorf("err = %v", err)
	}
	if err := (Exec{Bin: "false"}).OpenFile(ctx, f); err == nil {
		t.Error("want error from failing opener")
	}
	fake := &Fake{}
	if err := fake.OpenFile(ctx, f); err != nil || len(fake.Files()) != 1 || fake.Files()[0] != f {
		t.Errorf("fake = %v, %v", fake.Files(), err)
	}
	if err := fake.OpenFile(ctx, "rel"); !errors.Is(err, ErrBadPath) || len(fake.Files()) != 1 {
		t.Errorf("fake accepted a relative path: %v", err)
	}
}
