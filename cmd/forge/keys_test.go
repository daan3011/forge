package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/daan3011/forge/internal/config"
)

func TestKeysDump(t *testing.T) {
	cfg := config.Default()
	cfg.Keybinds = map[string][]string{"help": {"f1"}}
	d := deps{loadConfig: func() (*config.Config, error) { return cfg, nil }}

	out, _, err := run(t, d, "keys", "--dump", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var entries []struct {
		ID   string   `json:"id"`
		Keys []string `json:"keys"`
	}
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatal(err)
	}
	var help []string
	for _, e := range entries {
		if e.ID == "help" {
			help = e.Keys
		}
	}
	if len(help) != 1 || help[0] != "f1" {
		t.Fatalf("help keys = %v, want the config override [f1]", help)
	}

	out, _, err = run(t, d, "keys", "--dump", "--defaults")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "| `?` | `help` |") {
		t.Fatal("--defaults should ignore the override")
	}
}

func TestKeysErrors(t *testing.T) {
	d := deps{loadConfig: func() (*config.Config, error) { return config.Default(), nil }}
	if _, _, err := run(t, d, "keys"); err == nil {
		t.Fatal("want an error without --dump")
	}
	if _, _, err := run(t, d, "keys", "--dump", "--format", "yaml"); err == nil {
		t.Fatal("want an error for an unknown format")
	}
	help, _, _ := run(t, d, "--help")
	if strings.Contains(help, "keys") {
		t.Fatal("keys should be hidden from --help")
	}
}
