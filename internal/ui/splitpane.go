package ui

import (
	"math"
	"strings"

	"charm.land/lipgloss/v2"
)

// SplitPane lays out a list (left) and a detail pane (right) side by side,
// each in a rounded border. The split is adjustable with Grow and Shrink.
type SplitPane struct {
	theme    *Theme
	ratio    float64 // target ratio, changed by Grow and Shrink
	shown    float64 // ratio the panes are laid out at; see ShowRatio
	width    int
	height   int
	leftOut  int // outer width of the left pane, borders included
	rightOut int // outer width of the right pane; 0 when collapsed
}

// Split pane tuning.
const (
	DefaultSplitRatio = 0.4
	SplitStep         = 0.05
	MinLeftWidth      = 30
	MinRightWidth     = 24
	minRatio          = 0.2
	maxRatio          = 0.8
	borderSize        = 2 // one cell of border on each side
)

// NewSplitPane returns a split pane with the default ratio.
func NewSplitPane(theme *Theme) SplitPane {
	return SplitPane{theme: theme, ratio: DefaultSplitRatio, shown: DefaultSplitRatio}
}

// SetSize sets the total outer size of both panes.
func (s *SplitPane) SetSize(width, height int) {
	s.width, s.height = max(width, 0), max(height, 0)
	s.layout()
}

// Ratio returns the target share of the width given to the left pane.
func (s *SplitPane) Ratio() float64 { return s.ratio }

// ShownRatio returns the share the panes are currently laid out at.
func (s *SplitPane) ShownRatio() float64 { return s.shown }

// ShowRatio lays the panes out at r without changing the target ratio, so
// an owner can spring the divider towards Ratio. r is clamped like Ratio.
func (s *SplitPane) ShowRatio(r float64) {
	s.shown = min(max(r, minRatio), maxRatio)
	s.layout()
}

// Grow widens the left pane by one step.
func (s *SplitPane) Grow() { s.setRatio(s.ratio + SplitStep) }

// Shrink narrows the left pane by one step.
func (s *SplitPane) Shrink() { s.setRatio(s.ratio - SplitStep) }

func (s *SplitPane) setRatio(r float64) {
	// Round to avoid float drift after many steps.
	r = math.Round(r*100) / 100
	s.ratio = min(max(r, minRatio), maxRatio)
	s.shown = s.ratio
	s.layout()
}

func (s *SplitPane) layout() {
	if s.width < MinLeftWidth+MinRightWidth {
		s.leftOut, s.rightOut = s.width, 0
		return
	}
	left := int(math.Round(float64(s.width) * s.shown))
	left = min(max(left, MinLeftWidth), s.width-MinRightWidth)
	s.leftOut, s.rightOut = left, s.width-left
}

// Collapsed reports whether the right pane is hidden for lack of space.
func (s *SplitPane) Collapsed() bool { return s.rightOut == 0 }

// LeftSize returns the inner size of the left pane.
func (s *SplitPane) LeftSize() (width, height int) {
	return max(s.leftOut-borderSize, 0), max(s.height-borderSize, 0)
}

// RightSize returns the inner size of the right pane (zero when collapsed).
func (s *SplitPane) RightSize() (width, height int) {
	if s.rightOut == 0 {
		return 0, 0
	}
	return max(s.rightOut-borderSize, 0), max(s.height-borderSize, 0)
}

// View renders both panes at exactly the pane's size. focusLeft picks which
// pane gets the ember border.
func (s *SplitPane) View(left, right string, focusLeft bool) string {
	if s.width == 0 || s.height == 0 {
		return ""
	}
	lw, lh := s.LeftSize()
	leftView := s.pane(left, lw, lh, focusLeft || s.Collapsed())
	if s.Collapsed() {
		return leftView
	}
	rw, rh := s.RightSize()
	return lipgloss.JoinHorizontal(lipgloss.Top, leftView, s.pane(right, rw, rh, !focusLeft))
}

func (s *SplitPane) pane(content string, w, h int, focused bool) string {
	style := s.theme.Pane
	if focused {
		style = s.theme.PaneFocused
	}
	return style.Render(Box(content, w, h))
}

// Box clips or pads content to exactly width x height cells.
func Box(content string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i, l := range lines {
		lines[i] = fit(l, width)
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}
