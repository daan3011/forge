package plane

import (
	"strings"
	"testing"
)

func TestBranchFor(t *testing.T) {
	tests := []struct {
		name string
		item WorkItem
		want string
	}{
		{"simple", WorkItem{Identifier: "PROJ-123", Name: "Fix login"}, "proj-123-fix-login"},
		{"punctuation", WorkItem{Identifier: "PROJ-1", Name: "Fix: login (OAuth) -- again!"}, "proj-1-fix-login-oauth-again"},
		{"accents", WorkItem{Identifier: "WEB-7", Name: "Café crème für Straße"}, "web-7-cafe-creme-fur-strasse"},
		{"combining marks", WorkItem{Identifier: "WEB-7", Name: "Café"}, "web-7-cafe"},
		{"other scripts dropped", WorkItem{Identifier: "WEB-8", Name: "日本 deploy"}, "web-8-deploy"},
		{"empty title", WorkItem{Identifier: "PROJ-9"}, "proj-9"},
		{"title of junk", WorkItem{Identifier: "PROJ-9", Name: "!!!"}, "proj-9"},
		{"no identifier uses id", WorkItem{ID: "abc", Name: "x"}, "abc-x"},
		{
			"long title cut at a word",
			WorkItem{Identifier: "PROJ-123", Name: "Make the dashboard load much faster on slow connections please"},
			"proj-123-make-the-dashboard-load-much-faster-on",
		},
		{
			"one long word cut hard",
			WorkItem{Identifier: "P-1", Name: strings.Repeat("a", 80)},
			"p-1-" + strings.Repeat("a", 46),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BranchFor(tt.item)
			if got != tt.want {
				t.Errorf("BranchFor = %q, want %q", got, tt.want)
			}
			if len(got) > MaxBranchLen {
				t.Errorf("len %d > %d", len(got), MaxBranchLen)
			}
		})
	}
}

func TestFindKey(t *testing.T) {
	ids := []string{"PROJ", "WEB", "PROJX"}
	tests := []struct {
		name  string
		ids   []string
		texts []string
		want  string
	}{
		{"branch", ids, []string{"proj-123-fix-login", ""}, "PROJ-123"},
		{"title", ids, []string{"feature/x", "Fix login (WEB-7)"}, "WEB-7"},
		{"branch wins over title", ids, []string{"web-1-a", "PROJ-2 b"}, "WEB-1"},
		{"mid title", ids, []string{"", "fix: PROJ-45 crash on start"}, "PROJ-45"},
		{"prefix path", ids, []string{"daan/proj-8-x"}, "PROJ-8"},
		{"longer identifier", ids, []string{"projx-3"}, "PROJX-3"},
		{"unknown identifier", ids, []string{"utf-8", "p3-7 notes"}, ""},
		{"inside a word", ids, []string{"myproj-3", "proj-3a"}, ""},
		{"no identifiers", nil, []string{"proj-1"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := FindKey(tt.ids, tt.texts...)
			if got != tt.want || ok != (tt.want != "") {
				t.Errorf("FindKey = %q, %v, want %q", got, ok, tt.want)
			}
		})
	}
}
