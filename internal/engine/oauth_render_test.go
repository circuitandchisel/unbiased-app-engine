package engine

import "testing"
import "strings"

func TestOAuthClientRendersAfterScalars(t *testing.T) {
	out, err := RenderConfig("https://api.unbiased.ai/v1", []MCPServer{{
		Name: "HoneyComb", URL: "https://mcp.honeycomb.io/mcp",
		OAuthClientID: "our-client-id", Scopes: []string{"read"},
		StartupTimeoutSec: 30, ToolTimeoutSec: 60,
	}})
	if err != nil {
		t.Fatal(err)
	}
	sub := strings.Index(out, "[mcp_servers.HoneyComb.oauth]")
	if sub < 0 {
		t.Fatal("oauth sub-table missing")
	}
	// Every scalar for this server must appear BEFORE the sub-table header,
	// or TOML puts it inside the sub-table.
	for _, key := range []string{"url =", "scopes =", "startup_timeout_sec =", "tool_timeout_sec ="} {
		if i := strings.Index(out, key); i < 0 || i > sub {
			t.Errorf("%q at %d must come before the oauth sub-table at %d", key, i, sub)
		}
	}
	if !strings.Contains(out, `client_id = "our-client-id"`) {
		t.Error("client_id not rendered")
	}
}

func TestOAuthRejectedOnStdio(t *testing.T) {
	_, err := RenderConfig("https://x", []MCPServer{{Name: "s", Command: "run", OAuthClientID: "x"}})
	if err == nil {
		t.Fatal("expected stdio+oauth to be refused")
	}
}

func TestOAuthValueQuotingRefused(t *testing.T) {
	_, err := RenderConfig("https://x", []MCPServer{{Name: "s", URL: "https://y", OAuthClientID: `a"b`}})
	if err == nil {
		t.Fatal("expected a quote in the client id to be refused")
	}
}
