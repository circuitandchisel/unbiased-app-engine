// Package environment implements portable manifest v1, independently of the desktop host.
package environment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
)

const MaxManifestBytes = 256 * 1024

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var commitPattern = regexp.MustCompile(`^[a-f0-9]{40,64}$`)

type Owner struct {
	OrganizationID string `json:"organizationId"`
	MemberID       string `json:"memberId"`
}
type Scope struct {
	Kind           string  `json:"kind"`
	ProjectID      *string `json:"projectId,omitempty"`
	ConversationID string  `json:"conversationId,omitempty"`
}

// A conversation scope always encodes projectId, including an explicit null for regular chats.
func (s Scope) MarshalJSON() ([]byte, error) {
	if s.Kind == "conversation" {
		return json.Marshal(struct {
			Kind           string  `json:"kind"`
			ProjectID      *string `json:"projectId"`
			ConversationID string  `json:"conversationId"`
		}{s.Kind, s.ProjectID, s.ConversationID})
	}
	type plain Scope
	return json.Marshal(plain(s))
}

type Skill struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Origin  Scope  `json:"origin"`
	Enabled bool   `json:"enabled"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
	Path    string `json:"path"`
}
type Connector struct {
	Origin        Scope  `json:"origin"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	Endpoint      string `json:"endpoint"`
	Enabled       bool   `json:"enabled"`
	CredentialRef string `json:"credentialRef,omitempty"`
}
type Repository struct {
	SourceID     string  `json:"sourceId"`
	RepositoryID *string `json:"repositoryId"`
	CheckoutID   string  `json:"checkoutId"`
	Kind         string  `json:"kind"`
	DisplayName  string  `json:"displayName"`
	Access       string  `json:"access"`
	Branch       string  `json:"branch,omitempty"`
	Commit       string  `json:"commit,omitempty"`
	Remote       string  `json:"remote,omitempty"`
	WorktreeName string  `json:"worktreeName,omitempty"`
}
type Manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	Owner         Owner        `json:"owner"`
	Scope         Scope        `json:"scope"`
	Revision      int64        `json:"revision"`
	Skills        []Skill      `json:"skills"`
	Connectors    []Connector  `json:"connectors"`
	Repositories  []Repository `json:"repositories"`
}

func validID(s string) bool { return idPattern.MatchString(s) }
func text(s string, max int) bool {
	if len(s) == 0 || len(s) > max {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool { return r < 32 || r == 127 })
}
func (s Scope) Validate() error {
	if s.ProjectID != nil && !validID(*s.ProjectID) {
		return fmt.Errorf("invalid project ID")
	}
	switch s.Kind {
	case "global":
		if s.ProjectID != nil || s.ConversationID != "" {
			return fmt.Errorf("invalid global scope")
		}
	case "project":
		if s.ProjectID == nil || s.ConversationID != "" {
			return fmt.Errorf("invalid project scope")
		}
	case "conversation":
		if !validID(s.ConversationID) {
			return fmt.Errorf("invalid conversation scope")
		}
	default:
		return fmt.Errorf("invalid scope")
	}
	return nil
}
func contains(parent, child Scope) bool {
	if parent.Kind == "global" {
		return true
	}
	if parent.Kind == "project" {
		return child.ProjectID != nil && *child.ProjectID == *parent.ProjectID
	}
	return child.Kind == "conversation" && parent.ConversationID == child.ConversationID && ((parent.ProjectID == nil && child.ProjectID == nil) || (parent.ProjectID != nil && child.ProjectID != nil && *parent.ProjectID == *child.ProjectID))
}

var endpointPattern = regexp.MustCompile(`^https://(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}(?:/(?:mcp|sse)?)?$`)

func ValidateEndpoint(endpoint string) error {
	if !endpointPattern.MatchString(endpoint) {
		return fmt.Errorf("portable connector requires canonical lowercase HTTPS DNS /mcp, /sse or / without credentials, query, fragment, port or normalized path segments")
	}
	return nil
}

func Decode(data []byte) (Manifest, error) {
	var m Manifest
	if len(data) > MaxManifestBytes {
		return m, fmt.Errorf("manifest too large")
	}
	// Check required fields before Go's zero values could turn a missing enabled into disablement.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return m, err
	}
	for _, key := range []string{"schemaVersion", "owner", "scope", "revision", "skills", "connectors", "repositories"} {
		if len(raw[key]) == 0 || string(raw[key]) == "null" {
			return m, fmt.Errorf("missing %s", key)
		}
	}
	for _, key := range []string{"skills", "connectors"} {
		var entries []map[string]json.RawMessage
		if err := json.Unmarshal(raw[key], &entries); err != nil {
			return m, err
		}
		for _, entry := range entries {
			if string(entry["enabled"]) != "true" && string(entry["enabled"]) != "false" {
				return m, fmt.Errorf("explicit enabled required")
			}
		}
	}
	var scope map[string]json.RawMessage
	_ = json.Unmarshal(raw["scope"], &scope)
	if string(scope["kind"]) == `"conversation"` && len(scope["projectId"]) == 0 {
		return m, fmt.Errorf("explicit conversation projectId required")
	}
	if err := requiredShape(raw["scope"], []string{"kind"}); err != nil {
		return m, err
	}
	for key, fields := range map[string][]string{
		"skills":       {"id", "name", "origin", "enabled", "version", "digest", "path"},
		"connectors":   {"id", "name", "origin", "endpoint", "enabled"},
		"repositories": {"sourceId", "repositoryId", "checkoutId", "kind", "displayName", "access"},
	} {
		var entries []map[string]json.RawMessage
		if err := json.Unmarshal(raw[key], &entries); err != nil {
			return m, err
		}
		for _, entry := range entries {
			for _, field := range fields {
				if len(entry[field]) == 0 || (field != "repositoryId" && string(entry[field]) == "null") {
					return m, fmt.Errorf("missing %s.%s", key, field)
				}
			}
			for _, field := range []string{"credentialRef", "branch", "commit", "remote", "worktreeName"} {
				if optional, ok := entry[field]; ok && string(optional) == "null" {
					return m, fmt.Errorf("null optional field %s", field)
				}
			}
			if origin, ok := entry["origin"]; ok {
				if err := requiredShape(origin, []string{"kind"}); err != nil {
					return m, err
				}
			}
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return m, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return m, fmt.Errorf("trailing JSON")
	}
	return m, m.Validate()
}
func (m Manifest) Validate() error {
	encoded, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(encoded) > MaxManifestBytes {
		return fmt.Errorf("manifest too large")
	}
	if m.SchemaVersion != 1 {
		return fmt.Errorf("unsupported environment schema")
	}
	if !validID(m.Owner.OrganizationID) || !validID(m.Owner.MemberID) {
		return fmt.Errorf("invalid owner")
	}
	if err := m.Scope.Validate(); err != nil {
		return err
	}
	if m.Revision < 1 || m.Revision > 9007199254740991 {
		return fmt.Errorf("invalid revision")
	}
	if m.Skills == nil || m.Connectors == nil || m.Repositories == nil || len(m.Skills) > 200 || len(m.Connectors) > 100 || len(m.Repositories) > 50 {
		return fmt.Errorf("invalid list limits")
	}
	seen := map[string]bool{}
	add := func(key string) bool {
		if seen[key] {
			return false
		}
		seen[key] = true
		return true
	}
	for _, s := range m.Skills {
		if !validID(s.ID) || !validID(s.Name) || !text(s.Version, 128) || !digestPattern.MatchString(s.Digest) || s.Path != "skills/"+s.ID+"/SKILL.md" || s.Origin.Validate() != nil || !contains(s.Origin, m.Scope) || !add("skill:"+s.ID) || !add("skill-name:"+s.Name) {
			return fmt.Errorf("invalid skill %q", s.ID)
		}
	}
	for _, c := range m.Connectors {
		if !validID(c.ID) || !validID(c.Name) || len(c.Name) > 24 || c.Origin.Validate() != nil || !contains(c.Origin, m.Scope) || ValidateEndpoint(c.Endpoint) != nil || (c.CredentialRef != "" && !validID(c.CredentialRef)) || !add("connector:"+c.ID) || !add("connector-name:"+c.Name) {
			return fmt.Errorf("invalid connector %q", c.ID)
		}
	}
	for _, r := range m.Repositories {
		if !validID(r.SourceID) || !validID(r.CheckoutID) || !text(r.DisplayName, 128) || (r.Access != "read" && r.Access != "write") || !add("source:"+r.SourceID) || !add("checkout:"+r.CheckoutID) {
			return fmt.Errorf("invalid repository")
		}
		if (r.Kind == "git" && (r.RepositoryID == nil || !validID(*r.RepositoryID))) || (r.Kind != "git" && (r.Kind != "directory" || r.RepositoryID != nil)) {
			return fmt.Errorf("invalid repository kind")
		}
		if (r.Branch != "" && !text(r.Branch, 256)) || (r.WorktreeName != "" && !text(r.WorktreeName, 256)) || (r.Commit != "" && !commitPattern.MatchString(r.Commit)) {
			return fmt.Errorf("invalid Git metadata")
		}
		if r.Remote != "" {
			u, err := url.Parse(r.Remote)
			if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
				return fmt.Errorf("unsafe Git remote")
			}
		}
	}
	return nil
}

func requiredShape(data json.RawMessage, fields []string) error {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	for _, field := range fields {
		if len(value[field]) == 0 {
			return fmt.Errorf("missing %s", field)
		}
	}
	if string(value["kind"]) == `"global"` && len(value) != 1 {
		return fmt.Errorf("global scope cannot name project/conversation")
	}
	if string(value["kind"]) == `"project"` && len(value) != 2 {
		return fmt.Errorf("project scope requires only kind/projectId")
	}
	if string(value["kind"]) == `"conversation"` && len(value) != 3 {
		return fmt.Errorf("conversation scope requires kind/projectId/conversationId")
	}
	if string(value["kind"]) == `"conversation"` && len(value["projectId"]) == 0 {
		return fmt.Errorf("conversation projectId must be explicit")
	}
	return nil
}
