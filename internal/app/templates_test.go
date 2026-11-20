package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/ui"
)

func TestEditTemplatesCommand(t *testing.T) {
	if hasItem(mustNew(t, config.Default()), "templates_edit") {
		t.Error("Edit templates offered without a templates folder")
	}

	dir := filepath.Join(t.TempDir(), "templates")
	m := mustNew(t, config.Default(), WithTemplates(dir, nil))
	var found *item
	for _, it := range m.paletteItems() {
		if it.id == "templates_edit" {
			found = &it
		}
	}
	if found == nil || found.title != "Edit templates" {
		t.Fatalf("no Edit templates command: %+v", found)
	}
	t.Setenv("VISUAL", "true")
	_, cmd := found.run(m)
	msg := cmd() // creates the folder, then hands the editor to the program
	if e, ok := msg.(ui.ErrMsg); ok {
		t.Fatal(e.Err)
	}
	if msg == nil {
		t.Fatal("no exec message")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 6 {
		t.Fatalf("templates folder: %d entries, %v", len(entries), err)
	}
}

func hasItem(m Model, id string) bool {
	for _, it := range m.paletteItems() {
		if it.id == id {
			return true
		}
	}
	return false
}
