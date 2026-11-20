package ui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestTableRowHeight(t *testing.T) {
	theme := NewTheme(true, nil)
	tb := NewTable(theme, DefaultKeyMap(), []Column{
		{Title: "#", Width: 3},
		{Title: "Title", Flex: true, Render: func(cell string, _ int, row lipgloss.Style) string {
			a, b, _ := strings.Cut(cell, "\n")
			return row.Render(strings.ToUpper(a)) + "\n" + row.Render(b)
		}},
		{Title: "id", Hidden: true},
	})
	tb.SetRowHeight(2)
	rows := make([][]string, 6)
	for i := range rows {
		rows[i] = []string{string(rune('1' + i)), "repo" + string(rune('a'+i)) + "\ntitle " + string(rune('a'+i)), "x" + string(rune('a'+i))}
	}
	tb.SetRows(rows)
	tb.SetSize(20, 7) // header + 3 two-line rows

	v := ansi.Strip(tb.View())
	assertSize(t, tb.View(), 20, 7)
	lines := strings.Split(v, "\n")
	if !strings.HasPrefix(lines[1], "1   REPOA") || !strings.HasPrefix(lines[2], "    title a") {
		t.Fatalf("two-line row wrong:\n%s", v)
	}
	if strings.Contains(v, "xa") {
		t.Fatalf("hidden column drawn:\n%s", v)
	}
	for range 4 {
		tb.Update(keyMsg("j"))
	}
	v = ansi.Strip(tb.View())
	if tb.Cursor() != 4 || !strings.Contains(v, "REPOE") || strings.Contains(v, "REPOA") {
		t.Fatalf("cursor %d not scrolled by rows:\n%s", tb.Cursor(), v)
	}
	tb.Update(keyMsg("ctrl+u")) // half a page of 3 rows = 1 row
	if tb.Cursor() != 3 {
		t.Fatalf("half page up: cursor %d", tb.Cursor())
	}
	tb.SetFilter("xb") // hidden cells still match
	if len(tb.VisibleRows()) != 1 || tb.SelectedRow()[0] != "2" {
		t.Fatalf("filter on hidden column: %v", tb.VisibleRows())
	}

	// Plain columns split on newlines too; a missing line is blank.
	plain := NewTable(theme, DefaultKeyMap(), []Column{{Title: "A", Flex: true}})
	plain.SetRowHeight(2)
	plain.SetRows([][]string{{"only"}})
	plain.SetSize(10, 3)
	assertSize(t, plain.View(), 10, 3)
	if pl := strings.Split(ansi.Strip(plain.View()), "\n"); strings.TrimSpace(pl[1]) != "only" || strings.TrimSpace(pl[2]) != "" {
		t.Fatalf("plain two-line:\n%s", plain.View())
	}
}

func TestSectionBar(t *testing.T) {
	theme := NewTheme(true, nil)
	titles := []string{"My PRs", "Review requested", "Involved"}
	counts := []int{3, 0, -1}
	wide := ansi.Strip(SectionBar(theme, titles, counts, 0, 80))
	if !strings.Contains(wide, "My PRs (3) │ Review requested (0) │ Involved ") || strings.Contains(wide, "(-1)") {
		t.Fatalf("wide bar = %q", wide)
	}
	mid := ansi.Strip(SectionBar(theme, titles, counts, 0, 30))
	if !strings.Contains(mid, "My PRs (3)") || strings.Contains(mid, "Review") || !strings.Contains(mid, "(0)") {
		t.Fatalf("compact bar = %q", mid)
	}
	narrow := ansi.Strip(SectionBar(theme, titles, counts, 0, 16))
	if strings.Contains(narrow, "My PRs") {
		t.Fatalf("narrow bar = %q", narrow)
	}
	for _, w := range []int{80, 30, 16, 5} {
		if got := lipgloss.Width(SectionBar(theme, titles, counts, 1, w)); got != w {
			t.Errorf("width %d: got %d", w, got)
		}
	}
	if SectionBar(theme, titles, counts, 0, 0) != "" {
		t.Error("zero width should render nothing")
	}
}

func TestStatusBarDetail(t *testing.T) {
	sb := NewStatusBar(NewTheme(true, nil))
	k := DefaultKeyMap()
	now := time.Now()
	info := StatusInfo{
		Tab: "PRs", Project: "forge", Updated: now.Add(-7 * time.Second), Now: now,
		Detail: "@daan · PR 3/3 (fetched 3)",
		Keys:   []key.Binding{k.List.Filter, k.Global.Help},
	}
	wide := ansi.Strip(sb.View(140, info))
	if !strings.Contains(wide, "updated 7s ago · @daan · PR 3/3 (fetched 3) · / filter") {
		t.Fatalf("wide = %q", wide)
	}
	// Hints go first, then the detail.
	mid := ansi.Strip(sb.View(60, info))
	if !strings.Contains(mid, "fetched 3") || strings.Contains(mid, "filter") {
		t.Fatalf("mid = %q", mid)
	}
	narrow := ansi.Strip(sb.View(40, info))
	if strings.Contains(narrow, "fetched") || !strings.Contains(narrow, "updated 7s ago") {
		t.Fatalf("narrow = %q", narrow)
	}
}

func TestShortAge(t *testing.T) {
	d := 24 * time.Hour
	tests := map[time.Duration]string{
		10 * time.Second: "now", 5 * time.Minute: "5m", 3 * time.Hour: "3h", 2 * d: "2d",
		8 * d: "1w", 45 * d: "1mo", 400 * d: "1y", 800 * d: "2y",
	}
	for in, want := range tests {
		if got := ShortAge(in); got != want {
			t.Errorf("ShortAge(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestPRIcons(t *testing.T) {
	th := NewTheme(true, nil)
	for _, tt := range []struct {
		f    func(string) (string, lipgloss.Style)
		in   string
		want string
	}{
		{th.ReviewIcon, "APPROVED", "✓"},
		{th.ReviewIcon, "CHANGES_REQUESTED", "✗"},
		{th.ReviewIcon, "", "·"},
		{th.CIIcon, "SUCCESS", "✓"},
		{th.CIIcon, "ERROR", "✗"},
		{th.CIIcon, "PENDING", "●"},
		{th.PRIcon, "open", "⑂"},
		{th.PRIcon, "draft", "◌"},
		{th.PRIcon, "merged", "⊕"},
	} {
		if got, _ := tt.f(tt.in); got != tt.want {
			t.Errorf("icon(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if o := NewTheme(true, map[string]string{"patina": "#00ff00"}); !near(o.Success, lipgloss.Color("#00ff00")) {
		t.Error("patina override not applied")
	}
}
