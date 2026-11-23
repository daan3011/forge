package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daan3011/forge/internal/templates"
	"github.com/daan3011/forge/internal/ui"
)

func TestSyncTemplatesCommand(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mine.md"), []byte("Do it.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	msg := syncTemplates(dir)()
	if s, ok := msg.(ui.SuccessMsg); !ok || !strings.Contains(s.Text, "1 template") {
		t.Fatalf("first: %#v", msg)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "mine.md"))
	if !strings.Contains(string(b), templates.ToolsMarker) {
		t.Errorf("not synced: %s", b)
	}
	if _, ok := syncTemplates(dir)().(ui.InfoMsg); !ok {
		t.Error("second run should report nothing to do")
	}
}
