package engine

import (
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
	for _, s := range servers {
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
