package templates

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/daan3011/forge/internal/testutil"
)

func TestDefaultsEndWithToolsSection(t *testing.T) {
	for _, tm := range Defaults() {
		if !strings.HasSuffix(tm.Body, ToolsSection) {
			t.Errorf("%s does not end with the tools section", tm.Name)
		}
		if strings.Contains(ToolsSection, "{") {
			t.Fatal("tools section must not contain placeholders")
		}
		if _, missing := Render(tm.Body, map[string]string{}); len(missing) > len(tm.Vars()) {
			t.Errorf("%s: section adds placeholders", tm.Name)
		}
	}
	if !strings.HasPrefix(ToolsSection, ToolsMarker) {
		t.Error("section must start with the marker")
	}
}

func TestSync(t *testing.T) {
	testutil.IsolateHome(t)
	dir := t.TempDir()
	files := map[string]string{
		"old.md":       "---\nname: old\n---\nDo the thing.\n",
		"nonewline.md": "Do it",
		"blank.md":     "Body\n\n",
		"current.md":   "Body\n\n" + ToolsSection,
		"notes.txt":    "not a template",
	}
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Dry run reports but writes nothing.
	ch, err := Sync(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch) != 3 {
		t.Fatalf("dry run changes = %+v", ch)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "old.md")); string(b) != files["old.md"] {
		t.Fatal("dry run wrote")
	}

	ch, err = Sync(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range ch {
		names = append(names, filepath.Base(c.Path))
		if !strings.Contains(c.Diff(), "+forge tools (mcp__forge__*)") {
			t.Errorf("diff: %s", c.Diff())
		}
	}
	if strings.Join(names, ",") != "blank.md,nonewline.md,old.md" {
		t.Errorf("changed %v", names)
	}
	want := map[string]string{
		"old.md":       files["old.md"] + "\n" + ToolsSection,
		"nonewline.md": "Do it\n\n" + ToolsSection,
		"blank.md":     "Body\n\n" + ToolsSection,
		"current.md":   files["current.md"],
		"notes.txt":    files["notes.txt"],
	}
	for n, w := range want {
		b, _ := os.ReadFile(filepath.Join(dir, n))
		if string(b) != w {
			t.Errorf("%s =\n%q\nwant\n%q", n, b, w)
		}
	}
	if fi, _ := os.Stat(filepath.Join(dir, "old.md")); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode changed to %v", fi.Mode())
	}
	// A synced template still parses with its front matter.
	b, _ := os.ReadFile(filepath.Join(dir, "old.md"))
	if tm, err := Parse("old.md", b); err != nil || tm.Name != "old" {
		t.Errorf("parse after sync: %+v %v", tm, err)
	}

	// Idempotent.
	ch, err = Sync(dir, false)
	if err != nil || len(ch) != 0 {
		t.Errorf("second sync: %+v %v", ch, err)
	}
}

func TestSyncMissingDirAndErrors(t *testing.T) {
	if ch, err := Sync(filepath.Join(t.TempDir(), "nope"), false); err != nil || ch != nil {
		t.Errorf("missing dir: %v %v", ch, err)
	}
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("permissions")
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.md")
	_ = os.WriteFile(bad, []byte("x"), 0o000)
	_ = os.WriteFile(filepath.Join(dir, "good.md"), []byte("y\n"), 0o644)
	ch, err := Sync(dir, false)
	if err == nil || !strings.Contains(err.Error(), "bad.md") {
		t.Errorf("err = %v", err)
	}
	if len(ch) != 1 || filepath.Base(ch[0].Path) != "good.md" {
		t.Errorf("changes = %+v", ch)
	}
}

func TestInsertBeforeTools(t *testing.T) {
	tests := []struct {
		name, prompt, want string
	}{
		{"no section", "Do it.\n", "Do it.\n\nRead @a.md\n"},
		{"before section", "Do it.\n\n" + ToolsSection, "Do it.\n\nRead @a.md\n\n" + ToolsSection},
		{"only section", ToolsSection, "Read @a.md\n\n" + ToolsSection},
	}
	for _, tt := range tests {
		if got := InsertBeforeTools(tt.prompt, "Read @a.md"); got != tt.want {
			t.Errorf("%s:\n%q\nwant\n%q", tt.name, got, tt.want)
		}
	}
}
