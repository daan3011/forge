// Package doctor checks that forge's environment works: the external tools
// it drives, GitHub and Plane access, and what forge registers for each
// agent session (hooks, MCP config, skills). Every check says how to fix
// what it finds; `forge doctor` prints them.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/daan3011/forge/internal/sessions/claude"
	"github.com/daan3011/forge/internal/sessions/hooks"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/slashcmd"
)

// Status is a check's outcome.
type Status int

// The statuses, from best to worst.
const (
	// OK is a check that passed.
	OK Status = iota
	// Skip is a check that doesn't apply (e.g. Plane not set up).
	Skip
	// Warn is a problem forge works around (an optional tool missing).
	Warn
	// Fail is a problem that breaks part of forge.
	Fail
)

func (s Status) String() string {
	return [...]string{"ok", "skip", "warn", "fail"}[s]
}

// MarshalText makes Status a string in --json output.
func (s Status) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// UnmarshalText reads a status written by MarshalText.
func (s *Status) UnmarshalText(b []byte) error {
	for v := OK; v <= Fail; v++ {
		if v.String() == string(b) {
			*s = v
			return nil
		}
	}
	return fmt.Errorf("doctor: unknown status %q", b)
}

// Check is one line of the report.
type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
	// Fix tells the user what to do; empty when nothing needs doing.
	Fix string `json:"fix,omitempty"`
	// Sessions are the agent sessions --fix re-prepares for this check.
	Sessions []string `json:"sessions,omitempty"`
}

// Session is an agent session whose forge registrations are checked.
type Session struct {
	Name       string
	Worktree   string
	SessionDir string
	SkillsRoot string
}

// Env is everything the checks look at; every field can be faked.
type Env struct {
	GOOS     string
	LookPath func(string) (string, error)
	// Output runs a command and returns its combined, trimmed output.
	Output       func(ctx context.Context, bin string, args ...string) (string, error)
	Lstat        func(string) (fs.FileInfo, error)
	EvalSymlinks func(string) (string, error)
	// TempDirs are where a claude symlink must not point.
	TempDirs []string
	// Home is the user's home directory (to suggest a claude relink).
	Home string

	// Version and Bin are this forge's version and resolved path; PathDirs
	// the directories on $PATH.
	Version  string
	Bin      string
	PathDirs []string

	// ConfigPath is the config file; ConfigErr its load error
	// (fs.ErrNotExist when there is none yet).
	ConfigPath string
	ConfigErr  error
	// StorePath is the database; StoreErr its open error. StoreMissing
	// says it doesn't exist yet (forge creates it on first run).
	StorePath    string
	StoreErr     error
	StoreMissing bool

	// GitHubViewer returns the logged-in GitHub user; IsGitHubAuth tells
	// a missing / rejected token from other errors.
	GitHubViewer func(context.Context) (string, error)
	IsGitHubAuth func(error) bool

	// PlaneConfigured is false when no Plane workspace is set (the check
	// is skipped). PlaneMe returns the API key's user; PlaneErr is a setup
	// error (no API key) found before any request.
	PlaneConfigured bool
	PlaneErr        error
	PlaneMe         func(context.Context) (string, error)
	IsPlaneAuth     func(error) bool

	// TmuxVersion asks forge's tmux for its version.
	TmuxVersion func(context.Context) (tmux.Version, error)
	// TmuxConf is forge's tmux config file.
	TmuxConf string

	// Sessions are the active agent sessions; SessionsErr why they
	// couldn't be listed. SkillsEnabled says forge syncs skills.
	Sessions      []Session
	SessionsErr   error
	SkillsEnabled bool

	// Timeout bounds each check; zero means DefaultTimeout.
	Timeout time.Duration
}

// DefaultTimeout bounds each check.
const DefaultTimeout = 5 * time.Second

// TmuxMin is the oldest tmux forge supports (display-popup,
// extended-keys); TmuxGood the one image previews need (passthrough).
var (
	TmuxMin  = tmux.Version{Major: 3, Minor: 2}
	TmuxGood = tmux.Version{Major: 3, Minor: 3}
)

// Run runs every check concurrently and returns them in a fixed order.
func Run(ctx context.Context, e Env) []Check {
	checks := []func(context.Context, Env) Check{
		checkForge, checkConfig, checkStore, checkGit, checkGH, checkTmux, checkTmuxConf,
		checkClaude, optional("delta", "diffs are coloured by forge instead", "git-delta"),
		optional("chafa", "image previews fall back to text in terminals without graphics", "chafa"),
		optional("rsvg-convert", "SVG artifacts aren't previewed", "librsvg"),
		checkPlane, checkHooks, checkMCP, checkSkills,
	}
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	out := make([]Check, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Go(func() {
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			out[i] = c(cctx, e)
		})
	}
	wg.Wait()
	return out
}

// Failed reports whether any check failed.
func Failed(cs []Check) bool {
	return slices.ContainsFunc(cs, func(c Check) bool { return c.Status == Fail })
}

// FixSessions are the sessions --fix should re-prepare, each once.
func FixSessions(cs []Check) []string {
	var out []string
	for _, c := range cs {
		for _, s := range c.Sessions {
			if !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
	}
	return out
}

// aptNames are Debian/Ubuntu package names that differ from Homebrew's.
var aptNames = map[string]string{"librsvg": "librsvg2-bin"}

// install is the package-manager hint for pkg (a Homebrew name) on goos.
func install(goos, pkg string) string {
	if goos == "darwin" {
		return "brew install " + pkg
	}
	if n, ok := aptNames[pkg]; ok {
		pkg = n
	}
	return "install " + pkg + " with your package manager (e.g. sudo apt install " + pkg + ")"
}

func checkForge(_ context.Context, e Env) Check {
	c := Check{Name: "forge", Detail: e.Version}
	if e.Bin == "" {
		c.Status, c.Detail = Fail, "can't find forge's own binary"
		c.Fix = "hooks and the MCP server need it: run forge from a regular install"
		return c
	}
	c.Detail += " at " + e.Bin
	if !slices.Contains(e.PathDirs, filepath.Dir(e.Bin)) {
		if p, err := e.LookPath("forge"); err != nil || p != e.Bin {
			c.Status = Warn
			c.Fix = fmt.Sprintf("add it to your PATH: export PATH=%q:$PATH", filepath.Dir(e.Bin))
		}
	}
	return c
}

func checkConfig(_ context.Context, e Env) Check {
	c := Check{Name: "config", Detail: e.ConfigPath}
	switch {
	case errors.Is(e.ConfigErr, fs.ErrNotExist):
		c.Status, c.Detail = Warn, "no config yet at "+e.ConfigPath
		c.Fix = "run forge (or forge doctor --fix) to write the default"
	case e.ConfigErr != nil:
		c.Status, c.Detail = Fail, e.ConfigErr.Error()
		c.Fix = "fix the file (forge config edit), then run forge doctor again"
	}
	return c
}

func checkStore(_ context.Context, e Env) Check {
	c := Check{Name: "database", Detail: e.StorePath}
	if e.StoreMissing {
		c.Detail = "not created yet (forge creates it on first run): " + e.StorePath
	}
	if e.StoreErr != nil {
		c.Status, c.Detail = Fail, e.StoreErr.Error()
		c.Fix = "check that " + filepath.Dir(e.StorePath) + " is writable"
	}
	return c
}

// version runs `bin flag` and returns its first line.
func version(ctx context.Context, e Env, bin string, args ...string) (string, error) {
	out, err := e.Output(ctx, bin, args...)
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(out, "\n")
	return strings.TrimSpace(line), nil
}

func checkGit(ctx context.Context, e Env) Check {
	c := Check{Name: "git"}
	p, err := e.LookPath("git")
	if err != nil {
		c.Status, c.Detail, c.Fix = Fail, "not installed", install(e.GOOS, "git")
		return c
	}
	v, err := version(ctx, e, p, "--version")
	if err != nil {
		c.Status, c.Detail, c.Fix = Fail, err.Error(), "reinstall git"
		return c
	}
	c.Detail = v
	return c
}

func checkGH(ctx context.Context, e Env) Check {
	c := Check{Name: "gh"}
	if _, err := e.LookPath("gh"); err != nil {
		c.Status, c.Detail, c.Fix = Fail, "not installed", install(e.GOOS, "gh")
		return c
	}
	if e.GitHubViewer == nil {
		c.Detail = "installed"
		return c
	}
	login, err := e.GitHubViewer(ctx)
	switch {
	case err != nil && e.IsGitHubAuth != nil && e.IsGitHubAuth(err):
		c.Status, c.Detail, c.Fix = Fail, "not logged in", "gh auth login"
	case err != nil:
		c.Status, c.Detail, c.Fix = Fail, "can't reach GitHub: "+err.Error(), "check your network, then gh auth status"
	default:
		c.Detail = "logged in as " + login
	}
	return c
}

func checkTmux(ctx context.Context, e Env) Check {
	c := Check{Name: "tmux"}
	if _, err := e.LookPath("tmux"); err != nil {
		c.Status, c.Detail, c.Fix = Fail, "not installed", install(e.GOOS, "tmux")
		return c
	}
	v, err := e.TmuxVersion(ctx)
	if err != nil {
		c.Status, c.Detail, c.Fix = Fail, err.Error(), "check that tmux -V works"
		return c
	}
	c.Detail = "tmux " + v.String()
	switch {
	case !v.AtLeast(TmuxMin.Major, TmuxMin.Minor):
		c.Status = Fail
		c.Fix = fmt.Sprintf("forge needs tmux %s or newer: %s", TmuxMin, install(e.GOOS, "tmux"))
	case !v.AtLeast(TmuxGood.Major, TmuxGood.Minor):
		c.Status = Warn
		c.Fix = fmt.Sprintf("tmux %s or newer gives image previews inside tmux", TmuxGood)
	}
	return c
}

func checkTmuxConf(_ context.Context, e Env) Check {
	c := Check{Name: "tmux config", Detail: e.TmuxConf}
	if e.TmuxConf == "" {
		c.Status, c.Detail = Skip, "no config directory"
		return c
	}
	if _, err := os.Stat(e.TmuxConf); errors.Is(err, fs.ErrNotExist) {
		c.Status, c.Detail = Warn, "not written yet: "+e.TmuxConf
		c.Fix = "run forge (or forge doctor --fix) to write the default"
		return c
	}
	switch v, ok := tmux.ConfigVersionOf(e.TmuxConf); {
	case !ok:
		c.Detail += " (customised, no version header)"
	case v < tmux.ConfigVersion:
		c.Status = Warn
		c.Detail = fmt.Sprintf("%s is v%d, forge ships v%d", e.TmuxConf, v, tmux.ConfigVersion)
		c.Fix = "forge tmux config --reset (back up your changes first)"
	}
	return c
}

func checkClaude(ctx context.Context, e Env) Check {
	c := Check{Name: "claude"}
	p, err := e.LookPath(claude.DefaultBin)
	if err != nil {
		c.Status, c.Detail = Fail, "not installed"
		c.Fix = "curl -fsSL https://claude.ai/install.sh | bash"
		return c
	}
	if fi, err := e.Lstat(p); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		target, err := e.EvalSymlinks(p)
		if err != nil {
			c.Status, c.Detail = Fail, p+" is a broken symlink"
			c.Fix = relinkFix(e, p)
			return c
		}
		if underAny(e, target, e.TempDirs) {
			c.Status = Fail
			c.Detail = "claude symlink points into a temp dir — reinstall or relink (" + p + " → " + target + ")"
			c.Fix = relinkFix(e, p)
			return c
		}
	}
	v, err := version(ctx, e, p, "--version")
	if err != nil {
		c.Status, c.Detail = Fail, err.Error()
		c.Fix = "check that claude --version works; reinstall: curl -fsSL https://claude.ai/install.sh | bash"
		return c
	}
	c.Detail = v
	return c
}

// relinkFix suggests pointing link at the newest installed claude version,
// else reinstalling.
func relinkFix(e Env, link string) string {
	reinstall := "curl -fsSL https://claude.ai/install.sh | bash"
	var versions []string
	if e.Home != "" {
		versions, _ = filepath.Glob(filepath.Join(e.Home, ".local", "share", "claude", "versions", "*"))
	}
	if len(versions) == 0 {
		return fmt.Sprintf("reinstall: %s  (or relink: ln -sf <a claude binary outside the temp dir> %q)", reinstall, link)
	}
	slices.SortFunc(versions, compareVersionPaths)
	return fmt.Sprintf("ln -sf %q %q  (or reinstall: %s)", versions[len(versions)-1], link, reinstall)
}

// compareVersionPaths orders .../versions/<x.y.z> numerically.
func compareVersionPaths(a, b string) int {
	pa := strings.Split(filepath.Base(a), ".")
	pb := strings.Split(filepath.Base(b), ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		var na, nb int
		_, ea := fmt.Sscanf(pa[i], "%d", &na)
		_, eb := fmt.Sscanf(pb[i], "%d", &nb)
		if ea != nil || eb != nil {
			if c := strings.Compare(pa[i], pb[i]); c != 0 {
				return c
			}
			continue
		}
		if na != nb {
			return na - nb
		}
	}
	return len(pa) - len(pb)
}

// underAny reports whether path lies in one of dirs, comparing resolved
// paths (macOS /var and /tmp are links into /private).
func underAny(e Env, path string, dirs []string) bool {
	norm := func(p string) string {
		if r, err := e.EvalSymlinks(p); err == nil {
			p = r
		}
		return filepath.Clean(p)
	}
	path = norm(path)
	for _, d := range dirs {
		if d == "" {
			continue
		}
		d = norm(d)
		if path == d || strings.HasPrefix(path, d+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// optional checks a tool forge uses when it's there.
func optional(bin, without, pkg string) func(context.Context, Env) Check {
	return func(_ context.Context, e Env) Check {
		c := Check{Name: bin}
		p, err := e.LookPath(bin)
		if err != nil {
			c.Status, c.Detail = Warn, "not installed (optional): "+without
			c.Fix = install(e.GOOS, pkg)
			return c
		}
		c.Detail = p
		return c
	}
}

func checkPlane(ctx context.Context, e Env) Check {
	c := Check{Name: "plane"}
	switch {
	case !e.PlaneConfigured:
		c.Status, c.Detail = Skip, "not set up (optional): set plane.workspace_slug in the config"
		return c
	case e.PlaneErr != nil:
		c.Status, c.Detail = Fail, e.PlaneErr.Error()
		c.Fix = "export your Plane API key (plane.api_key_env, default PLANE_API_KEY) in your shell profile"
		return c
	case e.PlaneMe == nil:
		c.Status, c.Detail = Skip, "no client"
		return c
	}
	user, err := e.PlaneMe(ctx)
	switch {
	case err != nil && e.IsPlaneAuth != nil && e.IsPlaneAuth(err):
		c.Status, c.Detail = Fail, "API key rejected: "+err.Error()
		c.Fix = "create a new key in Plane (Profile settings → API tokens) and export it"
	case err != nil:
		c.Status, c.Detail = Fail, "can't reach Plane: "+err.Error()
		c.Fix = "check plane.base_url (it must serve /api/v2) and your network"
	default:
		c.Detail = "connected as " + user
	}
	return c
}

// sessionCheck runs bad on every active session and lists those it finds
// a problem with.
func sessionCheck(name string, e Env, fix string, bad func(Session) string) Check {
	c := Check{Name: name}
	if e.SessionsErr != nil {
		c.Status, c.Detail = Warn, "can't list sessions: "+e.SessionsErr.Error()
		return c
	}
	if len(e.Sessions) == 0 {
		c.Detail = "no active sessions (written on start and resume)"
		return c
	}
	var probs []string
	for _, s := range e.Sessions {
		if p := bad(s); p != "" {
			probs = append(probs, s.Name+": "+p)
			c.Sessions = append(c.Sessions, s.Name)
		}
	}
	if len(probs) == 0 {
		c.Detail = fmt.Sprintf("%d active session(s) up to date", len(e.Sessions))
		return c
	}
	c.Status, c.Detail = Fail, strings.Join(probs, "; ")
	c.Fix = fix
	return c
}

const fixSessions = "forge doctor --fix (or resume the session)"

func checkHooks(_ context.Context, e Env) Check {
	return sessionCheck("hooks", e, fixSessions, func(s Session) string {
		reg, err := hooks.Registered(s.Worktree)
		if err != nil {
			return err.Error()
		}
		var missing []string
		stale := ""
		for _, ev := range hooks.Events {
			switch bin, ok := reg[ev]; {
			case !ok:
				missing = append(missing, ev)
			case bin != e.Bin:
				stale = bin
			}
		}
		switch {
		case len(missing) == len(hooks.Events):
			return "not registered"
		case len(missing) > 0:
			return "missing " + strings.Join(missing, ", ")
		case stale != "":
			return "calls " + stale
		}
		return ""
	})
}

func checkMCP(_ context.Context, e Env) Check {
	return sessionCheck("mcp config", e, fixSessions, func(s Session) string {
		bin, err := claude.ReadMCPConfigBin(filepath.Join(s.SessionDir, claude.MCPConfigFile))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return "missing"
		case err != nil:
			return err.Error()
		case bin != e.Bin:
			return "runs " + bin
		}
		return ""
	})
}

func checkSkills(_ context.Context, e Env) Check {
	if !e.SkillsEnabled {
		return Check{Name: "skills", Status: Skip, Detail: "forge skills are off"}
	}
	return sessionCheck("skills", e, fixSessions, func(s Session) string {
		if _, err := os.Stat(filepath.Join(s.SkillsRoot, slashcmd.ManifestFile)); err != nil {
			return "not synced"
		}
		return ""
	})
}
