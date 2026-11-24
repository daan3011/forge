package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDetectDistro(t *testing.T) {
	tests := map[string]string{
		"ID=ubuntu\nID_LIKE=debian\n":              DistroDebian,
		"ID=debian\n":                              DistroDebian,
		`ID="fedora"` + "\n":                       DistroFedora,
		"ID=rocky\nID_LIKE=\"rhel centos fedora\"": DistroFedora,
		"ID=arch\n":                                "",
		"":                                         "",
	}
	for in, want := range tests {
		if got := DetectDistro(in); got != want {
			t.Errorf("DetectDistro(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInstallHint(t *testing.T) {
	tests := []struct{ goos, distro, pkg, want string }{
		{"darwin", "", "tmux", "brew install tmux"},
		{"darwin", "", "librsvg", "brew install librsvg"},
		{"linux", DistroDebian, "librsvg", "sudo apt install librsvg2-bin"},
		{"linux", DistroFedora, "librsvg", "sudo dnf install librsvg2-tools"},
		{"linux", DistroFedora, "tmux", "sudo dnf install tmux"},
		{"linux", "", "librsvg", "install librsvg with your package manager (e.g. sudo apt install librsvg2-bin or sudo dnf install librsvg2-tools)"},
	}
	for _, tt := range tests {
		if got := InstallHint(tt.goos, tt.distro, tt.pkg); got != tt.want {
			t.Errorf("InstallHint(%s, %s, %s) = %q, want %q", tt.goos, tt.distro, tt.pkg, got, tt.want)
		}
	}
}

func TestCheckXDG(t *testing.T) {
	env := map[string]string{}
	e := Env{Getenv: func(k string) string { return env[k] }}
	if c := checkXDG(context.Background(), e); c.Status != OK {
		t.Errorf("clean env: %+v", c)
	}
	env["XDG_DATA_HOME"] = "/tmp/forge-try/data"
	c := checkXDG(context.Background(), e)
	if c.Status != Warn || !strings.Contains(c.Detail, "XDG_DATA_HOME=/tmp/forge-try/data") || !strings.Contains(c.Fix, "FORGE_DATA_DIR") {
		t.Errorf("temp XDG: %+v", c)
	}
	if c := checkXDG(context.Background(), Env{}); c.Status != Skip {
		t.Errorf("no getenv: %+v", c)
	}
}

func TestRunOnly(t *testing.T) {
	e := Env{
		GOOS:     "darwin",
		LookPath: func(string) (string, error) { return "", errors.New("missing") },
	}
	got := RunOnly(context.Background(), e, NameRsvg, NameGit, NameChafa)
	var names []string
	for _, c := range got {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "git,chafa,rsvg-convert" {
		t.Errorf("names = %v", names)
	}
	if got[0].Fix != "brew install git" {
		t.Errorf("git fix = %q", got[0].Fix)
	}
}
