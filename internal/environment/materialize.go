package environment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const MaxSkillBytes = 64 * 1024

// Paths are runtime-local inputs, never part of a portable manifest.
type Paths struct {
	RuntimeRoot  string            `json:"runtimeRoot"`
	ArtifactRoot string            `json:"artifactRoot"`
	EngineHome   string            `json:"engineHome"`
	Repositories map[string]string `json:"repositories"`
}
type Prepared struct {
	Home     string
	Skills   map[string][]byte
	Manifest Manifest
}

func NoSymlinks(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("absolute clean path required: %s", path)
	}
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(path, current), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe symlink: %s", current)
		}
	}
	return nil
}
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Prepare performs all validation before generated configuration is touched.
// v1 supports reviewed Markdown artifacts only. Digests prove integrity, not absence of secrets.
func Prepare(m Manifest, p Paths) (Prepared, error) {
	out := Prepared{Home: p.EngineHome, Skills: map[string][]byte{}, Manifest: m}
	if err := m.Validate(); err != nil {
		return out, err
	}
	for _, path := range []string{p.RuntimeRoot, p.ArtifactRoot, p.EngineHome} {
		if err := NoSymlinks(path); err != nil {
			return out, err
		}
	}
	if !within(p.RuntimeRoot, p.ArtifactRoot) || !within(p.RuntimeRoot, p.EngineHome) {
		return out, fmt.Errorf("artifact root and engine home must be inside runtime root")
	}
	// A shared member runtime cannot expose project/conversation roots globally.
	// Fail closed until a per-thread Rust skill-root API is available and verified.
	for _, s := range m.Skills {
		if s.Enabled && s.Origin.Kind != "global" {
			return out, fmt.Errorf("SCOPED_SKILL_ACTIVATION_UNSUPPORTED: pinned engine needs isolated per-thread roots")
		}
	}
	for _, c := range m.Connectors {
		if c.Enabled && c.Origin.Kind != "global" {
			return out, fmt.Errorf("SCOPED_CONNECTOR_ACTIVATION_UNSUPPORTED: pinned engine requires verified per-thread configuration")
		}
	}
	if m.Scope.Kind != "global" {
		return out, fmt.Errorf("SCOPED_ENVIRONMENT_ACTIVATION_UNSUPPORTED: a shared member runtime accepts global bootstrap only; project/conversation settings require verified per-thread materialization")
	}
	if len(p.Repositories) != len(m.Repositories) {
		return out, fmt.Errorf("exact repository path mappings required")
	}
	if p.ArtifactRoot == p.EngineHome || within(p.ArtifactRoot, p.EngineHome) || within(p.EngineHome, p.ArtifactRoot) {
		return out, fmt.Errorf("engine home and artifacts must not overlap")
	}
	seenPaths := map[string]bool{}
	for _, r := range m.Repositories {
		path, ok := p.Repositories[r.CheckoutID]
		if !ok || !within(p.RuntimeRoot, path) {
			return out, fmt.Errorf("missing/unsafe checkout mapping %s", r.CheckoutID)
		}
		if err := NoSymlinks(path); err != nil {
			return out, err
		}
		if seenPaths[path] || path == p.EngineHome || within(path, p.EngineHome) || within(p.EngineHome, path) || path == p.ArtifactRoot || within(path, p.ArtifactRoot) || within(p.ArtifactRoot, path) {
			return out, fmt.Errorf("checkout mappings must be distinct from generated/artifact paths")
		}
		seenPaths[path] = true
	}
	for _, s := range m.Skills {
		if !s.Enabled {
			continue
		}
		path := filepath.Join(p.ArtifactRoot, filepath.FromSlash(s.Path))
		if err := NoSymlinks(path); err != nil {
			return out, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return out, err
		}
		if !info.Mode().IsRegular() || info.Size() > MaxSkillBytes {
			return out, fmt.Errorf("invalid skill artifact")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return out, err
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != s.Digest {
			return out, fmt.Errorf("skill digest mismatch")
		}
		out.Skills[s.Name] = data
	}
	return out, nil
}

// Write uses a new home only: never overwrite another shared runtime's state.
func (p Prepared) Write() error { return p.WriteGenerated(nil) }

// WriteGenerated publishes only after all generated configuration is ready.
func (p Prepared) WriteGenerated(generate func(stage string) error) error {
	if err := NoSymlinks(p.Home); err != nil {
		return err
	}
	if _, err := os.Lstat(p.Home); !os.IsNotExist(err) {
		return fmt.Errorf("explicit engine home must be new; refusing to replace existing runtime state")
	}
	if err := os.MkdirAll(filepath.Dir(p.Home), 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(p.Home), ".environment-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for name, data := range p.Skills {
		dir := filepath.Join(stage, "skills", name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), data, 0600); err != nil {
			return err
		}
	}
	data, err := json.Marshal(p.Manifest)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "environment.json"), data, 0600); err != nil {
		return err
	}
	if generate != nil {
		if err := generate(stage); err != nil {
			return err
		}
	}
	return os.Rename(stage, p.Home)
}

// Explicit mode is an allowlist, independent of the developer's parent environment.
func ChildEnv(home, key string) []string {
	return []string{"HOME=" + filepath.Dir(home), "CODEX_HOME=" + home, "PATH=/usr/bin:/bin", "LANG=C.UTF-8", "UNBIASED_API_KEY=" + key, "UNBIASED_MAX_REQUEST_BODY_BYTES=7340032"}
}
