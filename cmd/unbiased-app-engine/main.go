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
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/circuitandchisel/unbiased-app-engine/internal/engine"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "unbiased-app-engine:", err)
		os.Exit(1)
	}
}

func run() error {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolving home directory: %w", err)
	}

	var (
		engineBin = flag.String("engine", "", "path to the pinned engine binary (default: bin/pareto-app-server next to this executable, then $PATH)")
		homeDir   = flag.String("home", engine.DefaultHomeDir(userHome), "engine home directory (regenerated on every start)")
		baseURL   = flag.String("base-url", envOr("UNBIASED_BASE_URL", engine.DefaultBaseURL), "Unbiased gateway base URL")
	)
	flag.Parse()

	bin, err := resolveEngineBin(*engineBin)
	if err != nil {
		return err
	}

	key, err := engine.ResolveKey(os.Getenv, engine.DefaultCredentialsPath(userHome))
	if err != nil {
		return err
	}

	if err := engine.MaterializeHome(*homeDir, *baseURL); err != nil {
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
