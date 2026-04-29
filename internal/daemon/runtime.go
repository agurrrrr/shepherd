package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agurrrrr/shepherd/internal/config"
)

// RuntimeInfo describes how local processes (the MCP child, CLI tools) talk
// to the running shepherd daemon — over a localhost HTTP loopback with a
// shared secret. Written when the daemon starts, removed on shutdown.
//
// Why: stateless MCP children cannot hold onto chrome sessions across calls.
// The forwarder reads this file to find the long-running daemon.
type RuntimeInfo struct {
	Addr     string `json:"addr"`      // http://127.0.0.1:8585
	MCPToken string `json:"mcp_token"` // 32-byte hex secret, regenerated each start
	PID      int    `json:"pid"`
}

func runtimeFilePath() string {
	return filepath.Join(config.GetConfigDir(), "runtime.json")
}

// WriteRuntime generates a fresh token and writes the runtime info file with
// mode 0600 so other users on the host cannot read the secret.
func WriteRuntime(addr string) (*RuntimeInfo, error) {
	tok := make([]byte, 32)
	if _, err := rand.Read(tok); err != nil {
		return nil, fmt.Errorf("generate mcp token: %w", err)
	}
	info := &RuntimeInfo{
		Addr:     addr,
		MCPToken: hex.EncodeToString(tok),
		PID:      os.Getpid(),
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(runtimeFilePath(), data, 0600); err != nil {
		return nil, err
	}
	return info, nil
}

// ReadRuntime returns the runtime info from disk, or an error if the daemon
// is not running.
func ReadRuntime() (*RuntimeInfo, error) {
	data, err := os.ReadFile(runtimeFilePath())
	if err != nil {
		return nil, err
	}
	var info RuntimeInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("invalid runtime.json: %w", err)
	}
	return &info, nil
}

// RemoveRuntime removes the runtime info file (called on daemon shutdown).
func RemoveRuntime() error {
	err := os.Remove(runtimeFilePath())
	if err != nil && os.IsNotExist(err) {
		return nil
	}
	return err
}
