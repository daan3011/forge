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
	bin, event, ok := strings.Cut(strings.TrimSpace(cmd), " hook ")
	if !ok || !slices.Contains(Events, event) {
		return false
	}
	bin = strings.TrimSpace(bin)
	if u, ok := strings.CutPrefix(bin, "'"); ok {
		bin = strings.ReplaceAll(strings.TrimSuffix(u, "'"), `'\''`, "'")
	}
	return filepath.Base(bin) == "forge"
}

// Install registers forge's hooks for every event in Events in
// <dir>/.claude/settings.local.json. Everything else in the file (other
// settings, other hooks) is kept; forge's earlier registrations are
// replaced, so installing twice is harmless. The file is replaced
// atomically.
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
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(out, '\n'))
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
