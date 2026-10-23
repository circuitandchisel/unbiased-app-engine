// unbiased-app-engine: a drop-in `codex app-server` that is locked to the
// Unbiased gateway (Pareto).
//
// It materializes a private CODEX_HOME, loads the Unbiased API key, and execs
// the pinned engine binary with stdio passed straight through — so any client
// that can speak the codex app-server JSON-RPC protocol can spawn this binary
// instead of codex and inherit login, model, and gateway policy for free.
//
// Usage:
//
//	unbiased-app-engine [flags] [-- engine args...]
//
// Everything after "--" is passed to the engine verbatim (e.g. --listen).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/circuitandchisel/unbiased-app-engine/internal/engine"
	"github.com/circuitandchisel/unbiased-app-engine/internal/environment"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "unbiased-app-engine:", err)
		os.Exit(1)
	}
}

func run() error {
	userHome, homeErr := os.UserHomeDir()

	var (
		engineBin    = flag.String("engine", "", "path to the pinned engine binary (default: bin/pareto-app-server next to this executable, then $PATH)")
		homeDir      = flag.String("home", engine.DefaultHomeDir(userHome), "engine home directory (regenerated on every start)")
		baseURL      = flag.String("base-url", envOr("UNBIASED_BASE_URL", engine.DefaultBaseURL), "Unbiased gateway base URL")
		manifestFile = flag.String("environment-manifest", "", "explicit portable environment manifest (no local config fallback)")
		mappingsFile = flag.String("runtime-paths", "", "explicit runtime-local path mappings")
		mcpConfig    = flag.String("mcp-config", engine.DefaultMCPConfigPath(userHome), "JSON file of user-added MCP servers (missing is fine)")
	)
	flag.Parse()

	bin, err := resolveEngineBin(*engineBin)
	if err != nil {
		return err
	}

	if *manifestFile != "" || *mappingsFile != "" {
		if *manifestFile == "" || *mappingsFile == "" {
			return fmt.Errorf("both --environment-manifest and --runtime-paths are required")
		}
		// No local credentials, MCP file, developer HOME or arbitrary engine arguments.
		if len(flag.Args()) != 0 {
			return fmt.Errorf("engine argument overrides are unsupported in explicit mode")
		}
		key := os.Getenv("UNBIASED_API_KEY")
		if key == "" {
			return fmt.Errorf("explicit environment needs UNBIASED_API_KEY; local credential fallback is disabled")
		}
		data, err := boundedFile(*manifestFile, environment.MaxManifestBytes)
		if err != nil {
			return err
		}
		manifest, err := environment.Decode(data)
		if err != nil {
			return err
		}
		data, err = boundedFile(*mappingsFile, environment.MaxManifestBytes)
		if err != nil {
			return err
		}
		var paths environment.Paths
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&paths); err != nil {
			return err
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return fmt.Errorf("trailing runtime path JSON")
		}
		prepared, err := environment.Prepare(manifest, paths)
		if err != nil {
			return err
		}
		// Every credential reference is unresolved in v1; do not advertise it as connected.
		servers := []engine.MCPServer{}
		for _, connector := range manifest.Connectors {
			if connector.Enabled && connector.CredentialRef == "" {
				servers = append(servers, engine.MCPServer{Name: connector.Name, URL: connector.Endpoint})
			}
			if connector.Enabled && connector.CredentialRef != "" {
				fmt.Fprintf(os.Stderr, "connector %s: sign-in required (credential references are not resolved)\n", connector.Name)
			}
		}
		if _, err := engine.RenderConfig(engine.DefaultBaseURL, servers); err != nil {
			return err
		}
		if err := prepared.WriteGenerated(func(stage string) error {
			if err := engine.MaterializeHome(stage, engine.DefaultBaseURL, servers); err != nil {
				return err
			}
			// Definitions are available but never automatically enabled for a conversation.
			generated := filepath.Join(stage, "config.toml")
			config, err := os.ReadFile(generated)
			if err != nil {
				return err
			}
			contents := string(config)
			for _, server := range servers {
				header := "[mcp_servers." + server.Name + "]\n"
				contents = strings.ReplaceAll(contents, header, header+"enabled = false\n")
			}
			return os.WriteFile(generated, []byte(contents), 0600)
		}); err != nil {
			return err
		}

		if err := os.Chdir(paths.RuntimeRoot); err != nil {
			return err
		}
		return syscall.Exec(bin, []string{bin}, environment.ChildEnv(paths.EngineHome, key))
	}

	if homeErr != nil {
		return fmt.Errorf("resolving home directory: %w", homeErr)
	}
	key, err := engine.ResolveKey(os.Getenv, engine.DefaultCredentialsPath(userHome))
	if err != nil {
		return err
	}

	// MCP servers: the compiled-in set, plus whatever the desktop app has
	// written to the user's config. Both go through the same validation before
	// a single character reaches config.toml — the file chooses WHICH servers
	// run, never what the TOML says.
	userServers, err := engine.LoadUserMCPServers(*mcpConfig)
	if err != nil {
		return err
	}
	mcpServers, err := engine.MergeMCPServers(engine.BuiltinMCPServers(), userServers)
	if err != nil {
		return err
	}
	if err := engine.MaterializeHome(*homeDir, *baseURL, mcpServers); err != nil {
		return err
	}

	// Exec, not spawn: the engine takes over our PID and stdio, so the client
	// that launched us supervises exactly one process and signal delivery
	// (SIGTERM on app quit, SIGINT from a terminal) reaches the engine intact.
	argv := append([]string{bin}, flag.Args()...)
	return syscall.Exec(bin, argv, engine.Env(os.Environ(), *homeDir, key))
}

// resolveEngineBin finds the engine binary. Precedence: explicit flag, the
// repo/install layout (bin/pareto-app-server next to this executable's
// directory), then $PATH. The flag and layout come first so a stray install
// on PATH can never shadow the pinned engine.
func resolveEngineBin(flagVal string) (string, error) {
	if flagVal != "" {
		abs, err := filepath.Abs(flagVal)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("engine binary not found at %s", abs)
		}
		return abs, nil
	}
	if self, err := os.Executable(); err == nil {
		for _, candidate := range []string{
			filepath.Join(filepath.Dir(self), "pareto-app-server"),
			filepath.Join(filepath.Dir(self), "..", "bin", "pareto-app-server"),
		} {
			if abs, err := filepath.Abs(candidate); err == nil {
				if _, err := os.Stat(abs); err == nil {
					return abs, nil
				}
			}
		}
	}
	if p, err := exec.LookPath("pareto-app-server"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("no engine binary found — run scripts/fetch-engine.sh (or pass --engine)")
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func boundedFile(path string, limit int64) ([]byte, error) {
	if err := environment.NoSymlinks(path); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("regular input file required")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("input exceeds size limit")
	}
	return data, nil
}
