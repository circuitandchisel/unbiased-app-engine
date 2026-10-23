package environment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) Manifest {
	t.Helper()
	data, err := os.ReadFile("../../contracts/environment/fixtures/valid.json")
	if err != nil {
		t.Fatal(err)
	}
	m, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func TestSharedContractFixtures(t *testing.T) {
	m := fixture(t)
	encoded, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Decode(encoded); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../contracts/environment/fixtures/invalid.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string          `json:"name"`
		Manifest json.RawMessage `json:"manifest"`
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			if _, err := Decode(tc.Manifest); err == nil {
				t.Fatal("invalid fixture accepted")
			}
		})
	}
	if _, err := Decode([]byte(strings.Repeat(" ", MaxManifestBytes+1))); err == nil {
		t.Fatal("size limit not enforced")
	}
}
func paths(t *testing.T) Paths {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	artifacts := filepath.Join(root, "artifacts")
	if err = os.MkdirAll(artifacts, 0700); err != nil {
		t.Fatal(err)
	}
	return Paths{RuntimeRoot: root, ArtifactRoot: artifacts, EngineHome: filepath.Join(root, "engine"), Repositories: map[string]string{"checkout-1": filepath.Join(root, "checkout")}}
}
func TestPrepareRejectsScopeAndUnsafePathsBeforeWriting(t *testing.T) {
	m := fixture(t)
	p := paths(t)
	if _, err := Prepare(m, p); err == nil || !strings.Contains(err.Error(), "SCOPED_SKILL") {
		t.Fatal("scoped skills exposed globally", err)
	}
	m.Skills = []Skill{}
	m.Scope = Scope{Kind: "global"}
	bad := p
	bad.EngineHome = filepath.Join(p.RuntimeRoot, "..", "escape")
	if _, err := Prepare(m, bad); err == nil {
		t.Fatal("traversal accepted")
	}
	outside := filepath.Join(p.RuntimeRoot, "link")
	if err := os.Symlink(p.ArtifactRoot, outside); err != nil {
		t.Fatal(err)
	}
	bad = p
	bad.ArtifactRoot = outside
	if _, err := Prepare(m, bad); err == nil {
		t.Fatal("symlink accepted")
	}
	if _, err := os.Stat(p.EngineHome); !os.IsNotExist(err) {
		t.Fatal("validation modified generated output")
	}
}
func TestArtifactsAreIntegrityCheckedAndBounded(t *testing.T) {
	m := fixture(t)
	m.Skills[0].Origin = Scope{Kind: "global"}
	m.Scope = Scope{Kind: "global"}
	p := paths(t)
	artifact := filepath.Join(p.ArtifactRoot, m.Skills[0].Path)
	if err := os.MkdirAll(filepath.Dir(artifact), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("# Review\n"), 0600); err != nil {
		t.Fatal(err)
	}
	prepared, err := Prepare(m, p)
	if err != nil {
		t.Fatal(err)
	}
	if err = prepared.Write(); err != nil {
		t.Fatal(err)
	}
	if err = prepared.Write(); err == nil {
		t.Fatal("existing engine home overwritten")
	}
	if _, err = os.ReadFile(filepath.Join(p.EngineHome, "skills", "review", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(artifact, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Prepare(m, p); err == nil {
		t.Fatal("bad digest accepted")
	}
	if err = os.WriteFile(artifact, []byte(strings.Repeat("x", MaxSkillBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Prepare(m, p); err == nil {
		t.Fatal("oversize skill accepted")
	}
}
func TestExplicitEnvironmentDoesNotInheritHostVariables(t *testing.T) {
	t.Setenv("AWS_SECRET_ACCESS_KEY", "do-not-forward")
	t.Setenv("OPENAI_API_KEY", "do-not-forward")
	t.Setenv("CODEX_HOME", "/developer/home")
	env := ChildEnv("/runtime/engine", "explicit-key")
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "do-not-forward") || strings.Contains(joined, "/developer") {
		t.Fatal("host environment leaked")
	}
	if !strings.Contains(joined, "CODEX_HOME=/runtime/engine") || !strings.Contains(joined, "UNBIASED_API_KEY=explicit-key") {
		t.Fatal("required explicit environment missing")
	}
}

func TestGeneratedHomeIsPublishedOnlyAfterSuccessfulSetup(t *testing.T) {
	m := fixture(t)
	m.Scope = Scope{Kind: "global"}
	m.Skills = []Skill{}
	p := paths(t)
	prepared, err := Prepare(m, p)
	if err != nil {
		t.Fatal(err)
	}
	if err = prepared.WriteGenerated(func(stage string) error {
		if err := os.WriteFile(filepath.Join(stage, "config.toml"), []byte("partial"), 0600); err != nil {
			t.Fatal(err)
		}
		return fmt.Errorf("setup failed")
	}); err == nil {
		t.Fatal("failure ignored")
	}
	if _, err = os.Lstat(p.EngineHome); !os.IsNotExist(err) {
		t.Fatal("partial home published")
	}
	if err = prepared.WriteGenerated(func(stage string) error {
		return os.WriteFile(filepath.Join(stage, "config.toml"), []byte("complete"), 0600)
	}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(p.EngineHome, "config.toml"))
	if err != nil || string(contents) != "complete" {
		t.Fatal("complete home missing", err)
	}
}
func TestRuntimeMappingsDoNotAliasGeneratedOrArtifactState(t *testing.T) {
	m := fixture(t)
	m.Scope = Scope{Kind: "global"}
	m.Skills = []Skill{}
	for _, target := range []string{"artifacts", "engine", "engine/subdir"} {
		p := paths(t)
		p.Repositories["checkout-1"] = filepath.Join(p.RuntimeRoot, target)
		if _, err := Prepare(m, p); err == nil {
			t.Fatalf("overlapping mapping accepted: %s", target)
		}
	}
	p := paths(t)
	p.EngineHome = filepath.Join(p.ArtifactRoot, "engine")
	if _, err := Prepare(m, p); err == nil {
		t.Fatal("engine home overlaps artifact input")
	}
}
