package app

import (
	"slices"
	"testing"

	"github.com/charmbracelet/x/exp/golden"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/ui"
)

// Regenerate goldens with: go test ./internal/app -run TestKeyInventory -update

func TestKeyInventoryMarkdown(t *testing.T) {
	entries, err := KeyInventory(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	golden.RequireEqual(t, RenderKeysMarkdown(entries))
}

func TestKeyInventoryJSON(t *testing.T) {
	entries, err := KeyInventory(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	out, err := RenderKeysJSON(entries)
	if err != nil {
		t.Fatal(err)
	}
	golden.RequireEqual(t, out)
}

// TestKeyInventoryCoversKeymap: every configurable action is in the dump,
// once, with its keys.
func TestKeyInventoryCoversKeymap(t *testing.T) {
	entries, err := KeyInventory(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, e := range entries {
		if e.Kind != KindPalette {
			seen[e.ID]++
		}
	}
	for _, id := range ui.ActionIDs() {
		if seen[id] != 1 {
			t.Errorf("action %q appears %d times in the inventory", id, seen[id])
		}
	}
	for _, id := range []string{"tmux_detach_client", "form_confirm_accept", "doctor", "tmux_restart"} {
		if !slices.ContainsFunc(entries, func(e KeyEntry) bool { return e.ID == id }) {
			t.Errorf("inventory lacks %q", id)
		}
	}
}

func TestKeyInventoryCustomCommands(t *testing.T) {
	cfg := config.Default()
	cfg.CustomCommands = []config.CustomCommand{{Name: "lint", Command: "make lint", Key: "L", Tabs: []string{"prs"}}}
	entries, err := KeyInventory(cfg)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(entries, func(e KeyEntry) bool { return e.ID == "custom:lint" })
	if i < 0 {
		t.Fatal("custom command missing")
	}
	if e := entries[i]; e.Kind != ui.KindTab || e.Scope != "prs" || !slices.Equal(e.Keys, []string{"L"}) {
		t.Fatalf("got %+v", e)
	}
}
