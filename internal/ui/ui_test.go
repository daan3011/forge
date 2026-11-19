package ui

import (
	"errors"
	"image/color"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func rgb(c color.Color) [3]uint32 {
	r, g, b, _ := c.RGBA()
	return [3]uint32{r >> 8, g >> 8, b >> 8}
}

func near(a, b color.Color) bool {
	x, y := rgb(a), rgb(b)
	for i := range x {
		d := int(x[i]) - int(y[i])
		if d < -2 || d > 2 {
			return false
		}
	}
	return true
}

func TestHeat(t *testing.T) {
	tests := []struct {
		name string
		t    float64
		want color.Color
	}{
		{"cold", 0, DarkRedHeat},
		{"white-hot", 1, WhiteHot},
		{"clamp low", -1, DarkRedHeat},
		{"clamp high", 2, WhiteHot},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Heat(tt.t); !near(got, tt.want) {
				t.Fatalf("Heat(%v) = %v, want %v", tt.t, rgb(got), rgb(tt.want))
			}
		})
	}
	mid := rgb(Heat(0.5))
	if mid[0] < 0xc0 || mid[1] < 0x60 {
		t.Fatalf("Heat(0.5) = %v, want an orange/yellow", mid)
	}
}

func TestHeatRamp(t *testing.T) {
	for _, n := range []int{0, 1, 2, 5, 100} {
		r := HeatRamp(n)
		if len(r) != n {
			t.Fatalf("HeatRamp(%d) has %d colours", n, len(r))
		}
		if n >= 2 && (!near(r[0], DarkRedHeat) || !near(r[n-1], WhiteHot)) {
			t.Fatalf("HeatRamp(%d) endpoints = %v..%v", n, rgb(r[0]), rgb(r[n-1]))
		}
	}
}

func TestNewTheme(t *testing.T) {
	dark, light := NewTheme(true, nil), NewTheme(false, nil)
	if near(dark.Bg, light.Bg) {
		t.Fatal("light and dark backgrounds should differ")
	}
	if !near(dark.Bg, Charcoal) || !near(dark.Accent, Ember) {
		t.Fatal("dark theme should use charcoal and ember")
	}
	custom := NewTheme(true, map[string]string{"ember": "#00ff00", "bogus": "#ffffff"})
	if !near(custom.Accent, lipgloss.Color("#00ff00")) {
		t.Fatalf("override not applied: %v", rgb(custom.Accent))
	}
}

func TestColumnWidths(t *testing.T) {
	tests := []struct {
		name  string
		cols  []Column
		total int
		want  []int
	}{
		{"flex fills", []Column{{Width: 5}, {Flex: true}, {Width: 4}}, 30, []int{5, 19, 4}},
		{"two flex share", []Column{{Flex: true}, {Flex: true}}, 11, []int{5, 5}},
		{"no flex pads last", []Column{{Width: 3}, {Width: 3}}, 10, []int{3, 6}},
		{"too narrow drops right", []Column{{Width: 10}, {Width: 10}}, 12, []int{10, 1}},
		{"zero", []Column{{Width: 3}}, 0, []int{0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := columnWidths(tt.cols, tt.total)
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("columnWidths = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestFit(t *testing.T) {
	tests := []struct {
		in    string
		width int
		want  string
	}{
		{"hello", 8, "hello   "},
		{"hello world", 8, "hello w…"},
		{"日本語テキスト", 7, "日本語…"},
		{"x", 0, ""},
	}
	for _, tt := range tests {
		got := fit(tt.in, tt.width)
		if strings.TrimRight(got, " ") != strings.TrimRight(tt.want, " ") || lipgloss.Width(got) != tt.width {
			t.Fatalf("fit(%q, %d) = %q (w=%d), want %q", tt.in, tt.width, got, lipgloss.Width(got), tt.want)
		}
	}
}

func keyMsg(s string) tea.KeyPressMsg {
	switch s {
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func TestTableCursorAndScroll(t *testing.T) {
	tb := NewTable(NewTheme(true, nil), []Column{{Title: "N", Flex: true}})
	rows := make([][]string, 10)
	for i := range rows {
		rows[i] = []string{string(rune('a' + i))}
	}
	tb.SetRows(rows)
	tb.SetSize(10, 4) // header + 3 rows

	tb.Update(keyMsg("k"))
	if tb.Cursor() != 0 {
		t.Fatalf("cursor went above 0: %d", tb.Cursor())
	}
	for range 5 {
		tb.Update(keyMsg("j"))
	}
	if tb.Cursor() != 5 {
		t.Fatalf("cursor = %d, want 5", tb.Cursor())
	}
	view := tb.View()
	if !strings.Contains(view, "f") || strings.Contains(view, "a ") {
		t.Fatalf("selected row not scrolled into view:\n%s", view)
	}
	for range 20 {
		tb.Update(keyMsg("down"))
	}
	if tb.Cursor() != 9 || tb.SelectedRow()[0] != "j" {
		t.Fatalf("cursor = %d, want 9", tb.Cursor())
	}
	assertSize(t, view, 10, 4)
}

func TestTableEmpty(t *testing.T) {
	tb := NewTable(NewTheme(true, nil), []Column{{Title: "N", Flex: true}})
	tb.SetSize(30, 5)
	if tb.SelectedRow() != nil {
		t.Fatal("empty table should have no selection")
	}
	if !strings.Contains(tb.View(), EmptyText) {
		t.Fatal("empty table should show the empty text")
	}
	assertSize(t, tb.View(), 30, 5)
}

func TestSplitPane(t *testing.T) {
	th := NewTheme(true, nil)
	for _, w := range []int{80, 120, 200} {
		s := NewSplitPane(th)
		s.SetSize(w, 20)
		lw, _ := s.LeftSize()
		rw, rh := s.RightSize()
		if lw+rw+2*borderSize != w || rh != 18 {
			t.Fatalf("w=%d: left %d + right %d + borders != total", w, lw, rw)
		}
		assertSize(t, s.View("left", "right", true), w, 20)
	}

	s := NewSplitPane(th)
	s.SetSize(80, 20)
	for range 20 {
		s.Shrink()
	}
	if lw, _ := s.LeftSize(); lw+borderSize < MinLeftWidth {
		t.Fatalf("left pane below minimum: %d", lw)
	}
	for range 20 {
		s.Grow()
	}
	if rw, _ := s.RightSize(); rw+borderSize < MinRightWidth {
		t.Fatalf("right pane below minimum: %d", rw)
	}
	if s.Ratio() > maxRatio {
		t.Fatalf("ratio %v above max", s.Ratio())
	}

	s.SetSize(40, 10)
	if !s.Collapsed() {
		t.Fatal("split should collapse below the minimum widths")
	}
	assertSize(t, s.View("left", "right", false), 40, 10)
}

func TestFormatAgo(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "just now"},
		{12 * time.Second, "12s ago"},
		{3 * time.Minute, "3m ago"},
		{5 * time.Hour, "5h ago"},
		{50 * time.Hour, "2d ago"},
	}
	for _, tt := range tests {
		if got := FormatAgo(tt.d); got != tt.want {
			t.Fatalf("FormatAgo(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestStatusBarWidth(t *testing.T) {
	sb := NewStatusBar(NewTheme(true, nil))
	now := time.Now()
	for _, w := range []int{80, 120, 40} {
		got := sb.View(w, StatusInfo{
			Tab: "Pipelines", Project: "forge",
			Message: strings.Repeat("a very long message ", 10),
			Updated: now.Add(-42 * time.Second), Now: now,
		})
		if lipgloss.Width(got) != w {
			t.Fatalf("width %d: got %d", w, lipgloss.Width(got))
		}
		if w >= 80 && (!strings.Contains(got, "updated 42s ago") || !strings.Contains(got, "help")) {
			t.Fatalf("missing right side: %q", got)
		}
	}
}

func TestTabBar(t *testing.T) {
	th := NewTheme(true, nil)
	titles := []string{"Home", "PRs", "Pipelines", "Plane", "Sessions", "Docs", "Artifacts", "Context"}
	wide := TabBar(th, titles, 2, 200)
	assertSize(t, wide, 200, TabBarHeight)
	if !strings.Contains(wide, "Sessions") {
		t.Fatal("wide tab bar should show every title")
	}
	narrow := TabBar(th, titles, 2, 80)
	assertSize(t, narrow, 80, TabBarHeight)
	if !strings.Contains(narrow, "3 Pipelines") || strings.Contains(narrow, "Sessions") {
		t.Fatalf("narrow tab bar should only spell out the active tab:\n%s", narrow)
	}
}

func TestToast(t *testing.T) {
	toast := NewToast(NewTheme(true, nil))
	cmd := toast.Show(errors.New("boom").Error(), ToastError)
	if cmd == nil || toast.Empty() || !strings.Contains(toast.View(30), "boom") {
		t.Fatal("toast should show")
	}
	if w := lipgloss.Width(toast.View(20)); w > 20 {
		t.Fatalf("toast wider than max: %d", w)
	}
	if toast.Update(InfoMsg{}) {
		t.Fatal("non-toast message should not be consumed")
	}
	if !toast.Update(toastExpireMsg{id: 1}) || !toast.Empty() {
		t.Fatal("toast should expire")
	}
	for range maxToasts + 2 {
		toast.Show("x", ToastInfo)
	}
	if len(toast.items) != maxToasts {
		t.Fatalf("toasts = %d, want %d", len(toast.items), maxToasts)
	}
}

func TestTabIndex(t *testing.T) {
	tests := []struct {
		msg  tea.KeyPressMsg
		want int
	}{
		{keyMsg("1"), 0},
		{keyMsg("9"), 8},
		{keyMsg("0"), -1},
		{keyMsg("j"), -1},
	}
	for _, tt := range tests {
		if got := Keys.TabIndex(tt.msg); got != tt.want {
			t.Fatalf("TabIndex(%q) = %d, want %d", tt.msg.String(), got, tt.want)
		}
	}
}

// assertSize checks that s is exactly height lines of exactly width cells.
func assertSize(t *testing.T, s string, width, height int) {
	t.Helper()
	lines := strings.Split(s, "\n")
	if len(lines) != height {
		t.Fatalf("got %d lines, want %d:\n%s", len(lines), height, s)
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w != width {
			t.Fatalf("line %d is %d wide, want %d: %q", i, w, width, l)
		}
	}
}
