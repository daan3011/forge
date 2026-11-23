package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/daan3011/forge/internal/shell"
)

// SettingsRel is the worktree-relative settings file forge registers its
// hooks in. Claude Code reads it from the session's working directory; it
// must be git-ignored.
const SettingsRel = ".claude/settings.local.json"

// MCPAllowRule auto-approves every tool of forge's MCP server (P6.5), so
// forge tool calls never raise a permission prompt.
const MCPAllowRule = "mcp__forge__*"

// HookTimeout is the per-hook timeout in seconds. forge hook gives up on
// its own well before this.
const HookTimeout = 5

// Command is the hook command line for event: `'<bin>' hook <event>`.
func Command(forgeBin, event string) string {
	return shell.Quote(forgeBin) + " hook " + event
}

// isForgeCommand reports whether cmd is a forge hook registration for any
// event, whatever binary path it was written with.
func isForgeCommand(cmd string) bool {
	_, ok := forgeCommandBin(cmd)
	return ok
}

// forgeCommandBin returns the binary of a forge hook registration, and
// false when cmd isn't one.
func forgeCommandBin(cmd string) (string, bool) {
	bin, event, ok := strings.Cut(strings.TrimSpace(cmd), " hook ")
	if !ok || !slices.Contains(Events, event) {
		return "", false
	}
	bin = strings.TrimSpace(bin)
	if u, ok := strings.CutPrefix(bin, "'"); ok {
		bin = strings.ReplaceAll(strings.TrimSuffix(u, "'"), `'\''`, "'")
	}
	if filepath.Base(bin) != "forge" {
		return "", false
	}
	return bin, true
}

// Install registers forge's hooks for every event in Events in
// <dir>/.claude/settings.local.json, and allows forge's MCP tools
// (MCPAllowRule in permissions.allow). Everything else in the file (other
// settings, other hooks, other permission rules) is kept; forge's earlier
// registrations are replaced and the rule is added once, so installing
// twice is harmless. The file is replaced atomically.
func Install(dir, forgeBin string) error {
	if forgeBin == "" {
		return errors.New("hooks: no forge binary")
	}
	path := filepath.Join(dir, SettingsRel)
	top := map[string]json.RawMessage{}
	switch b, err := os.ReadFile(path); {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return fmt.Errorf("hooks: %w", err)
	case len(bytes.TrimSpace(b)) > 0:
		if err := json.Unmarshal(b, &top); err != nil {
			return fmt.Errorf("hooks: %s: %w", path, err)
		}
	}
	hooks := map[string][]map[string]json.RawMessage{}
	if raw, ok := top["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return fmt.Errorf("hooks: %s: hooks: %w", path, err)
		}
	}
	for ev, groups := range hooks {
		kept, err := withoutForge(groups)
		if err != nil {
			return fmt.Errorf("hooks: %s: hooks.%s: %w", path, ev, err)
		}
		hooks[ev] = kept
	}
	for _, ev := range Events {
		entry, err := json.Marshal([]map[string]any{{
			"type": "command", "command": Command(forgeBin, ev), "timeout": HookTimeout,
		}})
		if err != nil {
			return err
		}
		hooks[ev] = append(hooks[ev], map[string]json.RawMessage{"hooks": entry})
	}
	raw, err := json.Marshal(hooks)
	if err != nil {
		return err
	}
	top["hooks"] = raw
	perms, err := withAllowRule(top["permissions"], MCPAllowRule)
	if err != nil {
		return fmt.Errorf("hooks: %s: permissions: %w", path, err)
	}
	top["permissions"] = perms
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(out, '\n'))
}

// withAllowRule adds rule to permissions.allow, keeping every other key
// and rule; a rule already there is not added again.
func withAllowRule(raw json.RawMessage, rule string) (json.RawMessage, error) {
	perms := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(raw)) > 0 && string(bytes.TrimSpace(raw)) != "null" {
		if err := json.Unmarshal(raw, &perms); err != nil {
			return nil, err
		}
	}
	var allow []json.RawMessage
	if a, ok := perms["allow"]; ok && string(bytes.TrimSpace(a)) != "null" {
		if err := json.Unmarshal(a, &allow); err != nil {
			return nil, fmt.Errorf("allow: %w", err)
		}
	}
	for _, r := range allow {
		var s string
		if json.Unmarshal(r, &s) == nil && s == rule {
			return json.Marshal(perms)
		}
	}
	q, err := json.Marshal(rule)
	if err != nil {
		return nil, err
	}
	allow = append(allow, q)
	a, err := json.Marshal(allow)
	if err != nil {
		return nil, err
	}
	perms["allow"] = a
	return json.Marshal(perms)
}

// withoutForge drops forge's hook commands from groups, and groups left
// with no hooks at all.
func withoutForge(groups []map[string]json.RawMessage) ([]map[string]json.RawMessage, error) {
	out := groups[:0]
	for _, g := range groups {
		var hs []map[string]json.RawMessage
		if raw, ok := g["hooks"]; ok {
			if err := json.Unmarshal(raw, &hs); err != nil {
				return nil, err
			}
		}
		had := len(hs)
		hs = slices.DeleteFunc(hs, func(h map[string]json.RawMessage) bool {
			var cmd string
			return json.Unmarshal(h["command"], &cmd) == nil && isForgeCommand(cmd)
		})
		if len(hs) == had {
			out = append(out, g)
			continue
		}
		if len(hs) == 0 {
			continue
		}
		raw, err := json.Marshal(hs)
		if err != nil {
			return nil, err
		}
		g["hooks"] = raw
		out = append(out, g)
	}
	return out, nil
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("hooks: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return fmt.Errorf("hooks: %w", err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // no-op after the rename
	_, werr := f.Write(data)
	cerr := f.Close()
	if err := errors.Join(werr, cerr, os.Chmod(tmp, 0o644)); err != nil {
		return fmt.Errorf("hooks: write settings: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("hooks: write settings: %w", err)
	}
	return nil
}

// Registered reads <dir>/.claude/settings.local.json and returns, per hook
// event, the forge binary its forge registration calls (events without one
// are absent). A missing file is no registrations, not an error.
func Registered(dir string) (map[string]string, error) {
	out := map[string]string{}
	path := filepath.Join(dir, SettingsRel)
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return out, nil
	case err != nil:
		return nil, fmt.Errorf("hooks: %w", err)
	case len(bytes.TrimSpace(b)) == 0:
		return out, nil
	}
	var top struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &top); err != nil {
		return nil, fmt.Errorf("hooks: %s: %w", path, err)
	}
	for ev, groups := range top.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				if bin, ok := forgeCommandBin(h.Command); ok {
					out[ev] = bin
				}
			}
		}
	}
	return out, nil
}
