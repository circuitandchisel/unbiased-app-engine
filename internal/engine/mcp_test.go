package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readConfig(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	return string(data), err
}

// hasTOMLLine reports whether any ACTIVE line of cfg starts with prefix.
//
// Substring matching is unusable on this file: the template documents
// `[mcp_servers.*]` in a comment, and `base_url = ` ends in the same six
// characters as `url = `. Both produced false positives in the first cut of
// these tests. Anchoring to a line, and skipping comments, is what the
// assertions actually mean.
func hasTOMLLine(cfg, prefix string) bool {
	for _, line := range strings.Split(cfg, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// stagingLeftovers reports the ".config.toml.*" temp files MaterializeHome
// stages into. A failed render must not leave one behind.
func stagingLeftovers(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".config.toml.") {
			out = append(out, e.Name())
		}
	}
	return out
}

// An empty set must leave no trace. This is the guarantee that lets the
// feature ship dark: build it, ship it, and until BuiltinMCPServers returns
// something the engine is configured exactly as before.
func TestRenderConfigNoServersWritesNoTable(t *testing.T) {
	cfg, err := RenderConfig(DefaultBaseURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if hasTOMLLine(cfg, "[mcp_servers") {
		t.Error("empty server set must not write an [mcp_servers] table")
	}
	if strings.Contains(cfg, "{{") {
		t.Error("unexpanded template placeholder left in config")
	}
}

func TestRenderConfigStdioServer(t *testing.T) {
	cfg, err := RenderConfig(DefaultBaseURL, []MCPServer{{
		Name:              "node_repl",
		Command:           "npx",
		Args:              []string{"-y", "@modelcontextprotocol/server-node-repl"},
		Env:               map[string]string{"NODE_ENV": "production", "LOG_LEVEL": "warn"},
		StartupTimeoutSec: 15,
		ToolTimeoutSec:    120,
		EnabledTools:      []string{"run", "reset"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`[mcp_servers.node_repl]`,
		`command = "npx"`,
		`args = ["-y", "@modelcontextprotocol/server-node-repl"]`,
		// Sorted, not map order: a config that differs between two starts of
		// the same build is a config nobody can diff.
		`env = { LOG_LEVEL = "warn", NODE_ENV = "production" }`,
		`startup_timeout_sec = 15`,
		`tool_timeout_sec = 120`,
		`enabled_tools = ["run", "reset"]`,
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config missing %q", want)
		}
	}
	// A stdio server has no HTTP keys.
	if hasTOMLLine(cfg, "url =") || hasTOMLLine(cfg, "bearer_token_env_var") {
		t.Error("stdio server must not emit http keys")
	}
	// And the provider lock still holds with servers present.
	if !strings.Contains(cfg, `model = "pareto"`) {
		t.Error("adding an mcp server must not disturb the provider lock")
	}
}

func TestRenderConfigHTTPServer(t *testing.T) {
	cfg, err := RenderConfig(DefaultBaseURL, []MCPServer{{
		Name:              "docs",
		URL:               "https://mcp.example.com/sse",
		BearerTokenEnvVar: "DOCS_MCP_TOKEN",
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`[mcp_servers.docs]`,
		`url = "https://mcp.example.com/sse"`,
		`bearer_token_env_var = "DOCS_MCP_TOKEN"`,
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config missing %q", want)
		}
	}
	// The credential's NAME is config; its value never is.
	if hasTOMLLine(cfg, "command =") {
		t.Error("http server must not emit a command")
	}
}

// Deterministic output, asserted directly rather than inferred from the env
// case above: Go randomises map iteration, so this would fail intermittently
// if the sort were dropped.
func TestRenderConfigIsDeterministic(t *testing.T) {
	server := MCPServer{
		Name:    "many_env",
		Command: "server",
		Env: map[string]string{
			"A": "1", "B": "2", "C": "3", "D": "4", "E": "5",
			"F": "6", "G": "7", "H": "8", "I": "9", "J": "10",
		},
	}
	first, err := RenderConfig(DefaultBaseURL, []MCPServer{server})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, err := RenderConfig(DefaultBaseURL, []MCPServer{server})
		if err != nil {
			t.Fatal(err)
		}
		if again != first {
			t.Fatal("render is not deterministic across calls")
		}
	}
}

func TestMCPServerRejections(t *testing.T) {
	cases := []struct {
		name   string
		server MCPServer
		want   string // substring the error must contain
	}{{
		name:   "no name",
		server: MCPServer{Command: "x"},
		want:   "no name",
	}, {
		name:   "name with a dot",
		server: MCPServer{Name: "node.repl", Command: "x"},
		want:   "letters, digits, underscores",
	}, {
		// The reason maxMCPNameLen exists: the flattened tool name has to fit
		// the gateway's 64-character cap.
		name:   "name too long for the tool-name budget",
		server: MCPServer{Name: strings.Repeat("a", maxMCPNameLen+1), Command: "x"},
		want:   "64-character cap",
	}, {
		name:   "no transport",
		server: MCPServer{Name: "bare"},
		want:   "needs a Command",
	}, {
		name:   "both transports",
		server: MCPServer{Name: "both", Command: "x", URL: "https://e.com"},
		want:   "not both",
	}, {
		name:   "plaintext url off this machine",
		server: MCPServer{Name: "plain", URL: "http://e.com"},
		want:   "allowed only on localhost",
	}, {
		name:   "plaintext url on a private address is still a network hop",
		server: MCPServer{Name: "lan", URL: "http://192.168.1.50:3845/mcp"},
		want:   "allowed only on localhost",
	}, {
		name:   "unsupported scheme",
		server: MCPServer{Name: "weird", URL: "ftp://e.com"},
		want:   "scheme must be https",
	}, {
		name:   "not a url",
		server: MCPServer{Name: "nope", URL: "https://"},
		want:   "not a valid absolute URL",
	}, {
		name:   "args on an http server",
		server: MCPServer{Name: "http_args", URL: "https://e.com", Args: []string{"-x"}},
		want:   "stdio servers only",
	}, {
		name:   "bearer token on a stdio server",
		server: MCPServer{Name: "stdio_bearer", Command: "x", BearerTokenEnvVar: "TOK"},
		want:   "http servers only",
	}, {
		name:   "bad env key",
		server: MCPServer{Name: "badenv", Command: "x", Env: map[string]string{"not-a-var": "v"}},
		want:   "not a valid environment variable name",
	}, {
		// The supervisor's own contract. A server that could set these could
		// repoint the engine's home or its credential.
		name:   "env overriding the credential",
		server: MCPServer{Name: "sneaky", Command: "x", Env: map[string]string{"UNBIASED_API_KEY": "sk_theirs"}},
		want:   "reserved by the supervisor",
	}, {
		name:   "env overriding CODEX_HOME",
		server: MCPServer{Name: "sneaky2", Command: "x", Env: map[string]string{"CODEX_HOME": "/tmp/theirs"}},
		want:   "reserved by the supervisor",
	}, {
		name:   "negative timeout",
		server: MCPServer{Name: "neg", Command: "x", StartupTimeoutSec: -1},
		want:   "cannot be negative",
	}, {
		name:   "enabled tool with a slash",
		server: MCPServer{Name: "tools", Command: "x", EnabledTools: []string{"a/b"}},
		want:   "letters, digits, underscores",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RenderConfig(DefaultBaseURL, []MCPServer{tc.server})
			if err == nil {
				t.Fatalf("expected a rejection, got a rendered config")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q must mention %q", err, tc.want)
			}
		})
	}
}

// TOML injection. Everything below would, unescaped, either break out of its
// quoted string or open a table of the attacker's choosing — which for this
// package means rewriting the provider block. Rejecting beats escaping: no
// legitimate command or URL contains any of these.
func TestMCPServerRejectsTOMLInjection(t *testing.T) {
	payloads := []string{
		`x" \n model = "gpt-5.5`,
		`x"`,
		`x\`,
		"x\nmodel = \"gpt-5.5\"",
		"x\ty",
		"x\x00y",
	}
	for _, p := range payloads {
		t.Run(strings.Map(func(r rune) rune {
			if r < 0x20 {
				return '_'
			}
			return r
		}, p), func(t *testing.T) {
			if _, err := RenderConfig(DefaultBaseURL, []MCPServer{{Name: "inj", Command: p}}); err == nil {
				t.Fatal("payload rendered instead of being rejected")
			}
			if _, err := RenderConfig(DefaultBaseURL, []MCPServer{{Name: "inj", URL: "https://e.com", BearerTokenEnvVar: p}}); err == nil {
				t.Fatal("payload rendered in bearer_token_env_var instead of being rejected")
			}
			if _, err := RenderConfig(DefaultBaseURL, []MCPServer{{Name: "inj", Command: "ok", Args: []string{p}}}); err == nil {
				t.Fatal("payload rendered in args instead of being rejected")
			}
			if _, err := RenderConfig(DefaultBaseURL, []MCPServer{{Name: "inj", Command: "ok", Env: map[string]string{"K": p}}}); err == nil {
				t.Fatal("payload rendered in env instead of being rejected")
			}
		})
	}
}

// TOML lets a duplicate table through and the last one wins, so codex would
// run something other than what the list says. Case-insensitive because the
// collision that matters is downstream, in flattened tool names.
func TestMCPServerRejectsDuplicateNames(t *testing.T) {
	for _, pair := range [][2]string{{"docs", "docs"}, {"docs", "DOCS"}} {
		_, err := RenderConfig(DefaultBaseURL, []MCPServer{
			{Name: pair[0], Command: "a"},
			{Name: pair[1], Command: "b"},
		})
		if err == nil {
			t.Fatalf("%v: duplicate names must be rejected", pair)
		}
		if !strings.Contains(err.Error(), "duplicate name") {
			t.Fatalf("%v: error must name the problem, got: %v", pair, err)
		}
	}
}

// An invalid server must not leave a partly-updated home. The previous
// config.toml is the last good one and has to stay that way.
func TestMaterializeHomeLeavesGoodConfigOnBadServer(t *testing.T) {
	dir := t.TempDir()
	if err := MaterializeHome(dir, DefaultBaseURL, nil); err != nil {
		t.Fatal(err)
	}
	before, err := readConfig(dir)
	if err != nil {
		t.Fatal(err)
	}

	err = MaterializeHome(dir, DefaultBaseURL, []MCPServer{{Name: "bad name", Command: "x"}})
	if err == nil {
		t.Fatal("expected MaterializeHome to reject an invalid server")
	}

	after, err := readConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatal("a rejected server must leave the existing config untouched")
	}
	// And no staging file left behind.
	if entries := stagingLeftovers(t, dir); len(entries) > 0 {
		t.Fatalf("staging files left behind: %v", entries)
	}
}

// The budget split is documented in two constants that have to agree.
func TestToolNameBudgetArithmetic(t *testing.T) {
	// mcp__ + name + __ + tool  must fit the gateway's 64-char cap.
	const gatewayCap = 64
	worst := len("mcp__") + maxMCPNameLen + len("__") + maxMCPToolNameLen
	if worst != gatewayCap {
		t.Fatalf("budget does not add up: mcp__(5) + name(%d) + __(2) + tool(%d) = %d, cap is %d",
			maxMCPNameLen, maxMCPToolNameLen, worst, gatewayCap)
	}
}

func TestLoadUserMCPServers(t *testing.T) {
	dir := t.TempDir()

	// Missing is the normal case, not an error: most users add no servers.
	got, err := LoadUserMCPServers(filepath.Join(dir, "absent.json"))
	if err != nil || got != nil {
		t.Fatalf("missing file: got %v, %v — want nil, nil", got, err)
	}

	good := filepath.Join(dir, "good.json")
	os.WriteFile(good, []byte(`{"servers":[
	  {"name":"docs","url":"https://mcp.example.com/sse","bearerTokenEnvVar":"TOK"},
	  {"name":"repl","command":"node","args":["s.mjs"],"env":{"A":"1"},"toolTimeoutSec":30}
	]}`), 0o600)
	servers, err := LoadUserMCPServers(good)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 || servers[0].Name != "docs" || servers[1].Command != "node" {
		t.Fatalf("unexpected parse: %+v", servers)
	}
	// It must render, since rendering is the whole point of loading it.
	cfg, err := RenderConfig(DefaultBaseURL, servers)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg, "[mcp_servers.docs]") || !strings.Contains(cfg, "[mcp_servers.repl]") {
		t.Error("loaded servers did not reach the config")
	}

	// A hand-edited file that is present but wrong must fail loudly, naming
	// the file — starting anyway would silently drop servers the user
	// believes are configured.
	for name, body := range map[string]string{
		"malformed.json": `{"servers":[`,
		"badserver.json": `{"servers":[{"name":"no transport"}]}`,
		"injection.json": `{"servers":[{"name":"x","command":"a\" \nmodel = \"gpt-5.5"}]}`,
	} {
		path := filepath.Join(dir, name)
		os.WriteFile(path, []byte(body), 0o600)
		_, err := LoadUserMCPServers(path)
		if err == nil {
			t.Errorf("%s: expected an error", name)
			continue
		}
		if !strings.Contains(err.Error(), path) {
			t.Errorf("%s: error must name the file, got: %v", name, err)
		}
	}
}

// A user server must not be able to shadow a compiled-in one by reusing its
// name — the merge validates the combined set, not each half.
func TestMergeMCPServersRejectsShadowing(t *testing.T) {
	builtin := []MCPServer{{Name: "official", Command: "a"}}
	user := []MCPServer{{Name: "official", Command: "evil"}}
	if _, err := MergeMCPServers(builtin, user); err == nil {
		t.Fatal("a user server reusing a builtin name must be rejected")
	}
	merged, err := MergeMCPServers(builtin, []MCPServer{{Name: "mine", Command: "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 2 {
		t.Fatalf("merged = %d servers, want 2", len(merged))
	}
}

// Locally-run MCP servers are the common case — Figma's Dev Mode server is
// http://127.0.0.1:3845/mcp — and loopback plaintext never leaves the machine,
// so the https rule that protects a network hop must not block them.
func TestMCPServerAcceptsLoopbackHTTP(t *testing.T) {
	for _, u := range []string{
		"http://127.0.0.1:3845/mcp",
		"http://localhost:3845/mcp",
		"http://[::1]:3845/mcp",
		"http://127.0.0.2:9000/mcp", // the whole 127/8 block, not just .1
		"https://mcp.example.com/sse",
	} {
		cfg, err := RenderConfig(DefaultBaseURL, []MCPServer{{Name: "s", URL: u}})
		if err != nil {
			t.Errorf("%s must be accepted: %v", u, err)
			continue
		}
		if !strings.Contains(cfg, `url = "`+u+`"`) {
			t.Errorf("%s did not reach the config", u)
		}
	}
}

// Host names are case-insensitive, and the app's URL() lowercases them before
// validating. If this side did not, the app would save a config the engine
// then refuses to boot with.
func TestLoopbackHostIsCaseInsensitive(t *testing.T) {
	for _, u := range []string{
		"http://LocalHost:3845/mcp",
		"http://LOCALHOST:3845/mcp",
	} {
		if _, err := RenderConfig(DefaultBaseURL, []MCPServer{{Name: "s", URL: u}}); err != nil {
			t.Errorf("%s must be accepted: %v", u, err)
		}
	}
	// Still not a licence for a real hop.
	if _, err := RenderConfig(DefaultBaseURL, []MCPServer{{Name: "s", URL: "http://LocalHost.evil.com/mcp"}}); err == nil {
		t.Error("a hostname merely starting with localhost must be refused")
	}
}
