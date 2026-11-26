package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

// Inventory kinds beyond the keymap's (ui.Kind*).
const (
	// KindAgentWindow are the keys forge's tmux.conf binds in agent windows.
	KindAgentWindow = "agent-window"
	// KindPalette are palette commands that have no key.
	KindPalette = "palette"
)

// KeyEntry is one line of the key inventory (`forge keys --dump`, P6.8).
type KeyEntry struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Scope   string   `json:"scope"`
	Keys    []string `json:"keys"`
	Desc    string   `json:"description"`
	Palette string   `json:"palette,omitempty"` // the palette command's title
	Fixed   bool     `json:"fixed,omitempty"`   // not changeable in the keybinds config
	Hidden  bool     `json:"hidden,omitempty"`  // a hidden development tool
}

// paletteAliases are actions whose palette command has another id: the
// viewers' keys that do what a tab command does.
var paletteAliases = map[string]string{
	"diff_open":        "pr_open",
	"log_rerun_failed": "pipeline_rerun_failed",
	"log_rerun":        "pipeline_rerun",
	"log_cancel":       "pipeline_cancel",
	"log_fix":          "pipeline_fix",
	"log_fix_edit":     "pipeline_fix_edit",
}

// viewerCommander is a tab with a full-screen viewer whose palette commands
// only show while it is open.
type viewerCommander interface {
	ViewerCommands() []tabs.Command
}

// KeyInventory lists every key binding forge has, with its palette command:
// the keymap (with cfg's overrides and custom commands), the keys of
// embedded forms, the agent-window keys of the default tmux.conf, and the
// palette commands that have no key. It builds the root model without
// running it, so it does no I/O.
func KeyInventory(cfg *config.Config) ([]KeyEntry, error) {
	m, err := New(cfg,
		WithDoctor(func(context.Context) (string, bool) { return "", false }),
		WithSetup(func(string) *exec.Cmd { return nil }),
		WithTemplates("templates", nil),
	)
	if err != nil {
		return nil, err
	}
	// palette id -> title. Shared commands (filter, grow_pane, ...) keep
	// the first tab's title.
	titles := map[string]string{}
	var order []string
	add := func(id, title string) {
		if _, ok := titles[id]; ok {
			return
		}
		order = append(order, id)
		titles[id] = title
	}
	for i, t := range m.tabs {
		m.active = i
		for _, it := range m.paletteItems() {
			if dynamicItem(it.id) {
				continue
			}
			add(it.id, it.title)
		}
		if v, ok := t.(viewerCommander); ok {
			for _, c := range v.ViewerCommands() {
				add(c.ID, c.Title)
			}
		}
	}

	var out []KeyEntry
	used := map[string]bool{}
	for _, b := range m.keys.Bindings() {
		e := KeyEntry{ID: b.ID, Kind: b.Kind, Scope: b.Scope, Keys: b.Keys, Desc: b.Desc, Fixed: b.Fixed, Hidden: b.Hidden}
		pid := b.ID
		if a, ok := paletteAliases[pid]; ok {
			pid = a
		}
		if t, ok := titles[pid]; ok {
			e.Palette = t
			used[pid] = true
		} else if b.ID == "goto_tab" {
			e.Palette = "Tabs: <tab name>"
		}
		if e.Keys == nil {
			e.Keys = []string{}
		}
		out = append(out, e)
	}
	out = append(out, tmuxKeys()...)
	for _, id := range order {
		if used[id] {
			continue
		}
		out = append(out, KeyEntry{ID: id, Kind: KindPalette, Scope: "palette", Keys: []string{}, Palette: titles[id]})
	}
	return out, nil
}

// dynamicItem reports palette entries built from data (tabs, projects,
// entities), not commands.
func dynamicItem(id string) bool {
	for _, p := range []string{"tab:", "project:", "entity:", "output:"} {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}

var tmuxBindRe = regexp.MustCompile(`^bind(?:-key)?\s+-n\s+(\S+)\s+(.+)$`)

// tmuxKeys reads the root-table bindings of the default tmux.conf, each
// described by the comment line above it.
func tmuxKeys() []KeyEntry {
	conf := tmux.DefaultConfig(tmux.ConfigColors{Bg: "#000000", Fg: "#ffffff", Accent: "#ff0000", Muted: "#888888"})
	var out []KeyEntry
	comment := ""
	sc := bufio.NewScanner(bytes.NewReader(conf))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if c, ok := strings.CutPrefix(line, "#"); ok {
			comment = strings.TrimSpace(c)
			continue
		}
		mm := tmuxBindRe.FindStringSubmatch(line)
		if mm == nil {
			comment = ""
			continue
		}
		desc := comment
		if _, after, ok := strings.Cut(comment, ": "); ok {
			desc = after
		}
		desc = strings.TrimSuffix(desc, ".")
		cmd := strings.Fields(mm[2])[0]
		out = append(out, KeyEntry{
			ID: "tmux_" + strings.ReplaceAll(cmd, "-", "_"), Kind: KindAgentWindow, Scope: "tmux",
			Keys: []string{tmuxKeyName(mm[1])}, Desc: desc, Fixed: true,
		})
		comment = ""
	}
	return out
}

// tmuxKeyName spells a tmux key the way forge's keymap does: "M-d" is
// "alt+d".
func tmuxKeyName(k string) string {
	return strings.ToLower(tmux.KeyLabel(k))
}

// RenderKeysJSON renders the inventory as indented JSON.
func RenderKeysJSON(entries []KeyEntry) ([]byte, error) {
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// kindOrder and kindTitles lay out the Markdown inventory.
var (
	kindOrder  = []string{ui.KindGlobal, ui.KindTab, ui.KindOverlay, ui.KindModal, ui.KindProgram, KindAgentWindow, KindPalette}
	kindTitles = map[string]string{
		ui.KindGlobal:   "Global (every tab)",
		ui.KindTab:      "Tabs",
		ui.KindOverlay:  "Overlays (full-screen viewers and panes)",
		ui.KindModal:    "Modal (inputs, pickers, forms)",
		ui.KindProgram:  "Programs (setup, hidden demos)",
		KindAgentWindow: "Agent windows (forge's tmux.conf)",
		KindPalette:     "Palette commands without a key",
	}
)

// RenderKeysMarkdown renders the inventory as Markdown tables, grouped by
// kind and then scope, in inventory order.
func RenderKeysMarkdown(entries []KeyEntry) []byte {
	var b strings.Builder
	b.WriteString("<!-- generated by `forge keys --dump`; do not edit by hand -->\n")
	for _, kind := range kindOrder {
		var scopes []string
		byScope := map[string][]KeyEntry{}
		for _, e := range entries {
			if e.Kind != kind {
				continue
			}
			if !slices.Contains(scopes, e.Scope) {
				scopes = append(scopes, e.Scope)
			}
			byScope[e.Scope] = append(byScope[e.Scope], e)
		}
		if len(scopes) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n", kindTitles[kind])
		for _, s := range scopes {
			if len(scopes) > 1 || (kind != ui.KindGlobal && kind != KindAgentWindow && kind != KindPalette) {
				fmt.Fprintf(&b, "\n### %s\n", s)
			}
			if kind == KindPalette {
				b.WriteString("\n| Action id | Palette command |\n| --- | --- |\n")
				for _, e := range byScope[s] {
					fmt.Fprintf(&b, "| `%s` | %s |\n", e.ID, cell(e.Palette))
				}
				continue
			}
			b.WriteString("\n| Keys | Action id | Does | Palette command |\n| --- | --- | --- | --- |\n")
			for _, e := range byScope[s] {
				id := "`" + e.ID + "`"
				if e.Fixed {
					id += " (fixed)"
				}
				if e.Hidden {
					id += " (hidden)"
				}
				fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", keysCell(e.Keys), id, cell(e.Desc), cell(e.Palette))
			}
		}
	}
	return []byte(b.String())
}

func keysCell(keys []string) string {
	if len(keys) == 0 {
		return "—"
	}
	parts := make([]string, len(keys))
	for i, k := range keys {
		if k == "|" {
			k = `\|`
		}
		parts[i] = "`" + k + "`"
	}
	return strings.Join(parts, " ")
}

func cell(s string) string {
	if s == "" {
		return "—"
	}
	return strings.ReplaceAll(s, "|", `\|`)
}
