package engine

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ResolveOAuthToken rereads the sign-in credential on every invocation so a
// host can rotate it without restarting the engine. apiKey is the legacy
// desktop/device-login field; accessToken is the credential's explicit name.
// Errors never include credential contents or JSON parser diagnostics.
func ResolveOAuthToken(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("cannot read Unbiased sign-in credentials; sign in or reconnect")
	}
	defer file.Close()
	const limit = 64 * 1024
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || len(data) > limit {
		return "", fmt.Errorf("cannot read Unbiased sign-in credentials")
	}
	var credentials struct {
		AccessToken string `json:"accessToken"`
		APIKey      string `json:"apiKey"`
	}
	if json.Unmarshal(data, &credentials) != nil {
		return "", fmt.Errorf("invalid Unbiased sign-in credentials; sign in or reconnect")
	}
	token := strings.TrimSpace(credentials.AccessToken)
	if token == "" {
		token = strings.TrimSpace(credentials.APIKey)
	}
	if token == "" || strings.ContainsAny(token, "\r\n\x00") {
		return "", fmt.Errorf("Unbiased sign-in token is unavailable; sign in or reconnect")
	}
	return token, nil
}

// MaterializeOAuthHome configures the app-server's primary bearer-token
// provider. No credential is embedded in TOML, argv or the child environment.
func MaterializeOAuthHome(dir, baseURL string, servers []MCPServer, command, credentials string) error {
	if !filepath.IsAbs(command) || !filepath.IsAbs(credentials) {
		return fmt.Errorf("OAuth command and credential paths must be absolute")
	}
	quote := func(value string) string {
		data, _ := json.Marshal(value) // JSON string escaping is valid TOML here.
		return string(data)
	}
	auth := fmt.Sprintf("auth = { command = %s, args = [\"auth-token\", \"--credentials-file\", %s], cwd = %s, refresh_interval_ms = 1000 }",
		quote(command), quote(credentials), quote(filepath.Dir(command)))
	return materializeHome(dir, baseURL, servers, auth)
}
