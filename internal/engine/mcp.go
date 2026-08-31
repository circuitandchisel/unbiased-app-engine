package engine

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// MCP (Model Context Protocol) servers give the engine tools we do not write.
// codex has supported them natively for a long time; what was missing was any
// way to ask for one, because this package owns config.toml outright and the
// template never mentioned them.
//
// Two things had to be true before this was worth wiring, and both now are:
//
//  1. The gateway speaks namespace tools. codex declares an MCP server's tools
//     as a `namespace` group, and chat-completions has no namespace concept.
//     gpu-router#414 flattens each inner tool to `<namespace>__<name>` upstream
//     and splits it back on the way out. Multi-agent v2 has ridden that exact
//     path in production since 2026-08 — MCP is the second consumer of a live
//     mechanism, not a new one.
//  2. Rendering stays ours. Servers are validated and escaped here, then
//     written by MaterializeHome. Nothing reads ~/.codex, and no string
//     reaches config.toml without passing the checks below.
//
// The security posture is unchanged and deliberately narrow: BuiltinMCPServers
// is compiled in, so the set of servers is a property of the binary, exactly
// like the provider block. Handing this list to something the user can edit is
// a different feature with a different threat model — see the note there.

// mcpNameRe is the grammar for a server name. Deliberately stricter than TOML
// bare keys: this name becomes part of a tool name that must survive three
// more hops (codex namespace, gateway flattening, the upstream provider), and
// every one of those grammars is `[A-Za-z0-9_-]`.
var mcpNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// mcpEnvKeyRe is the POSIX environment-variable-name grammar.
var mcpEnvKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// maxMCPNameLen keeps every tool a server exposes routable.
//
// The arithmetic, which is the whole reason this constant is not a round
// number. A tool reaches the provider as:
//
//	mcp__<server>__<tool>     5 + len(server) + 2 + len(tool)
//
// and gpu-router caps a flattened function name at 64 (dialects/responses.ts,
// MAX_TOOL_NAME_LEN — OpenAI's limit, enforced at flatten time so a rejection
// blames the router's own identifier rather than the client's). That leaves 57
// characters to divide between the server name and the longest tool name it
// exposes. Spending 24 here guarantees 33 for the tool, which clears every
// MCP server we have looked at.
//
// Getting this wrong is not a config error, it is a 400 on the user's first
// turn with that server, from a component two repos away. Failing here instead
// costs a `go test`.
const maxMCPNameLen = 24

// maxMCPToolNameLen is what a server's longest tool name may be, given the
// budget above. Not enforced here — we do not know a server's tools until it
// starts — but stated so the number has a home and the split is auditable.
const maxMCPToolNameLen = 57 - maxMCPNameLen

// MCPServer describes one MCP server for the engine to launch.
//
// Exactly one transport must be set: Command (stdio, a local child process) or
// URL (streamable HTTP, a remote server). codex infers the transport from
// which one is present, so neither is written as an explicit key.
type MCPServer struct {
	// Name is the server's key in config.toml and the stem of every tool name
	// it contributes. Must match mcpNameRe and fit maxMCPNameLen.
	Name string `json:"name"`

	// Command and Args launch a stdio server. Command is not resolved here:
	// it is looked up on the engine's PATH at start, by codex.
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`

	// Env sets variables for a stdio server's process, and is the ONLY way to
	// give one a variable: codex does not pass the engine's environment to an
	// MCP child. Measured against the pinned 0.147 binary, a stdio child gets
	// HOME, LOGNAME, PATH, SHELL, TMPDIR, USER, __CF_USER_TEXT_ENCODING and
	// these entries — nothing else, and notably not UNBIASED_API_KEY.
	Env map[string]string `json:"env,omitempty"`

	// URL addresses a streamable-HTTP server. BearerTokenEnvVar names the
	// variable holding its credential; the value is never written to
	// config.toml, only the variable's name.
	URL               string `json:"url,omitempty"`
	BearerTokenEnvVar string `json:"bearerTokenEnvVar,omitempty"`

	// OAuthClientID names an OAuth client WE registered with the provider.
	//
	// Without it codex falls back to dynamic client registration, which sends
	// a hardcoded client_name of "Codex" — so the provider's consent screen
	// reads "Codex is requesting access to your Honeycomb account" inside a
	// product that is not Codex. That name is not configurable: it appears in
	// the binary only in the DCR request, never as a config key. Supplying a
	// client_id skips registration entirely and the provider then shows the
	// application we registered, with our name and our logo.
	//
	// Scopes travel with it because a pre-registered client is granted what it
	// asked for at registration time, and OAuthResource covers providers that
	// require an RFC 8707 resource indicator.
	// Enabled turns a server off WITHOUT forgetting it. Absent means on, so
	// every existing file keeps its behaviour. A disabled server is simply not
	// written to config.toml, which is what codex reads — but its entry, and
	// crucially its OAuth client id, stay on disk. Deleting the server instead
	// would throw away a registration the user had to approve in a browser,
	// and re-adding it would mint a new client at the provider.
	Enabled *bool `json:"enabled,omitempty"`

	OAuthClientID string   `json:"oauthClientId,omitempty"`
	Scopes        []string `json:"scopes,omitempty"`
	OAuthResource string   `json:"oauthResource,omitempty"`

	// OAuthClientSecret routes the whole server through codex's PLUGIN
	// machinery instead of a [mcp_servers] table, because the pinned binary's
	// config surface has nowhere to put a secret: its McpServerOAuthConfig
	// parses client_id and nothing else, while the plugin .mcp.json schema
	// takes client_id, client_secret and callback_port — the bundled Google
	// connectors ship exactly that shape. Google's installed-app token
	// exchange requires the secret (which Google itself documents as
	// non-confidential for native apps), so without this field Gmail, Drive
	// and Calendar cannot complete a sign-in at all.
	OAuthClientSecret string `json:"oauthClientSecret,omitempty"`

	// StartupTimeoutSec bounds the handshake; ToolTimeoutSec bounds one call.
	// Zero means "let codex use its default" and omits the key.
	StartupTimeoutSec int `json:"startupTimeoutSec,omitempty"`
	ToolTimeoutSec    int `json:"toolTimeoutSec,omitempty"`

	// EnabledTools, when non-empty, restricts this server to those tool names.
	// Preferring an allowlist over a denylist means a server that grows a new
	// tool in a later release does not silently gain reach.
	EnabledTools []string `json:"enabledTools,omitempty"`
}

// BuiltinMCPServers is the set of MCP servers this engine build ships with.
//
// Empty by default, which writes no `[mcp_servers.*]` table at all, so the
// engine behaves exactly as it did before this feature existed. Add entries
// here to ship a server.
//
// This is the single seam. If MCP servers ever become user-configurable, the
// user's list must arrive here — validated by the same validateMCPServers and
// rendered by the same renderMCPServers — and NOT by relaxing MaterializeHome
// or letting anything else write config.toml. Be aware that doing so trades
// away the guarantee this package exists to provide: today config cannot be
// hostile because we are the only writer, and a user-supplied server is a
// process we launch, on the user's machine, with the engine's environment.
func BuiltinMCPServers() []MCPServer {
	return nil
}

// isLoopbackHost reports whether host addresses this machine only. Uses
// net.IP.IsLoopback so the whole 127.0.0.0/8 block and ::1 are covered, rather
// than string-matching "127.0.0.1" and missing 127.0.0.2.
func isLoopbackHost(host string) bool {
	// Lowercased because url.Parse preserves host case while the browser's
	// URL() normalises it. Without this the desktop app accepted
	// "http://LocalHost:3845/mcp", wrote it to the user's config, and the
	// engine then refused to start on the same value. Host names are
	// case-insensitive anyway (RFC 4343).
	host = strings.ToLower(host)
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// validate checks one server hard enough that rendering it cannot produce
// invalid or surprising TOML.
func (s MCPServer) validate() error {
	if s.Name == "" {
		return fmt.Errorf("mcp server has no name")
	}
	if !mcpNameRe.MatchString(s.Name) {
		return fmt.Errorf("mcp server %q: name must contain only letters, digits, underscores, and hyphens", s.Name)
	}
	if len(s.Name) > maxMCPNameLen {
		return fmt.Errorf("mcp server %q: name is %d characters, limit is %d — longer names push tool names past the gateway's 64-character cap (see maxMCPNameLen)", s.Name, len(s.Name), maxMCPNameLen)
	}

	hasStdio, hasHTTP := s.Command != "", s.URL != ""
	switch {
	case hasStdio && hasHTTP:
		return fmt.Errorf("mcp server %q: set Command or URL, not both — codex infers the transport from which is present", s.Name)
	case !hasStdio && !hasHTTP:
		return fmt.Errorf("mcp server %q: needs a Command (stdio) or a URL (streamable http)", s.Name)
	}

	if hasHTTP {
		u, err := url.Parse(s.URL)
		if err != nil || u.Host == "" {
			return fmt.Errorf("mcp server %q: URL is not a valid absolute URL", s.Name)
		}
		switch u.Scheme {
		case "https":
			// Fine anywhere.
		case "http":
			// Plaintext is refused for a network hop — an MCP server sees whole
			// conversations, and any bearer rides the same connection — but
			// loopback never leaves the machine, so there is nothing to
			// intercept. Locally-run servers are the common case (Figma's Dev
			// Mode server is http://127.0.0.1:3845/mcp), and rejecting them
			// would force the user to choose between this rule and the feature.
			// Deliberately loopback ONLY: 10./192.168./169.254. are real hops.
			if !isLoopbackHost(u.Hostname()) {
				return fmt.Errorf("mcp server %q: http:// is allowed only on localhost — use https:// for anything off this machine", s.Name)
			}
		default:
			return fmt.Errorf("mcp server %q: URL scheme must be https, or http on localhost", s.Name)
		}
		if len(s.Args) > 0 || len(s.Env) > 0 {
			return fmt.Errorf("mcp server %q: Args and Env apply to stdio servers only", s.Name)
		}
	}
	if hasStdio && s.BearerTokenEnvVar != "" {
		return fmt.Errorf("mcp server %q: BearerTokenEnvVar applies to http servers only", s.Name)
	}
	if hasStdio && (s.OAuthClientID != "" || len(s.Scopes) > 0 || s.OAuthResource != "" || s.OAuthClientSecret != "") {
		return fmt.Errorf("mcp server %q: OAuth settings apply to http servers only", s.Name)
	}
	// The client id is written into config.toml verbatim, so it is held to the
	// same quoting rule as every other value there.
	if s.OAuthClientSecret != "" && s.OAuthClientID == "" {
		return fmt.Errorf("mcp server %q: OAuthClientSecret without OAuthClientID", s.Name)
	}
	for _, v := range append([]string{s.OAuthClientID, s.OAuthResource, s.OAuthClientSecret}, s.Scopes...) {
		if strings.ContainsAny(v, "\"\\") || strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 }) {
			return fmt.Errorf("mcp server %q: OAuth values cannot contain quotes, backslashes or control characters", s.Name)
		}
	}
	if s.BearerTokenEnvVar != "" && !mcpEnvKeyRe.MatchString(s.BearerTokenEnvVar) {
		return fmt.Errorf("mcp server %q: BearerTokenEnvVar %q is not a valid environment variable name", s.Name, s.BearerTokenEnvVar)
	}

	for k := range s.Env {
		if !mcpEnvKeyRe.MatchString(k) {
			return fmt.Errorf("mcp server %q: env key %q is not a valid environment variable name", s.Name, k)
		}
		// codex strips both from an MCP child's environment on its own; this
		// stops `env` putting them back. Setting UNBIASED_API_KEY here would
		// hand a third-party process the gateway credential that codex just
		// took away, and CODEX_HOME would point it at the engine's own state.
		if k == "UNBIASED_API_KEY" || k == "CODEX_HOME" {
			return fmt.Errorf("mcp server %q: env must not set %s — it is reserved by the supervisor", s.Name, k)
		}
	}

	if s.StartupTimeoutSec < 0 || s.ToolTimeoutSec < 0 {
		return fmt.Errorf("mcp server %q: timeouts cannot be negative", s.Name)
	}

	// Every string that will be quoted into TOML. Control characters and
	// stray quotes are rejected rather than escaped: no legitimate command,
	// argument, or URL contains one, so accepting them would only widen what
	// this function has to reason about.
	fields := map[string][]string{
		"Command":      {s.Command},
		"URL":          {s.URL},
		"Args":         s.Args,
		"EnabledTools": s.EnabledTools,
	}
	for field, values := range fields {
		for _, v := range values {
			if err := checkTOMLSafe(v); err != nil {
				return fmt.Errorf("mcp server %q: %s: %w", s.Name, field, err)
			}
		}
	}
	for k, v := range s.Env {
		if err := checkTOMLSafe(v); err != nil {
			return fmt.Errorf("mcp server %q: env[%s]: %w", s.Name, k, err)
		}
	}
	for _, t := range s.EnabledTools {
		if !mcpNameRe.MatchString(t) {
			return fmt.Errorf("mcp server %q: enabled tool %q must contain only letters, digits, underscores, and hyphens", s.Name, t)
		}
	}
	return nil
}

// checkTOMLSafe rejects values that cannot be written as a TOML basic string
// without interpretation.
func checkTOMLSafe(v string) error {
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("value contains a control character")
		}
	}
	if strings.ContainsAny(v, "\"\\") {
		return fmt.Errorf("value contains a quote or backslash")
	}
	return nil
}

// managedPluginServers returns the enabled servers that must ship as plugins
// because they carry an OAuth client secret.
func managedPluginServers(servers []MCPServer) []MCPServer {
	var out []MCPServer
	for _, s := range servers {
		if s.OAuthClientSecret == "" {
			continue
		}
		if s.Enabled != nil && !*s.Enabled {
			continue
		}
		out = append(out, s)
	}
	return out
}

// secretProxyPort mirrors SECRET_PROXY_PORTS/secretProxyPort in the desktop
// app's src/main/oauth-proxy.ts — the app listens there, these plugin files
// dial there, and the two must agree or the connector dials a dead port. The
// proxy exists because the pinned codex loses a plugin's client_secret
// between authorize and token exchange; the app-side proxy re-injects it.
func secretProxyPort(name string) int {
	switch name {
	case "gmail":
		return 45991
	case "google-calendar":
		return 45992
	case "google-drive":
		return 45993
	}
	var h uint32
	for _, c := range name {
		h = h*31 + uint32(c)
	}
	return 45900 + int(h%80)
}

// WriteManagedPlugins materializes the plugin directories the managed
// marketplace lines in config.toml point at: for each secret-bearing server,
// a minimal .codex-plugin/plugin.json, an .mcp.json carrying the OAuth
// client, and one marketplace.json indexing them. The whole tree is rebuilt
// from scratch each start — the same regenerate-don't-mutate rule as
// config.toml, so a removed connector's secret does not linger on disk.
func WriteManagedPlugins(homeDir string, servers []MCPServer) error {
	root := filepath.Join(homeDir, "managed-plugins")
	managed := managedPluginServers(servers)
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("clearing managed plugins: %w", err)
	}
	// Codex COPIES a marketplace plugin into <home>/plugins/cache/<market>/
	// <name>/<version>/ on first load and reads the copy from then on, keyed
	// by version. Rewriting the source is therefore invisible to it: a cache
	// written before this connector moved to the proxy kept sending codex to
	// the provider directly, with a secret it then dropped — the "client_secret
	// is missing" failure survived every fix until this cache was cleared.
	// Wiping our own marketplace's cache each start keeps the running config
	// and the generated files in lockstep (and takes any secret an older
	// cached copy still holds with it).
	if err := os.RemoveAll(filepath.Join(homeDir, "plugins", "cache", "unbiased-managed")); err != nil {
		return fmt.Errorf("clearing managed plugin cache: %w", err)
	}
	if len(managed) == 0 {
		return nil
	}
	type marketEntry struct {
		Name   string `json:"name"`
		Source struct {
			Source string `json:"source"`
			Path   string `json:"path"`
		} `json:"source"`
		Policy struct {
			Installation   string `json:"installation"`
			Authentication string `json:"authentication"`
		} `json:"policy"`
	}
	var entries []marketEntry
	for _, s := range managed {
		dir := filepath.Join(root, "plugins", s.Name)
		if err := os.MkdirAll(filepath.Join(dir, ".codex-plugin"), 0o700); err != nil {
			return fmt.Errorf("creating managed plugin %s: %w", s.Name, err)
		}
		oauthCfg := map[string]any{"client_id": s.OAuthClientID, "callback_port": 45999}
		proxiedURL, perr := url.Parse(s.URL)
		if perr != nil {
			return fmt.Errorf("managed plugin %s: %w", s.Name, perr)
		}
		serverCfg := map[string]any{
			"type":  "http",
			"url":   fmt.Sprintf("http://127.0.0.1:%d%s", secretProxyPort(s.Name), proxiedURL.EscapedPath()),
			"oauth": oauthCfg,
		}
		if len(s.Scopes) > 0 {
			serverCfg["scopes"] = s.Scopes
		}
		server := serverCfg
		// Version tracks the content, so even a cache we failed to clear cannot
		// answer for a plugin whose config has changed.
		jbForHash, _ := json.Marshal(server)
		sum := sha256.Sum256(jbForHash)
		version := fmt.Sprintf("0.0.%d", binary.BigEndian.Uint32(sum[:4])%100000)
		manifest := map[string]any{"name": s.Name, "version": version}
		mb, _ := json.MarshalIndent(manifest, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, ".codex-plugin", "plugin.json"), mb, 0o600); err != nil {
			return fmt.Errorf("writing plugin.json for %s: %w", s.Name, err)
		}
		if len(s.Scopes) > 0 {
			server["scopes"] = s.Scopes
		}
		// No client_secret in the file: codex reads it and then loses it before
		// the token exchange, so the app-side proxy injects it there instead.
		// The URL above points at that proxy; MCP traffic passes through it
		// untouched, only the token endpoint is intercepted.
		mcp := map[string]any{"mcpServers": map[string]any{s.Name: server}}
		jb, _ := json.MarshalIndent(mcp, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), jb, 0o600); err != nil {
			return fmt.Errorf("writing .mcp.json for %s: %w", s.Name, err)
		}
		var e marketEntry
		e.Name = s.Name
		e.Source.Source = "local"
		e.Source.Path = "./plugins/" + s.Name
		e.Policy.Installation = "AVAILABLE"
		e.Policy.Authentication = "ON_USE"
		entries = append(entries, e)
	}
	idxDir := filepath.Join(root, ".agents", "plugins")
	if err := os.MkdirAll(idxDir, 0o700); err != nil {
		return fmt.Errorf("creating marketplace index dir: %w", err)
	}
	idx := map[string]any{"name": "unbiased-managed", "plugins": entries}
	ib, _ := json.MarshalIndent(idx, "", "  ")
	if err := os.WriteFile(filepath.Join(idxDir, "marketplace.json"), ib, 0o600); err != nil {
		return fmt.Errorf("writing marketplace.json: %w", err)
	}
	return nil
}

// validateMCPServers checks a whole set, including cross-server rules.
func validateMCPServers(servers []MCPServer) error {
	seen := make(map[string]struct{}, len(servers))
	for _, s := range servers {
		if err := s.validate(); err != nil {
			return err
		}
		// TOML would accept a duplicate table and let the last one win; codex
		// would then run something other than what the list says.
		lower := strings.ToLower(s.Name)
		if _, dup := seen[lower]; dup {
			return fmt.Errorf("mcp server %q: duplicate name (names are compared case-insensitively, because tool names collide that way downstream)", s.Name)
		}
		seen[lower] = struct{}{}
	}
	return nil
}

// tomlString quotes a value that has already passed checkTOMLSafe.
func tomlString(v string) string {
	return `"` + v + `"`
}

// tomlStringArray renders a TOML array of basic strings.
func tomlStringArray(vs []string) string {
	quoted := make([]string, 0, len(vs))
	for _, v := range vs {
		quoted = append(quoted, tomlString(v))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// renderMCPServers produces the `[mcp_servers.*]` tables, or "" for an empty
// set so the template placeholder collapses to nothing.
//
// Callers must have validated first; renderMCPServers assumes it.
func renderMCPServers(servers []MCPServer) string {
	if len(servers) == 0 {
		return ""
	}
	var b strings.Builder
	// Secret-bearing servers ride the plugin path (see OAuthClientSecret); the
	// files themselves are written by MaterializeHome, and these lines make
	// codex load them. Emitted BEFORE the [mcp_servers] tables so a plugin
	// line can never land inside a server's sub-table.
	managed := managedPluginServers(servers)
	if len(managed) > 0 {
		fmt.Fprintf(&b, "\n[marketplaces.unbiased-managed]\nsource_type = \"local\"\nsource = %s\n", tomlString("{{HOME}}/managed-plugins"))
		for _, s := range managed {
			fmt.Fprintf(&b, "\n[plugins.%s]\nenabled = true\n", tomlString(s.Name+"@unbiased-managed"))
		}
	}
	for _, s := range servers {
		// Disabled servers are validated like any other — a bad entry should
		// be reported whether or not it is currently switched on — but they
		// are not written out, so codex never starts them.
		if s.Enabled != nil && !*s.Enabled {
			continue
		}
		if s.OAuthClientSecret != "" {
			continue // rendered as a managed plugin above
		}
		fmt.Fprintf(&b, "\n[mcp_servers.%s]\n", s.Name)
		if s.Command != "" {
			fmt.Fprintf(&b, "command = %s\n", tomlString(s.Command))
			if len(s.Args) > 0 {
				fmt.Fprintf(&b, "args = %s\n", tomlStringArray(s.Args))
			}
			if len(s.Env) > 0 {
				// Sorted: Go randomises map iteration, and a config that
				// differs between two starts of the same build is a config
				// nobody can diff.
				keys := make([]string, 0, len(s.Env))
				for k := range s.Env {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				pairs := make([]string, 0, len(keys))
				for _, k := range keys {
					pairs = append(pairs, fmt.Sprintf("%s = %s", k, tomlString(s.Env[k])))
				}
				fmt.Fprintf(&b, "env = { %s }\n", strings.Join(pairs, ", "))
			}
		} else {
			fmt.Fprintf(&b, "url = %s\n", tomlString(s.URL))
			if s.BearerTokenEnvVar != "" {
				fmt.Fprintf(&b, "bearer_token_env_var = %s\n", tomlString(s.BearerTokenEnvVar))
			}
			if len(s.Scopes) > 0 {
				fmt.Fprintf(&b, "scopes = %s\n", tomlStringArray(s.Scopes))
			}
			if s.OAuthResource != "" {
				fmt.Fprintf(&b, "oauth_resource = %s\n", tomlString(s.OAuthResource))
			}

		}
		if s.StartupTimeoutSec > 0 {
			fmt.Fprintf(&b, "startup_timeout_sec = %d\n", s.StartupTimeoutSec)
		}
		if s.ToolTimeoutSec > 0 {
			fmt.Fprintf(&b, "tool_timeout_sec = %d\n", s.ToolTimeoutSec)
		}
		if len(s.EnabledTools) > 0 {
			fmt.Fprintf(&b, "enabled_tools = %s\n", tomlStringArray(s.EnabledTools))
		}
		// MUST be last for this server. A TOML sub-table header ends the
		// parent table, so any scalar written after it would silently land
		// inside [mcp_servers.NAME.oauth] instead of on the server itself.
		if s.OAuthClientID != "" {
			fmt.Fprintf(&b, "\n[mcp_servers.%s.oauth]\n", s.Name)
			fmt.Fprintf(&b, "client_id = %s\n", tomlString(s.OAuthClientID))
		}
	}
	return b.String()
}

// DefaultMCPConfigPath is where the desktop app stores user-added MCP servers.
func DefaultMCPConfigPath(home string) string {
	return filepath.Join(home, ".unbiased", "mcp-servers.json")
}

// LoadUserMCPServers reads user-added MCP servers from path.
//
// A missing file is the normal case and returns nothing. Anything present but
// unusable is an error, not a warning: this file is written by the app, so a
// malformed one means it was hand-edited, and starting anyway would silently
// drop servers the user believes are configured. The message names the file
// and the defect so the fix is obvious.
//
// Every entry goes through the same validateMCPServers as a compiled-in
// server. That is the load-bearing part of letting a file influence
// config.toml at all: the file chooses WHICH servers, it never gets to choose
// what reaches the TOML.
func LoadUserMCPServers(path string) ([]MCPServer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var file struct {
		Servers []MCPServer `json:"servers"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := validateMCPServers(file.Servers); err != nil {
		return nil, fmt.Errorf("in %s: %w", path, err)
	}
	return file.Servers, nil
}

// MergeMCPServers combines the compiled-in servers with user-added ones and
// validates the result as a set, so a user server cannot shadow a builtin by
// reusing its name.
func MergeMCPServers(builtin, user []MCPServer) ([]MCPServer, error) {
	all := make([]MCPServer, 0, len(builtin)+len(user))
	all = append(all, builtin...)
	all = append(all, user...)
	if err := validateMCPServers(all); err != nil {
		return nil, err
	}
	return all, nil
}
