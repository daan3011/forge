package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestHighlight(t *testing.T) {
	th := NewTheme(true, nil)
	got := Highlight("an Error and error", "error", th.Body, th.LogMatch)
	if ansi.Strip(got) != "an Error and error" {
		t.Errorf("highlight changed text: %q", ansi.Strip(got))
	}
	if !strings.Contains(got, th.LogMatch.Render("E")) {
		t.Error("match not highlighted")
	}
	if Highlight("plain", "", th.Body, th.LogMatch) != th.Body.Render("plain") {
		t.Error("empty query should render plainly")
	}
}
