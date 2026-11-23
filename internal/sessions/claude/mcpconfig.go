package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// MCPServerName is forge's server name in the config; its tools reach the
// agent as mcp__forge__*.
const MCPServerName = "forge"

// MCPConfigFile is the file name of a session's MCP config, in the session
// dir.
const MCPConfigFile = "mcp.json"

// mcpConfig is the --mcp-config file format, the same shape as .mcp.json.
type mcpConfig struct {
	MCPServers map[string]mcpServer `json:"mcpServers"`
}

type mcpServer struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

// MCPConfig returns the --mcp-config JSON that runs `<forgeBin> mcp` for
// session. The session name is written in literally (not ${FORGE_SESSION}),
// so it doesn't rely on env expansion; the server reads it from its
// environment.
func MCPConfig(forgeBin, session string) ([]byte, error) {
	if forgeBin == "" || session == "" {
		return nil, errors.New("claude: mcp config needs the forge binary and the session name")
	}
	b, err := json.MarshalIndent(mcpConfig{MCPServers: map[string]mcpServer{
		MCPServerName: {
			Type: "stdio", Command: forgeBin, Args: []string{"mcp"},
			Env: map[string]string{"FORGE_SESSION": session},
		},
	}}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// WriteMCPConfig writes session's MCP config to path (atomically, 0600),
// creating its folder. It is rewritten on every start and resume, so a
// moved forge binary is picked up; the same input gives the same bytes.
func WriteMCPConfig(path, forgeBin, session string) error {
	data, err := MCPConfig(forgeBin, session)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("claude: mcp config: %w", err)
	}
	f, err := os.CreateTemp(dir, ".mcp-*.json")
	if err != nil {
		return fmt.Errorf("claude: mcp config: %w", err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // no-op after the rename
	_, werr := f.Write(data)
	if err := errors.Join(werr, f.Close(), os.Chmod(tmp, 0o600)); err != nil {
		return fmt.Errorf("claude: mcp config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("claude: mcp config: %w", err)
	}
	return nil
}
