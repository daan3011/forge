package ui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func pickerKeys(p *FilePicker, keys ...tea.KeyPressMsg) {
	for _, k := range keys {
		p.Update(k)
	}
}

func typeText(p *FilePicker, s string) {
	for _, r := range s {
		p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

var (
	tabKey   = tea.KeyPressMsg{Code: tea.KeyTab}
	ctrlA    = tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}
	enterKey = tea.KeyPressMsg{Code: tea.KeyEnter}
	escKey   = tea.KeyPressMsg{Code: tea.KeyEscape}
)

func newTestPicker(pre ...string) *FilePicker {
	paths := []string{"cmd/main.go", "internal/auth/token.go", "internal/auth/session.go", "README.md"}
	return NewFilePicker(NewTheme(true, nil), DefaultKeyMap(), "Files", paths, pre)
}

func TestFilePickerFilter(t *testing.T) {
	p := newTestPicker()
	if got := len(p.Visible()); got != 4 {
		t.Fatalf("empty query shows %d, want 4", got)
	}
	typeText(p, "authtok")
	if got := p.Visible(); !slices.Equal(got, []string{"internal/auth/token.go"}) {
		t.Fatalf("visible = %v", got)
	}
	p.Update(keyMsg("backspace"))
	if p.Query() != "authto" {
		t.Fatalf("query = %q", p.Query())
	}
	typeText(p, "zzz")
	if v := ansi.Strip(p.View(40, 6)); !strings.Contains(v, "no matches") {
		t.Fatalf("view = %q", v)
	}
}

func TestFilePickerToggle(t *testing.T) {
	p := newTestPicker()
	pickerKeys(p, tabKey, tabKey) // marks the first two, moving down
	if got := p.Selected(); !slices.Equal(got, []string{"cmd/main.go", "internal/auth/token.go"}) {
		t.Fatalf("selected = %v", got)
	}
	pickerKeys(p, keyMsg("up"), tabKey) // unmark token.go
	if got := p.Selected(); !slices.Equal(got, []string{"cmd/main.go"}) {
		t.Fatalf("after unmark = %v", got)
	}
	typeText(p, "auth")
	pickerKeys(p, ctrlA)
	if got := p.Selected(); len(got) != 3 {
		t.Fatalf("toggle all = %v", got)
	}
	pickerKeys(p, ctrlA)
	if got := p.Selected(); !slices.Equal(got, []string{"cmd/main.go"}) {
		t.Fatalf("toggle all off = %v", got)
	}
	pickerKeys(p, enterKey)
	if !p.Done() || p.Aborted() {
		t.Fatal("enter should finish")
	}
}

func TestFilePickerPreselectAndCancel(t *testing.T) {
	p := newTestPicker("README.md", "gone.go")
	if got := p.Selected(); !slices.Equal(got, []string{"README.md", "gone.go"}) {
		t.Fatalf("preselected = %v", got)
	}
	v := ansi.Strip(p.View(60, 8))
	if !strings.Contains(v, "2 selected · 4/4") || !strings.Contains(v, "● README.md") || !strings.Contains(v, "○ cmd/main.go") {
		t.Fatalf("view = %q", v)
	}
	if lines := strings.Split(p.View(60, 8), "\n"); len(lines) != 8 {
		t.Fatalf("view has %d lines, want 8", len(lines))
	}
	pickerKeys(p, escKey)
	if !p.Aborted() || p.Done() {
		t.Fatal("esc should abort")
	}
}

func TestFilePickerScroll(t *testing.T) {
	p := newTestPicker()
	for range 3 {
		p.Update(keyMsg("down"))
	}
	v := ansi.Strip(p.View(40, 5)) // two body lines
	if !strings.Contains(v, "README.md") || strings.Contains(v, "cmd/main.go") {
		t.Fatalf("view should scroll to the cursor: %q", v)
	}
}
