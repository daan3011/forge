package ui

import (
	"os"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

func TestDefaultKeyMapIsValid(t *testing.T) {
	k, err := NewKeyMap(nil)
	if err != nil {
		t.Fatalf("defaults conflict: %v", err)
	}
	for _, a := range k.actions() {
		h := a.b.Help()
		if h.Key == "" || h.Desc == "" || len(a.b.Keys()) == 0 {
			t.Fatalf("action %s has keys %v help %+v", a.id, a.b.Keys(), h)
		}
	}
	if got := len(ActionIDs()); got != len(k.actions()) {
		t.Fatalf("ActionIDs has %d entries", got)
	}
}

func TestNewKeyMapOverrides(t *testing.T) {
	tests := []struct {
		name      string
		overrides map[string][]string
		check     func(*KeyMap) key.Binding
		press     string
		wantMatch bool
		wantHelp  string
	}{
		{"default quit", nil, func(k *KeyMap) key.Binding { return k.Global.Quit }, "q", true, "q"},
		{"remapped quit", map[string][]string{"quit": {"Q"}}, func(k *KeyMap) key.Binding { return k.Global.Quit }, "Q", true, "Q"},
		{"old key gone", map[string][]string{"quit": {"Q"}}, func(k *KeyMap) key.Binding { return k.Global.Quit }, "q", false, "Q"},
		{"arrows prettified", map[string][]string{"up": {"up", "w"}}, func(k *KeyMap) key.Binding { return k.List.Up }, "w", true, "↑/w"},
		{"long run collapsed", map[string][]string{"goto_tab": {"V", "W", "X", "Y"}}, func(k *KeyMap) key.Binding { return k.Global.GotoTab }, "W", true, "V-Y"},
		{"session key remapped", map[string][]string{"session_new": {"N"}}, func(k *KeyMap) key.Binding { return k.Sessions.New }, "N", true, "N"},
		{"desc kept", map[string][]string{"refresh": {"f5"}}, func(k *KeyMap) key.Binding { return k.Global.Refresh }, "f5", true, "f5"},
		{"default attach", nil, func(k *KeyMap) key.Binding { return k.Sessions.Attach }, "enter", true, "enter"},
		{"default background resume", nil, func(k *KeyMap) key.Binding { return k.Sessions.Resume }, "b", true, "b"},
		{"default go to session", nil, func(k *KeyMap) key.Binding { return k.Global.GotoSession }, "ctrl+g", true, "ctrl+g"},
		{"default projects", nil, func(k *KeyMap) key.Binding { return k.Global.Projects }, "ctrl+p", true, "ctrl+p"},
		{"remapped projects", map[string][]string{"projects": {"ctrl+o"}}, func(k *KeyMap) key.Binding { return k.Global.Projects }, "ctrl+o", true, "ctrl+o"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k, err := NewKeyMap(tt.overrides)
			if err != nil {
				t.Fatal(err)
			}
			b := tt.check(k)
			if got := key.Matches(keyMsg(tt.press), b); got != tt.wantMatch {
				t.Fatalf("matches %q = %v, want %v", tt.press, got, tt.wantMatch)
			}
			if got := b.Help().Key; got != tt.wantHelp {
				t.Fatalf("help key = %q, want %q", got, tt.wantHelp)
			}
			if b.Help().Desc == "" {
				t.Fatal("override lost the description")
			}
		})
	}
}

func TestNewKeyMapErrors(t *testing.T) {
	tests := []struct {
		name      string
		overrides map[string][]string
		want      string
	}{
		{"unknown action", map[string][]string{"explode": {"x"}}, "keybinds.explode: unknown action; known: quit,"},
		{"conflict names the override", map[string][]string{"refresh": {"q"}}, `keybinds.refresh: "q" is already bound to quit`},
		{"conflict with later action", map[string][]string{"quit": {"j"}}, `keybinds.quit: "j" is already bound to down`},
		{"session keys clash with main keys", map[string][]string{"quit": {"x"}}, `keybinds.quit: "x" is already bound to session_kill`},
		{"tab key clashes with a global one", map[string][]string{"session_resume": {"R"}}, `keybinds.session_resume: "R" is already bound to refresh_all`},
		{"conflict within a tab", map[string][]string{"session_resume": {"n"}}, `keybinds.session_resume: "n" is already bound to session_new`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewKeyMap(tt.overrides)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}

	// Input keys are a separate scope: esc may be reused by a list action.
	if _, err := NewKeyMap(map[string][]string{"filter": {"esc"}}); err != nil {
		t.Fatalf("input scope should not conflict: %v", err)
	}
	// Repeating a key within one action is harmless.
	if _, err := NewKeyMap(map[string][]string{"quit": {"Q", "Q"}}); err != nil {
		t.Fatalf("duplicate within action: %v", err)
	}
}

func TestTabIndex(t *testing.T) {
	remapped, err := NewKeyMap(map[string][]string{"goto_tab": {"!", "@", "#"}})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		keys *KeyMap
		msg  tea.KeyPressMsg
		want int
	}{
		{"1", DefaultKeyMap(), keyMsg("1"), 0},
		{"9", DefaultKeyMap(), keyMsg("9"), 8},
		{"0", DefaultKeyMap(), keyMsg("0"), -1},
		{"j", DefaultKeyMap(), keyMsg("j"), -1},
		{"remapped @", remapped, keyMsg("@"), 1},
		{"remapped digit gone", remapped, keyMsg("1"), -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.keys.TabIndex(tt.msg); got != tt.want {
				t.Fatalf("TabIndex(%q) = %d, want %d", tt.msg.String(), got, tt.want)
			}
		})
	}
}

// TestKeyStrings checks that the key strings in the defaults match what Bubble
// Tea reports for real key presses.
func TestKeyStrings(t *testing.T) {
	k := DefaultKeyMap()
	tests := []struct {
		name string
		msg  tea.KeyPressMsg
		b    key.Binding
	}{
		{"G", tea.KeyPressMsg{Code: 'g', ShiftedCode: 'G', Text: "G", Mod: tea.ModShift}, k.List.Bottom},
		{"?", tea.KeyPressMsg{Code: '?', Text: "?"}, k.Global.Help},
		{"/", tea.KeyPressMsg{Code: '/', Text: "/"}, k.List.Filter},
		{"ctrl+d", tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, k.List.PageDown},
		{"ctrl+u", tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}, k.List.PageUp},
		{"ctrl+c", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, k.Global.ForceQuit},
		{"home", tea.KeyPressMsg{Code: tea.KeyHome}, k.List.Top},
		{"esc", tea.KeyPressMsg{Code: tea.KeyEscape}, k.Input.Cancel},
		{"enter", tea.KeyPressMsg{Code: tea.KeyEnter}, k.Input.Accept},
		{"backspace", tea.KeyPressMsg{Code: tea.KeyBackspace}, k.Input.DeleteBack},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !key.Matches(tt.msg, tt.b) {
				t.Fatalf("%q (%s) does not match %v", tt.name, tt.msg.String(), tt.b.Keys())
			}
		})
	}
}

// TestDefaultConfigListsActions keeps the keybinds docs in the default config
// template in sync with the keymap.
func TestDefaultConfigListsActions(t *testing.T) {
	b, err := os.ReadFile("../config/default.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ActionIDs() {
		if !strings.Contains(string(b), "# "+id+": [") {
			t.Fatalf("default.yaml does not document keybinds.%s", id)
		}
	}
}

func TestConflictsAcrossTabScopes(t *testing.T) {
	b := func(keys ...string) *key.Binding { kb := key.NewBinding(key.WithKeys(keys...)); return &kb }
	tests := []struct {
		name string
		acts []action
		want int
	}{
		{"two tabs may share a key", []action{
			{"session_resume", "tab:sessions", b("b")},
			{"plane_board", "tab:plane", b("b")},
		}, 0},
		{"same tab may not", []action{
			{"session_resume", "tab:sessions", b("b")},
			{"session_other", "tab:sessions", b("b")},
		}, 1},
		{"a tab key may not shadow a main key", []action{
			{"refresh_all", scopeMain, b("R")},
			{"plane_board", "tab:plane", b("R")},
		}, 1},
		{"input keys are separate", []action{
			{"accept", scopeInput, b("enter")},
			{"session_attach", "tab:sessions", b("enter")},
		}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := conflicts(tt.acts, nil); len(got) != tt.want {
				t.Errorf("conflicts = %v, want %d", got, tt.want)
			}
		})
	}
}
