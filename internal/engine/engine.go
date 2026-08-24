// Package engine materializes a private, Pareto-locked CODEX_HOME and execs
// the pinned codex-app-server binary inside it.
//
// The design invariant: the engine's entire view of the world is a directory
// this package wrote moments ago. It never reads ~/.codex, never inherits a
// user's provider config, and receives exactly one credential — the Unbiased
// API key — via the environment. Clients that spawn us get a stock app-server
// speaking the documented JSON-RPC protocol on stdio, already aimed at Pareto.
package engine

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed config.toml.tmpl
var configTemplate string

// DefaultBaseURL is the production Unbiased gateway.
const DefaultBaseURL = "https://api.unbiased.ai/v1"

// ResolveKey returns the Unbiased API key: the environment wins, then the
// credentials file written by `unbiased login`. The two-level order matches
// unbiased-cli so the same shell behaves identically under both tools.
func ResolveKey(getenv func(string) string, credentialsPath string) (string, error) {
	if k := strings.TrimSpace(getenv("UNBIASED_API_KEY")); k != "" {
		return k, nil
	}
	data, err := os.ReadFile(credentialsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("no API key: UNBIASED_API_KEY is not set and %s does not exist — run `unbiased login` first", credentialsPath)
		}
		return "", fmt.Errorf("reading credentials: %w", err)
	}
	var creds struct {
		APIKey string `json:"apiKey"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return "", fmt.Errorf("parsing %s: %w", credentialsPath, err)
	}
	if strings.TrimSpace(creds.APIKey) == "" {
		return "", fmt.Errorf("no API key in %s — run `unbiased login` again", credentialsPath)
	}
	return creds.APIKey, nil
}

// DefaultCredentialsPath is where `unbiased login` stores the key.
func DefaultCredentialsPath(home string) string {
	return filepath.Join(home, ".unbiased", "credentials.json")
}

// DefaultHomeDir is the engine home the supervisor owns outright.
func DefaultHomeDir(home string) string {
	return filepath.Join(home, ".unbiased", "app-engine", "home")
}

// RenderConfig produces the config.toml contents for the given gateway URL
// and MCP server set. Split from MaterializeHome so tests can assert on
// content without a filesystem.
//
// The error is for an unusable MCP server — a bad name, no transport, a value
// that cannot be quoted safely. Failing here is the point: the alternatives
// are a config the engine rejects at boot, or one it accepts whose tool names
// the gateway then refuses mid-conversation, two repos away from the cause.
func RenderConfig(baseURL string, servers []MCPServer) (string, error) {
	if err := validateMCPServers(servers); err != nil {
		return "", err
	}
	out := strings.ReplaceAll(configTemplate, "{{BASE_URL}}", baseURL)
	return strings.ReplaceAll(out, "{{MCP_SERVERS}}", renderMCPServers(servers)), nil
}

// MaterializeHome (re)creates the engine home directory and rewrites its
// config.toml from the embedded template. Rewriting every start is the point:
// whatever an engine or a previous run left behind, the provider config is
// ours again before the engine boots.
func MaterializeHome(dir, baseURL string, servers []MCPServer) error {
	// Render before touching the filesystem: an invalid MCP server must not
	// leave a half-updated home behind, and the existing config.toml is still
	// the last good one until the rename below.
	contents, err := RenderConfig(baseURL, servers)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating engine home: %w", err)
	}
	// Write-then-rename in the same directory so a crash mid-write can never
	// leave the engine reading a truncated config.
	tmp, err := os.CreateTemp(dir, ".config.toml.")
	if err != nil {
		return fmt.Errorf("staging config: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.WriteString(contents); err != nil {
		tmp.Close()
		return fmt.Errorf("writing config: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("setting config mode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing config: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, "config.toml")); err != nil {
		return fmt.Errorf("installing config: %w", err)
	}
	return nil
}

// Env returns the child environment for the engine: the parent environment
// with CODEX_HOME and UNBIASED_API_KEY forced to our values. Forcing (not
// appending) matters — exec uses the last duplicate, but there is no reason
// to hand the engine two values and hope.
func Env(parent []string, homeDir, key string) []string {
	out := make([]string, 0, len(parent)+2)
	for _, kv := range parent {
		if strings.HasPrefix(kv, "CODEX_HOME=") || strings.HasPrefix(kv, "UNBIASED_API_KEY=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "CODEX_HOME="+homeDir, "UNBIASED_API_KEY="+key)
}
