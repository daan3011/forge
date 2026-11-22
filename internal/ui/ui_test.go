package ui

import (
	"errors"
	"image/color"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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
		{"hidden takes no space", []Column{{Width: 5}, {Hidden: true, Width: 9}, {Flex: true}}, 30, []int{5, 0, 24}},
		{"hidden last, no flex", []Column{{Width: 3}, {Hidden: true}}, 10, []int{10, 0}},
		{"hidden while too narrow", []Column{{Width: 10}, {Width: 10}, {Hidden: true}}, 12, []int{10, 1, 0}},
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
	case "ctrl+d":
		return tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
	case "ctrl+u":
		return tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
	case "ctrl+p":
		return tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}
	case "ctrl+o":
		return tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func TestTableCursorAndScroll(t *testing.T) {
	tb := NewTable(NewTheme(true, nil), DefaultKeyMap(), []Column{{Title: "N", Flex: true}})
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
	tb := NewTable(NewTheme(true, nil), DefaultKeyMap(), []Column{{Title: "N", Flex: true}})
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
	k := DefaultKeyMap()
	now := time.Now()
	for _, w := range []int{80, 120, 40} {
		got := sb.View(w, StatusInfo{
			Tab: "Pipelines", Project: "forge",
			Message: strings.Repeat("a very long message ", 10),
			Updated: now.Add(-42 * time.Second), Now: now,
			Keys: []key.Binding{k.Global.Help},
		})
		if lipgloss.Width(got) != w {
			t.Fatalf("width %d: got %d", w, lipgloss.Width(got))
		}
		if w >= 80 && (!strings.Contains(got, "updated 42s ago") || !strings.Contains(got, "help")) {
			t.Fatalf("missing right side: %q", got)
		}
	}
}

func TestStatusBarRefreshState(t *testing.T) {
	sb := NewStatusBar(NewTheme(true, nil))
	k := DefaultKeyMap()
	now := time.Now()
	hints := []key.Binding{k.Global.Refresh, k.List.Filter, k.Global.Help}
	tests := []struct {
		name       string
		width      int
		info       StatusInfo
		want, gone []string
	}{
		{
			name:  "fetching shows spinner instead of age",
			width: 100,
			info:  StatusInfo{Fetching: true, Spinner: "⣾", Updated: now.Add(-3 * time.Minute)},
			want:  []string{"⣾ refreshing"},
			gone:  []string{"updated"},
		},
		{
			name:  "activity replaces refreshing",
			width: 100,
			info:  StatusInfo{Fetching: true, Spinner: "⣾", Activity: "merging #12", Updated: now.Add(-3 * time.Minute)},
			want:  []string{"⣾ merging #12…"},
			gone:  []string{"updated", "refreshing"},
		},
		{
			name:  "done shows age",
			width: 100,
			info:  StatusInfo{Updated: now.Add(-3 * time.Minute)},
			want:  []string{"updated 3m ago"},
			gone:  []string{"refreshing"},
		},
		{
			name:  "rate badge",
			width: 100,
			info:  StatusInfo{Rate: "gh 312/5000 · resets 14m"},
			want:  []string{"gh 312/5000 · resets 14m", "? help"},
		},
		{
			name:  "rate badge outlives hints",
			width: 50,
			info:  StatusInfo{Rate: "gh 12/5000"},
			want:  []string{"gh 12/5000"},
			gone:  []string{"r refresh"},
		},
		{
			name:  "waiting badge",
			width: 100,
			info:  StatusInfo{Waiting: 2, Rate: "gh 12/5000"},
			want:  []string{"● 2 waiting", "gh 12/5000", "? help"},
		},
		{
			name:  "waiting badge outlives hints",
			width: 40,
			info:  StatusInfo{Waiting: 1},
			want:  []string{"● 1 waiting"},
			gone:  []string{"r refresh"},
		},
		{
			name:  "no waiting badge at zero",
			width: 100,
			gone:  []string{"waiting"},
		},
		{
			name:  "rate badge dropped when nothing fits",
			width: 16,
			info:  StatusInfo{Rate: "gh 12/5000 · resets 14m"},
			gone:  []string{"gh 12"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := tt.info
			info.Tab, info.Project, info.Now, info.Keys = "PRs", "forge", now, hints
			got := sb.View(tt.width, info)
			if lipgloss.Width(got) != tt.width {
				t.Fatalf("width = %d, want %d", lipgloss.Width(got), tt.width)
			}
			plain := ansi.Strip(got)
			for _, w := range tt.want {
				if !strings.Contains(plain, w) {
					t.Errorf("missing %q in %q", w, plain)
				}
			}
			for _, g := range tt.gone {
				if strings.Contains(plain, g) {
					t.Errorf("%q should not be in %q", g, plain)
				}
			}
		})
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

func TestStatusBarHints(t *testing.T) {
	sb := NewStatusBar(NewTheme(true, nil))
	k := DefaultKeyMap()
	hints := []key.Binding{k.Global.Refresh, k.List.Filter, k.List.Up, k.List.Down, k.Global.Help}
	tests := []struct {
		width      int
		want, gone []string
	}{
		{200, []string{"r refresh", "/ filter", "k/↑ up", "j/↓ down", "? help"}, nil},
		{60, []string{"? help"}, []string{"r refresh"}},
		{14, nil, []string{"? help"}},
	}
	for _, tt := range tests {
		got := sb.View(tt.width, StatusInfo{Tab: "PRs", Message: "hi", Keys: hints})
		if lipgloss.Width(got) != tt.width {
			t.Fatalf("width %d: got %d", tt.width, lipgloss.Width(got))
		}
		plain := ansi.Strip(got)
		for _, w := range tt.want {
			if !strings.Contains(plain, w) {
				t.Fatalf("width %d: missing %q in %q", tt.width, w, plain)
			}
		}
		for _, g := range tt.gone {
			if strings.Contains(plain, g) {
				t.Fatalf("width %d: %q should be dropped from %q", tt.width, g, plain)
			}
		}
	}
}

func TestTableJumps(t *testing.T) {
	tb := NewTable(NewTheme(true, nil), DefaultKeyMap(), []Column{{Title: "N", Flex: true}})
	rows := make([][]string, 20)
	for i := range rows {
		rows[i] = []string{string(rune('a' + i))}
	}
	tb.SetRows(rows)
	tb.SetSize(10, 9) // 8 body rows, half page = 4

	steps := []struct {
		key  string
		want int
	}{
		{"G", 19}, {"g", 0}, {"ctrl+d", 4}, {"ctrl+d", 8}, {"ctrl+u", 4}, {"ctrl+u", 0}, {"ctrl+u", 0},
	}
	for _, s := range steps {
		tb.Update(keyMsg(s.key))
		if tb.Cursor() != s.want {
			t.Fatalf("after %s: cursor = %d, want %d", s.key, tb.Cursor(), s.want)
		}
		assertSize(t, tb.View(), 10, 9)
	}
}

func TestTableFilter(t *testing.T) {
	tb := NewTable(NewTheme(true, nil), DefaultKeyMap(), []Column{{Title: "#", Width: 3}, {Title: "Title", Flex: true}})
	tb.SetRows([][]string{{"1", "Add retry"}, {"2", "Bump deps"}, {"3", "Bump go"}})
	tb.SetSize(30, 5)
	tb.Update(keyMsg("G"))

	tb.SetFilter("BUMP")
	if n := len(tb.VisibleRows()); n != 2 {
		t.Fatalf("visible = %d, want 2", n)
	}
	if tb.SelectedRow()[1] != "Bump go" {
		t.Fatalf("cursor not clamped into filtered rows: %v", tb.SelectedRow())
	}
	if len(tb.Rows()) != 3 {
		t.Fatal("Rows should ignore the filter")
	}

	tb.SetFilter("nothing")
	if tb.SelectedRow() != nil || !strings.Contains(tb.View(), "no matches") {
		t.Fatalf("expected no matches:\n%s", tb.View())
	}
	assertSize(t, tb.View(), 30, 5)

	tb.SetFilter("")
	if len(tb.VisibleRows()) != 3 {
		t.Fatal("empty filter should show every row")
	}
}

func TestFilterInput(t *testing.T) {
	f := NewFilter(NewTheme(true, nil), DefaultKeyMap())
	if f.Update(keyMsg("a")) {
		t.Fatal("inactive filter should ignore keys")
	}
	f.Start()
	for _, k := range []string{"b", "u", "x", "backspace", "m"} {
		f.Update(keyMsg(k))
	}
	if f.Value() != "bum" {
		t.Fatalf("value = %q", f.Value())
	}
	if f.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}) {
		t.Fatal("ctrl combos should not be typed")
	}
	assertSize(t, f.View(12), 12, 1)
	f.Update(keyMsg("ctrl+u"))
	if f.Value() != "" {
		t.Fatalf("clear input left %q", f.Value())
	}
	f.Stop()
	if f.Active() || f.Visible() {
		t.Fatal("stopped empty filter should be hidden")
	}
}

func TestHelpView(t *testing.T) {
	k := DefaultKeyMap()
	h := NewHelpView(NewTheme(true, nil), k)
	h.SetContent([]HelpSection{
		{Title: "Global", Groups: [][]key.Binding{k.GlobalHelp()}},
		{Title: "PRs", Groups: [][]key.Binding{{k.List.Up, k.List.Down}, {k.List.Filter}}},
	})
	for _, sz := range [][2]int{{80, 24}, {200, 50}, {30, 8}} {
		h.SetSize(sz[0], sz[1])
		assertSize(t, h.View(), sz[0], sz[1])
	}

	h.SetSize(100, 40)
	plain := strings.Join(strings.Fields(ansi.Strip(h.View())), " ")
	for _, want := range []string{"Global", "PRs", "q quit", "1-9 go to tab", "k/↑ up", "/ filter", "esc close"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("help missing %q:\n%s", want, plain)
		}
	}

	// Too small to fit: scrolling moves the content and stays in range.
	h.SetSize(40, 6)
	top := h.View()
	h.Update(keyMsg("j"))
	if h.View() == top {
		t.Fatal("j should scroll the help")
	}
	for range 50 {
		h.Update(keyMsg("j"))
	}
	assertSize(t, h.View(), 40, 6)
	h.Update(keyMsg("g"))
	if h.View() != top {
		t.Fatal("g should scroll back to the top")
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

func TestTableEmptyView(t *testing.T) {
	tb := NewTable(NewTheme(true, nil), DefaultKeyMap(), []Column{{Title: "N", Flex: true}})
	tb.SetSize(30, 6)
	var gotW, gotH int
	tb.SetEmptyView(func(w, h int) string {
		gotW, gotH = w, h
		return Box("cold forge", w, h)
	})
	if !strings.Contains(tb.View(), "cold forge") || gotW != 30 || gotH != 5 {
		t.Fatalf("empty view not used (got %dx%d):\n%s", gotW, gotH, tb.View())
	}
	assertSize(t, tb.View(), 30, 6)

	// A filter that matches nothing still says so.
	tb.SetRows([][]string{{"a"}})
	tb.SetFilter("zzz")
	if v := tb.View(); strings.Contains(v, "cold forge") || !strings.Contains(v, "no matches") {
		t.Fatalf("filtered-out table used the empty view:\n%s", v)
	}
}

func TestSplitPaneShowRatio(t *testing.T) {
	s := NewSplitPane(NewTheme(true, nil))
	s.SetSize(100, 20)
	s.Grow()
	target := s.Ratio()
	s.ShowRatio(DefaultSplitRatio)
	if s.Ratio() != target || s.ShownRatio() != DefaultSplitRatio {
		t.Fatalf("ratio %v shown %v", s.Ratio(), s.ShownRatio())
	}
	if lw, _ := s.LeftSize(); lw != 40-borderSize {
		t.Fatalf("left laid out at %d, want the shown ratio", lw)
	}
	s.ShowRatio(5)
	if s.ShownRatio() != maxRatio {
		t.Fatalf("shown ratio not clamped: %v", s.ShownRatio())
	}
	assertSize(t, s.View("l", "r", true), 100, 20)
}

func TestSuccessToast(t *testing.T) {
	toast := NewToast(NewTheme(true, nil))
	toast.Show("a much longer older message", ToastInfo)
	toast.Show("merged", ToastSuccess)
	view := ansi.Strip(toast.View(40))
	if !strings.Contains(view, SuccessMark+"merged") {
		t.Fatalf("success mark missing:\n%s", view)
	}
	w, h := toast.LastSize(40)
	if w != lipgloss.Width(SuccessMark+"merged")+4 || h != 3 {
		t.Fatalf("last toast size %dx%d", w, h)
	}
	empty := NewToast(NewTheme(true, nil))
	if w, h := empty.LastSize(40); w != 0 || h != 0 {
		t.Fatal("empty stack has a size")
	}
}

func TestMix(t *testing.T) {
	a, b := Charcoal, WhiteHot
	if Mix(a, b, 0) != Mix(a, b, -3) || Mix(a, b, 1) != Mix(a, b, 9) {
		t.Fatal("Mix not clamped")
	}
	r0, _, _, _ := Mix(a, b, 0).RGBA()
	r1, _, _, _ := Mix(a, b, 1).RGBA()
	ra, _, _, _ := a.RGBA()
	rb, _, _, _ := b.RGBA()
	if r0>>8 != ra>>8 || r1>>8 != rb>>8 {
		t.Fatal("Mix endpoints wrong")
	}
}

func TestTableColumnRender(t *testing.T) {
	var calls []string
	cols := []Column{
		{Title: "St", Width: 6, Render: func(cell string, width int, row lipgloss.Style) string {
			calls = append(calls, cell)
			return row.Render(fit("* "+cell, width))
		}},
		{Title: "Name", Flex: true},
	}
	tb := NewTable(NewTheme(true, nil), DefaultKeyMap(), cols)
	tb.SetRows([][]string{{"idle", "alpha"}, {"waiting", "beta"}})
	tb.SetSize(24, 4)
	v := ansi.Strip(tb.View())
	if !strings.Contains(v, "St") || strings.Contains(v, "* St") {
		t.Errorf("header must not be custom-rendered:\n%s", v)
	}
	if !strings.Contains(v, "* idle alpha") || !strings.Contains(v, "* wai… beta") {
		t.Errorf("cells not rendered:\n%s", v)
	}
	if len(calls) != 2 {
		t.Errorf("render calls %q", calls)
	}
	assertSize(t, tb.View(), 24, 4)
	tb.SetFilter("waiting") // raw text is what the filter sees
	if n := len(tb.VisibleRows()); n != 1 {
		t.Errorf("filter matched %d rows", n)
	}
}

// TestSplitPaneRightOrigin checks RightOrigin against the rendered view:
// the right pane's first content cell is where it says (P4.7 places sixel
// images there).
func TestSplitPaneRightOrigin(t *testing.T) {
	s := NewSplitPane(NewTheme(true, nil))
	s.SetSize(100, 10)
	x, y, ok := s.RightOrigin()
	if !ok {
		t.Fatal("not collapsed at 100 columns")
	}
	rw, _ := s.RightSize()
	view := s.View("", "R"+strings.Repeat(" ", rw-1), true)
	line := ansi.Strip(strings.Split(view, "\n")[y])
	if got := ansi.Cut(line, x, x+1); got != "R" {
		t.Fatalf("cell (%d,%d) = %q in %q", x, y, got, line)
	}
	s.SetSize(40, 10)
	if _, _, ok := s.RightOrigin(); ok {
		t.Fatal("collapsed pane has no origin")
	}
}
