package ui

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// KeyMap holds every key binding in forge. Define bindings only here so the
// help overlay stays accurate. Build one with NewKeyMap; the root model owns
// it and hands a pointer to every tab and widget.
type KeyMap struct {
	Global GlobalKeys
	List   ListKeys
	Input  InputKeys
}

// GlobalKeys work on every tab.
type GlobalKeys struct {
	Quit      key.Binding
	ForceQuit key.Binding // also works while a text input has focus
	Help      key.Binding
	NextTab   key.Binding
	PrevTab   key.Binding
	GotoTab   key.Binding
	Refresh   key.Binding
	Palette   key.Binding // also works while a text input has focus
	Projects  key.Binding // project switcher; also works while a text input has focus
}

// ListKeys drive the list/detail layout every tab uses. Tab-specific groups
// (PR actions, session actions, ...) get their own struct here.
type ListKeys struct {
	Up         key.Binding
	Down       key.Binding
	Top        key.Binding
	Bottom     key.Binding
	PageDown   key.Binding
	PageUp     key.Binding
	Filter     key.Binding
	GrowPane   key.Binding
	ShrinkPane key.Binding
}

// InputKeys apply while a text input or overlay has focus.
type InputKeys struct {
	Cancel     key.Binding
	Accept     key.Binding
	DeleteBack key.Binding
	ClearInput key.Binding
	Next       key.Binding // next result in a picker (palette)
	Prev       key.Binding // previous result in a picker (palette)
}

func bind(desc string, keys ...string) key.Binding {
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(helpKey(keys), desc))
}

// DefaultKeyMap returns the built-in bindings.
func DefaultKeyMap() *KeyMap {
	return &KeyMap{
		Global: GlobalKeys{
			Quit:      bind("quit", "q"),
			ForceQuit: bind("force quit", "ctrl+c"),
			Help:      bind("help", "?"),
			NextTab:   bind("next tab", "tab"),
			PrevTab:   bind("prev tab", "shift+tab"),
			GotoTab:   bind("go to tab", "1", "2", "3", "4", "5", "6", "7", "8", "9"),
			Refresh:   bind("refresh", "r"),
			Palette:   bind("commands", "ctrl+k"),
			Projects:  bind("switch project", "ctrl+p"),
		},
		List: ListKeys{
			Up:         bind("up", "k", "up"),
			Down:       bind("down", "j", "down"),
			Top:        bind("top", "g", "home"),
			Bottom:     bind("bottom", "G", "end"),
			PageDown:   bind("half page down", "ctrl+d"),
			PageUp:     bind("half page up", "ctrl+u"),
			Filter:     bind("filter", "/"),
			GrowPane:   bind("widen list", ">"),
			ShrinkPane: bind("narrow list", "<"),
		},
		Input: InputKeys{
			Cancel:     bind("cancel", "esc"),
			Accept:     bind("accept", "enter"),
			DeleteBack: bind("delete char", "backspace"),
			ClearInput: bind("clear input", "ctrl+u"),
			Next:       bind("next", "down", "ctrl+n"),
			Prev:       bind("prev", "up", "ctrl+p"),
		},
	}
}

// action names a binding for config overrides and palette commands.
type action struct {
	id    string
	scope string // bindings in the same scope must not share keys
	b     *key.Binding
}

const (
	scopeMain  = "main" // global + list keys, active together
	scopeInput = "input"
)

func (k *KeyMap) actions() []action {
	g, l, in := &k.Global, &k.List, &k.Input
	return []action{
		{"quit", scopeMain, &g.Quit},
		{"force_quit", scopeMain, &g.ForceQuit},
		{"help", scopeMain, &g.Help},
		{"next_tab", scopeMain, &g.NextTab},
		{"prev_tab", scopeMain, &g.PrevTab},
		{"goto_tab", scopeMain, &g.GotoTab},
		{"refresh", scopeMain, &g.Refresh},
		{"palette", scopeMain, &g.Palette},
		{"projects", scopeMain, &g.Projects},
		{"up", scopeMain, &l.Up},
		{"down", scopeMain, &l.Down},
		{"top", scopeMain, &l.Top},
		{"bottom", scopeMain, &l.Bottom},
		{"page_down", scopeMain, &l.PageDown},
		{"page_up", scopeMain, &l.PageUp},
		{"filter", scopeMain, &l.Filter},
		{"grow_pane", scopeMain, &l.GrowPane},
		{"shrink_pane", scopeMain, &l.ShrinkPane},
		{"cancel", scopeInput, &in.Cancel},
		{"accept", scopeInput, &in.Accept},
		{"delete_back", scopeInput, &in.DeleteBack},
		{"clear_input", scopeInput, &in.ClearInput},
		{"next", scopeInput, &in.Next},
		{"prev", scopeInput, &in.Prev},
	}
}

// ActionIDs returns the names accepted in the keybinds config section, in
// keymap order.
func ActionIDs() []string {
	acts := DefaultKeyMap().actions()
	ids := make([]string, len(acts))
	for i, a := range acts {
		ids[i] = a.id
	}
	return ids
}

// NewKeyMap returns the default keymap with config overrides (action id ->
// keys) applied. Overriding an action replaces its keys and its help text.
// Unknown actions and keys bound to two actions in the same scope are errors.
func NewKeyMap(overrides map[string][]string) (*KeyMap, error) {
	k := DefaultKeyMap()
	acts := k.actions()
	byID := make(map[string]*action, len(acts))
	for i := range acts {
		byID[acts[i].id] = &acts[i]
	}

	var errs []error
	ids := make([]string, 0, len(overrides))
	for id := range overrides {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a, ok := byID[id]
		if !ok {
			errs = append(errs, fmt.Errorf("keybinds.%s: unknown action; known: %s", id, strings.Join(ActionIDs(), ", ")))
			continue
		}
		keys := overrides[id]
		a.b.SetKeys(keys...)
		a.b.SetHelp(helpKey(keys), a.b.Help().Desc)
	}

	// Report a conflict against the action that was overridden, so the error
	// points at the line the user changed.
	owner := map[string]map[string]string{} // scope -> key -> action id
	for _, a := range acts {
		if owner[a.scope] == nil {
			owner[a.scope] = map[string]string{}
		}
		for _, kk := range a.b.Keys() {
			prev, taken := owner[a.scope][kk]
			if !taken || prev == a.id {
				owner[a.scope][kk] = a.id
				continue
			}
			id, other := a.id, prev
			if _, ok := overrides[id]; !ok {
				id, other = prev, a.id
			}
			errs = append(errs, fmt.Errorf("keybinds.%s: %q is already bound to %s", id, kk, other))
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return k, nil
}

// keyNames are the help-text spellings of special keys.
var keyNames = map[string]string{
	"up":    "↑",
	"down":  "↓",
	"left":  "←",
	"right": "→",
}

// helpKey renders keys for help text: "k/↑", or "1-9" for long runs.
func helpKey(keys []string) string {
	names := make([]string, len(keys))
	for i, k := range keys {
		if n, ok := keyNames[k]; ok {
			k = n
		}
		names[i] = k
	}
	if len(names) > 3 {
		return names[0] + "-" + names[len(names)-1]
	}
	return strings.Join(names, "/")
}

// GlobalHelp returns the bindings that work on every tab, for the help overlay.
func (k *KeyMap) GlobalHelp() []key.Binding {
	g := k.Global
	return []key.Binding{g.Palette, g.Projects, g.GotoTab, g.NextTab, g.PrevTab, g.Refresh, g.Help, g.Quit, g.ForceQuit}
}

// TabIndex returns the zero-based tab index for a GotoTab key press (the
// position of the key in the binding), or -1 if it is not a GotoTab key.
func (k *KeyMap) TabIndex(msg tea.KeyPressMsg) int {
	if !key.Matches(msg, k.Global.GotoTab) {
		return -1
	}
	return slices.Index(k.Global.GotoTab.Keys(), msg.String())
}
