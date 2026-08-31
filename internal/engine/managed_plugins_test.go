package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func googleish(name string) MCPServer {
	return MCPServer{
		Name: name, URL: "https://gmailmcp.googleapis.com/mcp/v1",
		OAuthClientID: "id-123.apps.googleusercontent.com", OAuthClientSecret: "GOCSPX-abc",
		Scopes: []string{"https://mail.google.com/"},
	}
}

func TestSecretServersRenderAsPluginsNotTables(t *testing.T) {
	out, err := RenderConfig("https://x", []MCPServer{googleish("gmail"), {Name: "plain", URL: "https://a/mcp"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "[mcp_servers.gmail]") {
		t.Error("secret-bearing server leaked into an mcp_servers table")
	}
	if strings.Contains(out, "GOCSPX-abc") {
		t.Error("the secret must never appear in config.toml")
	}
	for _, want := range []string{`[marketplaces.unbiased-managed]`, `[plugins."gmail@unbiased-managed"]`, "[mcp_servers.plain]"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s", want)
		}
	}
	// plugin lines precede server tables so they cannot land inside one
	if strings.Index(out, "unbiased-managed") > strings.Index(out, "[mcp_servers.plain]") {
		t.Error("plugin lines must come before mcp_servers tables")
	}
}

func TestWriteManagedPluginsFiles(t *testing.T) {
	dir := t.TempDir()
	if err := WriteManagedPlugins(dir, []MCPServer{googleish("gmail")}); err != nil {
		t.Fatal(err)
	}
	var mcp struct {
		McpServers map[string]struct {
			Type   string   `json:"type"`
			URL    string   `json:"url"`
			Scopes []string `json:"scopes"`
			OAuth  struct {
				ClientID     string `json:"client_id"`
				ClientSecret string `json:"client_secret"`
				CallbackPort int    `json:"callback_port"`
			} `json:"oauth"`
		} `json:"mcpServers"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "managed-plugins", "plugins", "gmail", ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &mcp); err != nil {
		t.Fatal(err)
	}
	g := mcp.McpServers["gmail"]
	if g.OAuth.ClientSecret != "" {
		t.Error("the secret must NOT be in the plugin file — codex loses it; the app proxy injects it")
	}
	if g.OAuth.CallbackPort != 45999 || len(g.Scopes) != 1 || g.OAuth.ClientID == "" {
		t.Errorf("bad plugin mcp.json: %+v", g)
	}
	if g.URL != "http://127.0.0.1:45991/mcp/v1" {
		t.Errorf("plugin must dial the app's gmail proxy port, got %s", g.URL)
	}
	if _, err := os.Stat(filepath.Join(dir, "managed-plugins", ".agents", "plugins", "marketplace.json")); err != nil {
		t.Error("marketplace index missing")
	}
	// A stale cache is what made the proxy invisible to codex for an entire
	// debugging session: it copies the plugin on first load and reads the copy
	// after, so rewriting the source alone changes nothing.
	stale := filepath.Join(dir, "plugins", "cache", "unbiased-managed", "gmail", "0.0.1")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteManagedPlugins(dir, []MCPServer{googleish("gmail")}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugins", "cache", "unbiased-managed")); !os.IsNotExist(err) {
		t.Error("codex's cache of our marketplace must be cleared on every write")
	}
	// and the version tracks content, so a surviving cache entry cannot match
	var man struct {
		Version string `json:"version"`
	}
	raw2, err := os.ReadFile(filepath.Join(dir, "managed-plugins", "plugins", "gmail", ".codex-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw2, &man); err != nil {
		t.Fatal(err)
	}
	changed := googleish("gmail")
	changed.Scopes = []string{"https://mail.google.com/", "https://www.googleapis.com/auth/gmail.modify"}
	if err := WriteManagedPlugins(dir, []MCPServer{changed}); err != nil {
		t.Fatal(err)
	}
	raw3, _ := os.ReadFile(filepath.Join(dir, "managed-plugins", "plugins", "gmail", ".codex-plugin", "plugin.json"))
	var man2 struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(raw3, &man2)
	if man.Version == man2.Version {
		t.Errorf("plugin version must change when the config does (%s)", man.Version)
	}
	// removal wipes the tree — a deleted connector's secret must not linger
	if err := WriteManagedPlugins(dir, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "managed-plugins")); !os.IsNotExist(err) {
		t.Error("managed-plugins tree should be removed when no secret servers remain")
	}
}

func TestSecretRequiresClientId(t *testing.T) {
	if _, err := RenderConfig("https://x", []MCPServer{{Name: "g", URL: "https://a", OAuthClientSecret: "s"}}); err == nil {
		t.Fatal("secret without client id must be refused")
	}
}
