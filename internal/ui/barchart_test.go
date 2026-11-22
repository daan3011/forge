package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestBarChart(t *testing.T) {
	th := NewTheme(true, nil)
	tests := []struct {
		name   string
		values []float64
		labels []string
		width  int
		height int
		want   []string // rows without colour
	}{
		{"empty", nil, nil, 10, 2, nil},
		{
			"heights",
			[]float64{0, 4, 8, 2},
			[]string{"a", "b", "c", "d"},
			8, 2,
			[]string{"    █", "· █ █ ▄", "a b c d"},
		},
		{"all zero", []float64{0, 0}, []string{"x", "y"}, 4, 1, []string{"· ·", "x y"}},
		{
			"narrow thins labels",
			[]float64{1, 1, 1, 1},
			[]string{"01", "02", "03", "04"},
			4, 1,
			[]string{"████", "01"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BarChart(th, tt.values, tt.labels, tt.width, tt.height)
			if tt.want == nil {
				if got != "" {
					t.Fatalf("got %q, want empty", got)
				}
				return
			}
			rows := strings.Split(ansi.Strip(got), "\n")
			if len(rows) != len(tt.want) {
				t.Fatalf("rows = %q, want %q", rows, tt.want)
			}
			for i := range rows {
				if rows[i] != tt.want[i] {
					t.Errorf("row %d = %q, want %q", i, rows[i], tt.want[i])
				}
				if w := ansi.StringWidth(rows[i]); w > tt.width {
					t.Errorf("row %d is %d wide, max %d", i, w, tt.width)
				}
			}
		})
	}
}
