package slashcmd

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/daan3011/forge/internal/templates"
)

// Shadower is what hides a forge skill behind the bare /<name>.
type Shadower string

const (
	// ShadowRepo means the repo has .claude/skills/<name>/ or
	// .claude/commands/<name>.md.
	ShadowRepo Shadower = "repo"
	// ShadowPersonal means ~/.claude has the same.
	ShadowPersonal Shadower = "personal"
)

// Badge is the hint shown next to a shadowed command.
func (s Shadower) Badge(name string) string {
	switch s {
	case ShadowRepo:
		return "shadowed by repo — use /forge:" + name
	case ShadowPersonal:
		return "shadowed by your personal skill — use /forge:" + name
	}
	return ""
}

// ClaudeHome is the user's Claude Code folder: $CLAUDE_CONFIG_DIR, else
// ~/.claude. It is only ever read.
func ClaudeHome() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// Shadow reports whether a skill or command called name in checkout (a
// worktree or project path; may be empty) or in the user's Claude Code
// folder wins the bare /<name> over forge's. Claude Code runs project and
// personal skills before an --add-dir one, which stays reachable as
// /forge:<name>. It only reads.
func Shadow(checkout, name string) (Shadower, bool) {
	if checkout != "" && defines(filepath.Join(checkout, ".claude"), name) {
		return ShadowRepo, true
	}
	if h := ClaudeHome(); h != "" && defines(h, name) {
		return ShadowPersonal, true
	}
	return "", false
}

func defines(claudeDir, name string) bool {
	if st, err := os.Stat(filepath.Join(claudeDir, "skills", name)); err == nil && st.IsDir() {
		return true
	}
	st, err := os.Stat(filepath.Join(claudeDir, "commands", name+".md"))
	return err == nil && !st.IsDir()
}

// Catalog loads the commands from their sources on every call, so edits to
// overrides and templates show up on the next sync.
type Catalog struct {
	// OverridesDir is Dir(); TemplatesDir is templates.Dir(). Empty means
	// none.
	OverridesDir string
	TemplatesDir string
}

// NewCatalog uses the default folders.
func NewCatalog() Catalog {
	var c Catalog
	c.OverridesDir, _ = Dir()
	c.TemplatesDir, _ = templates.Dir()
	return c
}

// Commands returns every command after overrides. Bad override or template
// files are skipped and reported.
func (c Catalog) Commands() ([]Command, []error) {
	var errs []error
	var tpls []templates.Template
	if c.TemplatesDir != "" {
		var terrs []error
		tpls, terrs = templates.Load(c.TemplatesDir)
		errs = append(errs, terrs...)
	}
	var ovs []Command
	if c.OverridesDir != "" {
		var oerrs []error
		ovs, oerrs = LoadOverrides(c.OverridesDir)
		errs = append(errs, oerrs...)
	}
	return Merge(Builtins(), FromTemplates(tpls), ovs), errs
}

// Sync writes every command into root (see Install). Bad source files are
// reported in the error, the rest are still written.
func (c Catalog) Sync(root string, vals map[string]string) (Result, error) {
	cmds, errs := c.Commands()
	res, err := Install(root, cmds, vals)
	return res, errors.Join(append(errs, err)...)
}

// Status is one command and what shadows it in a checkout.
type Status struct {
	Command
	Shadow Shadower // "" when nothing does
}

// Status lists the commands with their shadowing in checkout (may be "").
func (c Catalog) Status(checkout string) ([]Status, []error) {
	cmds, errs := c.Commands()
	out := make([]Status, len(cmds))
	for i, cmd := range cmds {
		out[i].Command = cmd
		out[i].Shadow, _ = Shadow(checkout, cmd.Name)
	}
	return out, errs
}

// EnsureOverride copies name's current content (built-in or template) to
// the override file if there is none yet, and returns its path. A name
// that matches no command starts from an empty skill.
func (c Catalog) EnsureOverride(name string) (string, error) {
	if err := ValidName(name); err != nil {
		return "", err
	}
	if c.OverridesDir == "" {
		return "", errors.New("slashcmd: no overrides folder")
	}
	path := filepath.Join(c.OverridesDir, name+".md")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	cmds, _ := c.Commands()
	data := []byte("---\nname: " + name + "\ndescription: \ndisable-model-invocation: true\n---\n$ARGUMENTS\n")
	if cmd, ok := Find(cmds, name); ok {
		data = cmd.Content
	}
	if err := os.MkdirAll(c.OverridesDir, 0o755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return path, werr
}

// Reset removes name's override; it is not an error when there is none.
func (c Catalog) Reset(name string) error {
	if err := ValidName(name); err != nil {
		return err
	}
	if c.OverridesDir == "" {
		return nil
	}
	err := os.Remove(filepath.Join(c.OverridesDir, name+".md"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
