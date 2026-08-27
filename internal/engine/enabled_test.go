package engine

import (
	"strings"
	"testing"
)

func boolPtr(b bool) *bool { return &b }

func TestDisabledServerIsNotWritten(t *testing.T) {
	on, off := true, false
	out, err := RenderConfig("https://x", []MCPServer{
		{Name: "kept", URL: "https://a/mcp"},
		{Name: "explicit_on", URL: "https://b/mcp", Enabled: &on},
		{Name: "turned_off", URL: "https://c/mcp", Enabled: &off, OAuthClientID: "keep-me"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[mcp_servers.kept]", "[mcp_servers.explicit_on]"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(out, "turned_off") {
		t.Error("a disabled server reached config.toml")
	}
	if strings.Contains(out, "keep-me") {
		t.Error("a disabled server's client id reached config.toml")
	}
}

func TestDisabledServerStillValidated(t *testing.T) {
	off := false
	if _, err := RenderConfig("https://x", []MCPServer{
		{Name: "bad name!", URL: "https://a/mcp", Enabled: &off},
	}); err == nil {
		t.Fatal("a malformed disabled server should still be refused")
	}
}
