package ui

import (
	"strings"

	"charm.land/bubbles/v2/key"
)

// Binding kinds for the key inventory (`forge keys --dump`, P6.8).
const (
	KindGlobal  = "global"  // live on every tab
	KindTab     = "tab"     // live on one tab, with the global keys
	KindOverlay = "overlay" // a viewer or pane that takes over the screen
	KindModal   = "modal"   // a text input, picker or form with focus
	KindProgram = "program" // its own full-screen program (setup, demos)
)

// BindingInfo describes one binding for the key inventory. Fixed bindings
// can't be changed in the keybinds config (Huh form keys, the hidden
// demos); Hidden ones belong to development tools.
type BindingInfo struct {
	ID     string
	Kind   string
	Scope  string
	Keys   []string
	Desc   string
	Fixed  bool
	Hidden bool
}

// Bindings lists every binding in the keymap: the configurable actions in
// keymap order, then the custom commands, the keys of embedded Huh forms
// and the hidden demos. It reads the keymap itself, so it is always in step
// with what forge does.
func (k *KeyMap) Bindings() []BindingInfo {
	var out []BindingInfo
	for _, a := range k.actions() {
		kind, scope := kindOf(a.scope)
		out = append(out, info(a.id, kind, scope, *a.b, false, false))
	}
	for _, c := range k.Custom {
		kind, scope := KindGlobal, "main"
		if len(c.Tabs) > 0 {
			kind, scope = KindTab, strings.ToLower(strings.Join(c.Tabs, ","))
		}
		out = append(out, info("custom:"+c.Name, kind, scope, c.Binding, false, false))
	}
	for _, f := range k.formBindings() {
		out = append(out, info(f.id, KindModal, "form", f.b, true, false))
	}
	d, t := k.Demo, k.Tmux
	for _, f := range []struct {
		id    string
		scope string
		b     key.Binding
	}{
		{"demo_anim_replay", "demo anim", d.Replay},
		{"demo_anim_burst", "demo anim", d.Burst},
		{"demo_anim_full", "demo anim", d.Full},
		{"demo_anim_reduced", "demo anim", d.Reduced},
		{"demo_anim_off", "demo anim", d.Off},
		{"demo_anim_quit", "demo anim", d.Quit},
		{"demo_tmux_attach", "demo tmux", t.Attach},
		{"demo_tmux_restart", "demo tmux", t.Restart},
		{"demo_tmux_kill", "demo tmux", t.Kill},
		{"demo_tmux_quit", "demo tmux", t.Quit},
	} {
		out = append(out, info(f.id, KindProgram, f.scope, f.b, true, true))
	}
	return out
}

type namedBinding struct {
	id string
	b  key.Binding
}

// formBindings are the keys of the Huh forms and confirm dialogs embedded in
// forge (HuhKeyMap). They are Huh's, not configurable.
func (k *KeyMap) formBindings() []namedBinding {
	km := k.HuhKeyMap()
	return []namedBinding{
		{"form_cancel", km.Quit},
		{"form_next_field", km.Input.Next},
		{"form_prev_field", km.Input.Prev},
		{"form_submit", km.Input.Submit},
		{"form_accept_suggestion", km.Input.AcceptSuggestion},
		{"form_text_newline", km.Text.NewLine},
		{"form_text_editor", km.Text.Editor},
		{"form_select_up", km.Select.Up},
		{"form_select_down", km.Select.Down},
		{"form_select_filter", km.Select.Filter},
		{"form_multiselect_toggle", km.MultiSelect.Toggle},
		{"form_multiselect_all", km.MultiSelect.SelectAll},
		{"form_confirm_toggle", km.Confirm.Toggle},
		{"form_confirm_accept", km.Confirm.Accept},
		{"form_confirm_reject", km.Confirm.Reject},
		{"form_confirm_submit", km.Confirm.Submit},
	}
}

func info(id, kind, scope string, b key.Binding, fixed, hidden bool) BindingInfo {
	return BindingInfo{ID: id, Kind: kind, Scope: scope, Keys: b.Keys(), Desc: b.Help().Desc, Fixed: fixed, Hidden: hidden}
}

// kindOf maps an action scope to its inventory kind and scope name.
func kindOf(scope string) (kind, name string) {
	switch {
	case scope == scopeMain:
		return KindGlobal, scope
	case isTabScope(scope):
		return KindTab, strings.TrimPrefix(scope, "tab:")
	case scope == scopeTranscript, scope == scopeDiff, scope == scopeLog, scope == scopeNote:
		return KindOverlay, scope
	case scope == scopeSetup:
		return KindProgram, scope
	default: // input, capture, filepicker
		return KindModal, scope
	}
}
