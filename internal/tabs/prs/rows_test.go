package prs

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/github"
)

func TestStaleLevel(t *testing.T) {
	day := 24 * time.Hour
	for _, tt := range []struct {
		name string
		idle time.Duration
		zero bool
		days int
		want staleness
	}{
		{"fresh", time.Hour, false, 5, fresh},
		{"just under", 5*day - time.Second, false, 5, fresh},
		{"at threshold", 5 * day, false, 5, stale},
		{"between", 7 * day, false, 5, stale},
		{"just under twice", 10*day - time.Second, false, 5, stale},
		{"at twice", 10 * day, false, 5, veryStale},
		{"way over", 400 * day, false, 5, veryStale},
		{"off", 400 * day, false, 0, fresh},
		{"negative days", 400 * day, false, -1, fresh},
		{"no time", 0, true, 5, fresh},
	} {
		t.Run(tt.name, func(t *testing.T) {
			at := now.Add(-tt.idle)
			if tt.zero {
				at = time.Time{}
			}
			if got := staleLevel(at, now, tt.days); got != tt.want {
				t.Errorf("staleLevel(%s idle, %d days) = %d, want %d", tt.idle, tt.days, got, tt.want)
			}
		})
	}
}

func TestUpdatedCell(t *testing.T) {
	day := 24 * time.Hour
	for _, tt := range []struct {
		idle  time.Duration
		age   string
		level staleness
	}{
		{time.Hour, "1h", fresh},
		{6 * day, "6d", stale},
		{12 * day, "1w", veryStale},
	} {
		p := pr("acme/x", 1, "t", func(p *github.PR) { p.UpdatedAt = now.Add(-tt.idle) })
		cell := prRow(p, now, 5)[colUpdated]
		if tt.level == fresh && cell != tt.age {
			t.Errorf("fresh cell = %q, want plain %q", cell, tt.age)
		}
		if a, l := splitUpdatedCell(cell); a != tt.age || l != tt.level {
			t.Errorf("%s idle: cell %q splits to (%q, %d), want (%q, %d)", tt.idle, cell, a, l, tt.age, tt.level)
		}
	}
	if a, l := splitUpdatedCell("3d" + staleSep + "x"); a != "3d" || l != fresh {
		t.Errorf("bad level: (%q, %d)", a, l)
	}
}

func TestRenderUpdated(t *testing.T) {
	tb := newTestTab(nil, newKV())
	row := lipgloss.NewStyle()
	for _, tt := range []struct {
		cell  string
		lines []string
	}{
		{"1h", []string{"1h"}},
		{updatedCell("6d", stale), []string{"6d", "◷   "}},
		{updatedCell("2w", veryStale), []string{"2w", "◷   "}},
	} {
		got := strings.Split(ansi.Strip(tb.renderUpdated(tt.cell, 4, row)), "\n")
		if strings.Join(got, "|") != strings.Join(tt.lines, "|") {
			t.Errorf("renderUpdated(%q) = %q, want %q", tt.cell, got, tt.lines)
		}
	}
	// Amber and red differ from the plain age and from each other.
	plain := tb.renderUpdated("6d", 4, row)
	amber := tb.renderUpdated(updatedCell("6d", stale), 4, row)
	red := tb.renderUpdated(updatedCell("6d", veryStale), 4, row)
	a1, _, _ := strings.Cut(amber, "\n")
	r1, _, _ := strings.Cut(red, "\n")
	if plain == a1 || a1 == r1 {
		t.Errorf("stale ages should be coloured: plain %q amber %q red %q", plain, a1, r1)
	}
}
