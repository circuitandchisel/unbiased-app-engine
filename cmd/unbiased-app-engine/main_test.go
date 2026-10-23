package main

import (
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWrapperProcess(t *testing.T) {
	if os.Getenv("UNBIASED_WRAPPER_TEST_HELPER") != "1" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("UNBIASED_WRAPPER_TEST_ARGS")), &args); err != nil {
		t.Fatal(err)
	}
	os.Args = append([]string{"unbiased-app-engine"}, args...)
	flag.CommandLine = flag.NewFlagSet("wrapper", flag.ContinueOnError)
	if err := run(); err != nil {
		t.Fatal(err)
	}
}
func TestExplicitStartupAndDefaultLocalCompatibility(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "local-home")
	if err = os.MkdirAll(filepath.Join(home, ".unbiased"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, ".unbiased", "credentials.json"), []byte(`{"apiKey":"local-file-key"}`), 0600); err != nil {
		t.Fatal(err)
	}
	// A fake local executable, no network and no model. It reports only test environment.
	fake := filepath.Join(root, "fake-engine")
	if err = os.WriteFile(fake, []byte("#!/bin/sh\n/usr/bin/env\npwd\n"), 0700); err != nil {
		t.Fatal(err)
	}
	testHome := home
	invoke := func(args []string, key string) (string, error) {
		t.Helper()
		encoded, _ := json.Marshal(args)
		command := exec.Command(os.Args[0], "-test.run=^TestWrapperProcess$")
		command.Env = []string{"PATH=/usr/bin:/bin", "UNBIASED_WRAPPER_TEST_HELPER=1", "UNBIASED_WRAPPER_TEST_ARGS=" + string(encoded), "AWS_SECRET_ACCESS_KEY=fake-parent-secret"}
		if testHome != "" {
			command.Env = append(command.Env, "HOME="+testHome)
		}
		if key != "" {
			command.Env = append(command.Env, "UNBIASED_API_KEY="+key)
		}
		out, err := command.CombinedOutput()
		return string(out), err
	}
	local, err := invoke([]string{"--engine", fake}, "")
	if err != nil {
		t.Fatal(local, err)
	}
	if !strings.Contains(local, "UNBIASED_API_KEY=local-file-key") || !strings.Contains(local, "AWS_SECRET_ACCESS_KEY=fake-parent-secret") {
		t.Fatal("default local startup changed", local)
	}
	runtime := filepath.Join(root, "runtime")
	if err = os.MkdirAll(filepath.Join(runtime, "artifacts"), 0700); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "manifest.json")
	data := `{"schemaVersion":1,"owner":{"organizationId":"org","memberId":"user"},"scope":{"kind":"global"},"revision":1,"skills":[],"connectors":[{"id":"docs","name":"docs","origin":{"kind":"global"},"endpoint":"https://example.com/mcp","enabled":true},{"id":"private","name":"private","origin":{"kind":"global"},"endpoint":"https://example.com/mcp","enabled":true,"credentialRef":"opaque-ref"}],"repositories":[]}`
	if err = os.WriteFile(manifest, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	paths := filepath.Join(root, "paths.json")
	mapping, _ := json.Marshal(map[string]any{"runtimeRoot": runtime, "artifactRoot": filepath.Join(runtime, "artifacts"), "engineHome": filepath.Join(runtime, "engine"), "repositories": map[string]string{}})
	if err = os.WriteFile(paths, mapping, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--engine", fake, "--environment-manifest", manifest, "--runtime-paths", paths}
	output, err := invoke(args, "")
	if err == nil || !strings.Contains(output, "local credential fallback is disabled") {
		t.Fatal("explicit startup read local credentials", output)
	}
	testHome = "" // Explicit mode must work without a developer HOME.
	output, err = invoke(args, "explicit-runtime-key")
	if err != nil {
		t.Fatal(output, err)
	}
	if strings.Contains(output, "fake-parent-secret") || strings.Contains(output, "local-file-key") || strings.Contains(output, "TEST_HELPER") {
		t.Fatal("parent environment leaked", output)
	}
	if !strings.Contains(output, "UNBIASED_API_KEY=explicit-runtime-key") || !strings.HasSuffix(strings.TrimSpace(output), runtime) {
		t.Fatal("runtime key/cwd not isolated", output)
	}
	config, err := os.ReadFile(filepath.Join(runtime, "engine", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), "[mcp_servers.docs]\nenabled = false") || strings.Contains(string(config), "[mcp_servers.private]") {
		t.Fatal("connector availability enabled tools or unresolved credentials", string(config))
	}

}
