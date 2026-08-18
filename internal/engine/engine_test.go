package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveKeyEnvWins(t *testing.T) {
	getenv := func(k string) string {
		if k == "UNBIASED_API_KEY" {
			return "sk_env"
		}
		return ""
	}
	key, err := ResolveKey(getenv, "/nonexistent/credentials.json")
	if err != nil {
		t.Fatal(err)
	}
	if key != "sk_env" {
		t.Fatalf("key = %q, want env key", key)
	}
}

func TestResolveKeyFallsBackToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(path, []byte(`{"apiKey":"sk_stored"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := ResolveKey(func(string) string { return "" }, path)
	if err != nil {
		t.Fatal(err)
	}
	if key != "sk_stored" {
		t.Fatalf("key = %q, want stored key", key)
	}
}

func TestResolveKeyMissingEverything(t *testing.T) {
	_, err := ResolveKey(func(string) string { return "" }, filepath.Join(t.TempDir(), "nope.json"))
	if err == nil || !strings.Contains(err.Error(), "unbiased login") {
		t.Fatalf("error must point at `unbiased login`, got: %v", err)
	}
}

func TestRenderConfigLocksProvider(t *testing.T) {
	cfg := RenderConfig("https://gw.example/v1")
	for _, want := range []string{
		`model = "pareto"`,
		`model_provider = "unbiased"`,
		`base_url = "https://gw.example/v1"`,
		`env_key = "UNBIASED_API_KEY"`,
		`wire_api = "responses"`,
		`multi_agent = false`,
		// Multi-agent v2: namespace tools ride through the gateway
		// (gpu-router#414). Sub-agents inherit the pinned Pareto provider.
		`[features.multi_agent_v2]`,
		`enabled = true`,
		`max_concurrent_threads_per_session = 5`,
		`expose_spawn_agent_model_overrides = false`,
		`hide_spawn_agent_metadata = false`,
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config missing %q", want)
		}
	}
	if strings.Contains(cfg, "{{") {
		t.Error("unexpanded template placeholder left in config")
	}
}

func TestMaterializeHomeRewrites(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "home")
	if err := MaterializeHome(dir, DefaultBaseURL); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")

	// Simulate an engine (or anything else) rewriting the config underneath
	// us: the next start must win it back.
	if err := os.WriteFile(path, []byte("model = \"gpt-5.5\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MaterializeHome(dir, DefaultBaseURL); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `model = "pareto"`) {
		t.Fatal("restart must reclaim config.toml for pareto")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config mode = %o, want 0600", perm)
	}
}

func TestEnvForcesOurValues(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "CODEX_HOME=/theirs", "UNBIASED_API_KEY=sk_stale", "TERM=xterm"}
	got := Env(parent, "/ours", "sk_fresh")
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "/theirs") || strings.Contains(joined, "sk_stale") {
		t.Fatalf("parent values leaked through: %v", got)
	}
	if !strings.Contains(joined, "CODEX_HOME=/ours") || !strings.Contains(joined, "UNBIASED_API_KEY=sk_fresh") {
		t.Fatalf("our values missing: %v", got)
	}
}
