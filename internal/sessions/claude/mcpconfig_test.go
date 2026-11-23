package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteMCPConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions", "fix ci", MCPConfigFile) // folder created
	if err := WriteMCPConfig(path, "/opt/my tools/forge", "fix-ci"); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(first, &got); err != nil {
		t.Fatalf("%v\n%s", err, first)
	}
	s, ok := got.MCPServers["forge"]
	if !ok || len(got.MCPServers) != 1 || s.Type != "stdio" || s.Command != "/opt/my tools/forge" ||
		len(s.Args) != 1 || s.Args[0] != "mcp" || s.Env["FORGE_SESSION"] != "fix-ci" {
		t.Errorf("config %s", first)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
	// Idempotent; a moved binary is picked up.
	if err := WriteMCPConfig(path, "/opt/my tools/forge", "fix-ci"); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(path); string(again) != string(first) {
		t.Error("second write changed the file")
	}
	if err := WriteMCPConfig(path, "/new/forge", "fix-ci"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !json.Valid(b) || string(b) == string(first) {
		t.Errorf("binary not updated: %s", b)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("temp files left: %v", entries)
	}
	if err := WriteMCPConfig(path, "", "x"); err == nil {
		t.Error("no binary: want an error")
	}
}

func TestReadMCPConfigBin(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, MCPConfigFile)
	if err := WriteMCPConfig(p, "/usr/local/bin/forge", "s1"); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadMCPConfigBin(p); err != nil || got != "/usr/local/bin/forge" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := ReadMCPConfigBin(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("missing file: want an error")
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMCPConfigBin(bad); err == nil {
		t.Error("bad json: want an error")
	}
	other := filepath.Join(dir, "other.json")
	if err := os.WriteFile(other, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadMCPConfigBin(other); err != nil || got != "" {
		t.Errorf("no forge server: got %q, %v", got, err)
	}
}
