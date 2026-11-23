package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestOutputView(t *testing.T) {
	keys := DefaultKeyMap()
	o := NewOutputView(NewTheme(true, nil), keys)
	var out strings.Builder
	for i := range 30 {
		fmt.Fprintf(&out, "line %d\n", i)
	}
	o.SetRun(CommandRun{Name: "deploy", Command: "make deploy", Dir: "/wt", Exit: 3, Output: "\x1b[31m" + out.String(), Started: time.Date(2026, 11, 23, 9, 5, 0, 0, time.UTC), Took: time.Second})
	o.SetSize(60, 10)
	v := ansi.Strip(o.View())
	for _, want := range []string{"deploy", "exit 3", "09:05", "$ make deploy", "line 29", "close"} {
		if !strings.Contains(v, want) {
			t.Fatalf("view lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "line 0\n") || strings.Count(v, "\n") != 9 {
		t.Fatalf("want the tail in exactly 10 rows:\n%s", v)
	}
	o.Update(keyMsg("g"))
	if v := ansi.Strip(o.View()); !strings.Contains(v, "line 0") || strings.Contains(v, "line 29") {
		t.Fatalf("top:\n%s", v)
	}
}

func TestOutputViewEmptyAndError(t *testing.T) {
	o := NewOutputView(NewTheme(true, nil), DefaultKeyMap())
	o.SetRun(CommandRun{Name: "x", Err: errors.New("chdir /gone: no such file"), Exit: -1, Truncated: true})
	o.SetSize(60, 8)
	v := ansi.Strip(o.View())
	for _, want := range []string{"chdir /gone", "(no output)", "earlier output dropped"} {
		if !strings.Contains(v, want) {
			t.Fatalf("view lacks %q:\n%s", want, v)
		}
	}
}
