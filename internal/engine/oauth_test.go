package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOAuthCredentialRotationAndSafeFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	for _, data := range []string{`{"apiKey":"device-issued-token"}`, `{"accessToken":"rotated-user-token","apiKey":"stale-key"}`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		token, err := ResolveOAuthToken(path)
		if err != nil {
			t.Fatal(err)
		}
		want := "device-issued-token"
		if strings.Contains(data, "accessToken") {
			want = "rotated-user-token"
		}
		if token != want {
			t.Fatal("did not resolve the current sign-in credential")
		}
	}
	for _, data := range []string{`{"accessToken":"secret-must-not-leak"`, `{"accessToken":"line1\nsecret-must-not-leak"}`, `{}`, strings.Repeat("secret-must-not-leak", 5000)} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := ResolveOAuthToken(path)
		if err == nil || strings.Contains(err.Error(), "secret-must-not-leak") {
			t.Fatal("invalid credentials did not fail safely")
		}
	}
}

func TestOAuthHomeKeepsSecretsOutOfConfigAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	credentials := filepath.Join(dir, "signed in.json")
	if err := os.WriteFile(credentials, []byte(`{"accessToken":"private-user-token"}`), 0600); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "engine")
	if err := MaterializeOAuthHome(home, DefaultBaseURL, nil, filepath.Join(dir, "unbiased engine"), credentials); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	config := string(data)
	if strings.Contains(config, "private-user-token") || strings.Contains(config, "env_key =") || !strings.Contains(config, "auth-token") || !strings.Contains(config, "signed in.json") {
		t.Fatal("invalid sign-in configuration")
	}
	for _, entry := range Env([]string{"UNBIASED_API_KEY=stale-parent-key"}, home, "") {
		if strings.HasPrefix(entry, "UNBIASED_API_KEY=") {
			t.Fatal("OAuth mode inherited an API key")
		}
	}
}
