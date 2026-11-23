package doctor

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daan3011/forge/internal/sessions/claude"
	"github.com/daan3011/forge/internal/sessions/hooks"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/slashcmd"
)

var errAuth = errors.New("401")

// healthy is an Env where every check passes.
func healthy(t *testing.T) Env {
	t.Helper()
	dir := t.TempDir()
	conf := filepath.Join(dir, "tmux.conf")
	if _, err := tmux.EnsureConfig(conf, tmux.ConfigColors{}); err != nil {
		t.Fatal(err)
	}
	return Env{
		GOOS:     "darwin",
		LookPath: func(bin string) (string, error) { return "/usr/bin/" + bin, nil },
		Output: func(_ context.Context, bin string, _ ...string) (string, error) {
			return filepath.Base(bin) + " version 1.0\nmore", nil
		},
		Lstat:            func(string) (fs.FileInfo, error) { return fakeInfo{}, nil },
		EvalSymlinks:     func(p string) (string, error) { return p, nil },
		TempDirs:         []string{"/tmp"},
		Version:          "v1.2.3",
		Bin:              "/usr/bin/forge",
		PathDirs:         []string{"/usr/bin"},
		ConfigPath:       "/cfg/config.yaml",
		StorePath:        "/data/forge.db",
		GitHubViewer:     func(context.Context) (string, error) { return "octo", nil },
		IsGitHubAuth:     func(err error) bool { return errors.Is(err, errAuth) },
		TmuxVersion:      func(context.Context) (tmux.Version, error) { return tmux.Version{Major: 3, Minor: 5}, nil },
		TmuxConf:         conf,
		TmuxServerConfig: func(context.Context) ([]string, bool, error) { return nil, false, nil },
		SkillsEnabled:    true,
	}
}

type fakeInfo struct {
	fs.FileInfo
	mode fs.FileMode
}

func (f fakeInfo) Mode() fs.FileMode { return f.mode }

func find(t *testing.T, cs []Check, name string) Check {
	t.Helper()
	for _, c := range cs {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check %q", name)
	return Check{}
}

func TestHealthyIsGreen(t *testing.T) {
	cs := Run(context.Background(), healthy(t))
	for _, c := range cs {
		if c.Status != OK && c.Status != Skip {
			t.Errorf("%s: %s %q (fix %q)", c.Name, c.Status, c.Detail, c.Fix)
		}
	}
	if Failed(cs) {
		t.Error("Failed: want false")
	}
	if got := find(t, cs, "git").Detail; got != "git version 1.0" {
		t.Errorf("git detail = %q", got)
	}
	if got := find(t, cs, "plane").Status; got != Skip {
		t.Errorf("plane = %s, want skip when not configured", got)
	}
}

func TestChecks(t *testing.T) {
	missing := func(names ...string) func(string) (string, error) {
		return func(bin string) (string, error) {
			for _, n := range names {
				if bin == n {
					return "", errors.New("not found")
				}
			}
			return "/usr/bin/" + bin, nil
		}
	}
	tests := []struct {
		name   string
		edit   func(*Env)
		check  string
		status Status
		detail string // substring
		fix    string // substring
	}{
		{"git missing", func(e *Env) { e.LookPath = missing("git") }, "git", Fail, "not installed", "brew install git"},
		{"git missing linux", func(e *Env) { e.LookPath = missing("git"); e.GOOS = "linux" }, "git", Fail, "", "apt install git"},
		{"gh missing", func(e *Env) { e.LookPath = missing("gh") }, "gh", Fail, "not installed", "brew install gh"},
		{"gh logged out", func(e *Env) {
			e.GitHubViewer = func(context.Context) (string, error) { return "", errAuth }
		}, "gh", Fail, "not logged in", "gh auth login"},
		{"gh offline", func(e *Env) {
			e.GitHubViewer = func(context.Context) (string, error) { return "", errors.New("dial tcp") }
		}, "gh", Fail, "can't reach GitHub", "network"},
		{"gh ok", nil, "gh", OK, "logged in as octo", ""},
		{"tmux missing", func(e *Env) { e.LookPath = missing("tmux") }, "tmux", Fail, "not installed", "brew install tmux"},
		{"tmux too old", func(e *Env) {
			e.TmuxVersion = func(context.Context) (tmux.Version, error) { return tmux.Version{Major: 3, Minor: 1}, nil }
		}, "tmux", Fail, "tmux 3.1", "3.2 or newer"},
		{"tmux no passthrough", func(e *Env) {
			e.TmuxVersion = func(context.Context) (tmux.Version, error) { return tmux.Version{Major: 3, Minor: 2}, nil }
		}, "tmux", Warn, "tmux 3.2", "image previews"},
		{"tmux error", func(e *Env) {
			e.TmuxVersion = func(context.Context) (tmux.Version, error) { return tmux.Version{}, errors.New("boom") }
		}, "tmux", Fail, "boom", "tmux -V"},
		{"tmux conf missing", func(e *Env) { e.TmuxConf = filepath.Join(t.TempDir(), "none") }, "tmux config", Warn, "not written yet", "--fix"},
		{"tmux conf old", func(e *Env) {
			p := filepath.Join(t.TempDir(), "tmux.conf")
			if err := os.WriteFile(p, []byte("# forge-tmux-config v1\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			e.TmuxConf = p
		}, "tmux config", Warn, "is v1", "--reset"},
		{"tmux conf custom", func(e *Env) {
			p := filepath.Join(t.TempDir(), "tmux.conf")
			if err := os.WriteFile(p, []byte("set -g mouse on\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			e.TmuxConf = p
		}, "tmux config", OK, "customised", ""},
		{"tmux server down", func(e *Env) {
			e.TmuxServerConfig = func(context.Context) ([]string, bool, error) { return nil, false, nil }
		}, "tmux server", OK, "not running (it starts with ", ""},
		{"tmux server forge conf", func(e *Env) {
			conf := e.TmuxConf
			e.TmuxServerConfig = func(context.Context) ([]string, bool, error) { return []string{"/etc/tmux.conf", conf}, true, nil }
		}, "tmux server", OK, "running with ", ""},
		{"tmux server other conf", func(e *Env) {
			e.TmuxServerConfig = func(context.Context) ([]string, bool, error) { return []string{"/home/me/.tmux.conf"}, true, nil }
		}, "tmux server", Warn, "running with /home/me/.tmux.conf, not ", "forge tmux restart"},
		{"tmux server no conf", func(e *Env) {
			e.TmuxServerConfig = func(context.Context) ([]string, bool, error) { return nil, true, nil }
		}, "tmux server", Warn, "running with no config file", "forge tmux restart"},
		{"tmux server error", func(e *Env) {
			e.TmuxServerConfig = func(context.Context) ([]string, bool, error) { return nil, false, errors.New("permission denied") }
		}, "tmux server", Warn, "permission denied", ""},
		{"tmux server unknown", func(e *Env) { e.TmuxServerConfig = nil }, "tmux server", Skip, "", ""},
		{"tmux server no tmux", func(e *Env) { e.LookPath = missing("tmux") }, "tmux server", Skip, "isn't installed", ""},
		{"claude missing", func(e *Env) { e.LookPath = missing("claude") }, "claude", Fail, "not installed", "claude.ai/install.sh"},
		{"claude version fails", func(e *Env) {
			e.Output = func(context.Context, string, ...string) (string, error) { return "", errors.New("exit 1") }
		}, "claude", Fail, "exit 1", "claude --version"},
		{"claude temp symlink", func(e *Env) {
			e.Lstat = func(string) (fs.FileInfo, error) { return fakeInfo{mode: fs.ModeSymlink}, nil }
			e.EvalSymlinks = func(p string) (string, error) {
				if p == "/usr/bin/claude" {
					return "/tmp/claude-xyz/claude", nil
				}
				return p, nil
			}
		}, "claude", Fail, "claude symlink points into a temp dir — reinstall or relink", "ln -sf"},
		{"claude broken symlink", func(e *Env) {
			e.Lstat = func(string) (fs.FileInfo, error) { return fakeInfo{mode: fs.ModeSymlink}, nil }
			e.EvalSymlinks = func(p string) (string, error) {
				if p == "/usr/bin/claude" {
					return "", fs.ErrNotExist
				}
				return p, nil
			}
		}, "claude", Fail, "broken symlink", "install.sh"},
		{"claude good symlink", func(e *Env) {
			e.Lstat = func(string) (fs.FileInfo, error) { return fakeInfo{mode: fs.ModeSymlink}, nil }
			e.EvalSymlinks = func(p string) (string, error) {
				if p == "/usr/bin/claude" {
					return "/home/me/.local/share/claude/versions/2.1.0", nil
				}
				return p, nil
			}
		}, "claude", OK, "claude version 1.0", ""},
		{"delta missing is a warning", func(e *Env) { e.LookPath = missing("delta") }, "delta", Warn, "optional", "brew install git-delta"},
		{"chafa missing is a warning", func(e *Env) { e.LookPath = missing("chafa") }, "chafa", Warn, "optional", "brew install chafa"},
		{"plane no key", func(e *Env) { e.PlaneConfigured, e.PlaneErr = true, errors.New("no API key configured") }, "plane", Fail, "no API key", "PLANE_API_KEY"},
		{"plane ok", func(e *Env) {
			e.PlaneConfigured = true
			e.PlaneMe = func(context.Context) (string, error) { return "daan", nil }
		}, "plane", OK, "connected as daan", ""},
		{"plane rejected", func(e *Env) {
			e.PlaneConfigured = true
			e.PlaneMe = func(context.Context) (string, error) { return "", errAuth }
			e.IsPlaneAuth = func(err error) bool { return errors.Is(err, errAuth) }
		}, "plane", Fail, "rejected", "API tokens"},
		{"plane down", func(e *Env) {
			e.PlaneConfigured = true
			e.PlaneMe = func(context.Context) (string, error) { return "", errors.New("timeout") }
		}, "plane", Fail, "can't reach Plane", "base_url"},
		{"config missing", func(e *Env) { e.ConfigErr = fs.ErrNotExist }, "config", Warn, "no config yet", "--fix"},
		{"config invalid", func(e *Env) { e.ConfigErr = errors.New("bad yaml") }, "config", Fail, "bad yaml", "forge config edit"},
		{"store not created", func(e *Env) { e.StoreMissing = true }, "database", OK, "not created yet", ""},
		{"rsvg missing linux", func(e *Env) { e.LookPath = missing("rsvg-convert"); e.GOOS = "linux" }, "rsvg-convert", Warn, "optional", "apt install librsvg2-bin"},
		{"store broken", func(e *Env) { e.StoreErr = errors.New("locked") }, "database", Fail, "locked", "writable"},
		{"forge not on PATH", func(e *Env) {
			e.Bin, e.PathDirs = "/opt/forge/forge", []string{"/usr/bin"}
			e.LookPath = missing("forge")
		}, "forge", Warn, "v1.2.3 at /opt/forge/forge", `export PATH="/opt/forge"`},
		{"forge unknown", func(e *Env) { e.Bin = "" }, "forge", Fail, "can't find", ""},
		{"skills off", func(e *Env) { e.SkillsEnabled = false }, "skills", Skip, "off", ""},
		{"sessions unlisted", func(e *Env) { e.SessionsErr = errors.New("db") }, "hooks", Warn, "can't list sessions", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := healthy(t)
			if tt.edit != nil {
				tt.edit(&e)
			}
			c := find(t, Run(context.Background(), e), tt.check)
			if c.Status != tt.status {
				t.Errorf("status = %s, want %s (%q)", c.Status, tt.status, c.Detail)
			}
			if !strings.Contains(c.Detail, tt.detail) {
				t.Errorf("detail = %q, want it to contain %q", c.Detail, tt.detail)
			}
			if !strings.Contains(c.Fix, tt.fix) {
				t.Errorf("fix = %q, want it to contain %q", c.Fix, tt.fix)
			}
		})
	}
}

func TestRelinkSuggestsNewestVersion(t *testing.T) {
	home := t.TempDir()
	for _, v := range []string{"2.9.0", "2.10.1", "2.10.0"} {
		p := filepath.Join(home, ".local", "share", "claude", "versions", v)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	got := relinkFix(Env{Home: home}, "/home/me/.local/bin/claude")
	if !strings.Contains(got, "versions/2.10.1") || !strings.HasPrefix(got, "ln -sf") {
		t.Errorf("relinkFix = %q", got)
	}
	if got := relinkFix(Env{Home: t.TempDir()}, "/x"); !strings.Contains(got, "install.sh") || !strings.Contains(got, `ln -sf <a claude binary outside the temp dir> "/x"`) {
		t.Errorf("no versions: %q", got)
	}
}

func TestSessionChecks(t *testing.T) {
	const bin = "/usr/bin/forge"
	mk := func(t *testing.T, name string, hooksBin, mcpBin string, skills bool) Session {
		root := t.TempDir()
		s := Session{
			Name: name, Worktree: filepath.Join(root, "wt"),
			SessionDir: filepath.Join(root, "s"), SkillsRoot: filepath.Join(root, "s", "forge"),
		}
		if err := os.MkdirAll(s.Worktree, 0o700); err != nil {
			t.Fatal(err)
		}
		if hooksBin != "" {
			if err := hooks.Install(s.Worktree, hooksBin); err != nil {
				t.Fatal(err)
			}
		}
		if mcpBin != "" {
			if err := claude.WriteMCPConfig(filepath.Join(s.SessionDir, claude.MCPConfigFile), mcpBin, name); err != nil {
				t.Fatal(err)
			}
		}
		if skills {
			if err := os.MkdirAll(s.SkillsRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(s.SkillsRoot, slashcmd.ManifestFile), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return s
	}
	e := healthy(t)
	e.Sessions = []Session{
		mk(t, "good", bin, bin, true),
		mk(t, "moved", "/old/forge", "/old/forge", true),
		mk(t, "bare", "", "", false),
	}
	cs := Run(context.Background(), e)
	for _, tt := range []struct{ check, want string }{
		{"hooks", "moved: calls /old/forge; bare: not registered"},
		{"mcp config", "moved: runs /old/forge; bare: missing"},
		{"skills", "bare: not synced"},
	} {
		c := find(t, cs, tt.check)
		if c.Status != Fail || c.Detail != tt.want || !strings.Contains(c.Fix, "--fix") {
			t.Errorf("%s = %s %q fix %q, want fail %q", tt.check, c.Status, c.Detail, c.Fix, tt.want)
		}
	}
	if got := FixSessions(cs); strings.Join(got, ",") != "moved,bare" {
		t.Errorf("FixSessions = %v", got)
	}

	e.Sessions = e.Sessions[:1]
	for _, c := range Run(context.Background(), e) {
		if c.Status == Fail {
			t.Errorf("%s: %q", c.Name, c.Detail)
		}
	}
}

func TestTimeout(t *testing.T) {
	e := healthy(t)
	e.Timeout = 20 * time.Millisecond
	e.GitHubViewer = func(ctx context.Context) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if c := find(t, Run(context.Background(), e), "gh"); c.Status != Fail {
		t.Errorf("gh = %s, want fail on timeout", c.Status)
	}
}

func TestRender(t *testing.T) {
	cs := []Check{
		{Name: "git", Status: OK, Detail: "git 2"},
		{Name: "chafa", Status: Warn, Detail: "missing", Fix: "brew install chafa"},
		{Name: "plane", Status: Skip, Detail: "not set up"},
	}
	var b bytes.Buffer
	if err := Render(&b, cs, Styles{}); err != nil {
		t.Fatal(err)
	}
	want := "✓ git    git 2\n! chafa  missing\n         fix: brew install chafa\n– plane  not set up\n\n1 ok, 1 warning(s), 0 failed, 1 skipped\n"
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
	b.Reset()
	if err := Render(&b, cs[:1], Styles{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "the forge is hot") {
		t.Errorf("all green: %q", b.String())
	}
	b.Reset()
	if err := Render(&b, []Check{{Name: "x", Status: Fail}}, Styles{OK: strings.ToUpper, Fail: func(s string) string { return "<" + s + ">" }}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "<✗>") || !strings.Contains(b.String(), "1 failed>") {
		t.Errorf("styled: %q", b.String())
	}
}
