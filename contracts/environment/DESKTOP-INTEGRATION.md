# Portable environments and cloud continuation foundations

Status: local foundations and a feature-gated transport boundary. This change does not upload files, create cloud threads, deploy infrastructure, or provide working cloud continuation. Continue in cloud is disabled. There is no Relay adapter in this release.

## Verified integration context

This change was reviewed on desktop main e9bbf71 (1.13.22) and wrapper main cfa896b, whose engine.lock pins app-server 0.1.4. Skill discovery and protocol restrictions were rechecked against the matching unbiased-app-server-v0.1.4 source tag. No newly downloaded binary or live-model conformance was exercised; the adjacent Rust working tree is not the runtime contract. Relay authenticates organization.id as organizationId and user.id as memberId, using workload.id as its sessionId. The desktop now retains user.id from whoami; workload identity is never substituted for member identity.

Relay currently exposes workspace-based thread creation, text turns, cancellation, approval decisions and cursor-based SSE. It does not expose stable project environments, environment CAS, or an atomic import/freeze/commit desktop handoff. Existing archives cover conversation workspaces/rollouts, not persistent member-global settings. No cloud or Rust code was changed.

Pinned source inspection found a mismatch with older desktop comments: discovery is not simply “only <cwd>/.codex/skills.” Tag unbiased-app-server-v0.1.4 reads project config-layer skills, user CODEX_HOME/skills and HOME/.agents/skills, system roots, plugins, and repository .agents/skills along its discovery path. extraRoots is a shared SkillsHostService RwLock, and every extra root becomes SkillScope::User. SkillsConfig provides selectors/enablement, not an isolated project root. ThreadResumeParams and ThreadForkParams do not declare dynamicTools or experimentalRawEvents. Passing unknown fields is not evidence that dynamic tools survive a reload.

## Stable local identity

projects.json keeps its existing array representation and presentation fields, adding project id and sources. Every attachment has source id, repository id (null for non-Git directories), checkout id, kind, and a local path binding. These IDs are opaque, generated once and atomically persisted during migration. Rename, primary change and folder reorder preserve IDs. Registering the same remote twice does not automatically grant shared repository identity or access. A directory initialized as Git later receives repository identity without changing its attachment/checkout identity.

conversation-projects.json associates engine thread IDs with project IDs or explicit null. New side chats and registered subagents inherit project membership. Restored side-chat recovery records rebuild settings ancestry on restart. Settings lineage is separate from approval/grant lineage: this change does not copy one-time grants to side chats. Worktrees retain their logical names and receive checkout IDs separate from repository/source IDs. Existing worktrees gain an ID when preparing metadata.

Legacy path inference considers all matching projects, including ancestor matches and known worktree parent bindings. Multiple matches produce an ambiguous record and stay in Recents. The existing Move to project flow resolves ordinary cases; projects:resolve-membership accepts an explicit project ID or null, including projects sharing a source folder. The portable settings panel includes a project-ID selector for deliberate resolution, including shared-folder projects. Ambiguity metadata is also returned on sidebar threads. It is not silently resolved by iteration order.

Before removing a project or detaching a source folder, active and archived paginated legacy threads are assigned membership using the old metadata. Rename/reorder/primary edits remain local and do not require an engine connection. Source relocation does not infer that a newly attached path is the same checkout.

## Environment manifest v1

TypeScript: src/main/environment.ts. Go: internal/environment/manifest.go in unbiased-app-engine. Identical accepted/rejected JSON fixtures live in contracts/environment/fixtures in both repositories. Both components run these fixtures; review/copy fixture changes together. No raw TOML, credentials file, raw engine home, absolute host paths, or approval state is an environment.

Fields:

- schemaVersion: 1; owner: organizationId/memberId; scope: global, project(projectId), or conversation(conversationId, projectId or explicit null).
- revision: positive safe integer for one authoritative scope document. Revisions are independent across scopes.
- skills: stable id/name, origin scope, enabled state, version, SHA-256, portable skills/<id>/SKILL.md path.
- connectors: stable id/name, origin scope, non-secret endpoint, enabled state and optional opaque credentialRef. This is a definition, not a connection or permission.
- repositories: sourceId/repositoryId/checkoutId, kind, display name, explicit read/write access and optional branch/commit/remote/worktree name. Including a repository never grants filesystem or Git access.
- runtime mappings are a separate local input: runtimeRoot, artifactRoot, engineHome and checkoutId-to-absolute-path mappings. They are not uploaded as portable identity.

Limits: 256 KiB per manifest; 200 skills; 100 connectors; 50 repositories; bounded identifiers/text; unknown fields rejected. Skills have canonical ID-matching relative paths. Duplicate IDs/names and foreign origins are rejected.

Precedence is global -> matching project -> matching root conversation. Overrides replace by stable ID, including enabled:false tombstones. Resolution checks owner, project, conversation and layer order. Repository access comes from the narrowest supplied layer rather than unioning broader access. Side chats/subagents resolve settings through the root; no conversation override is promoted into project/global settings. Global means one organization/user pair, never all organizations of a signed-in person.

## Persistence and activation are separate

EnvironmentStore writes one owner/scope document using an exclusive writer lock, reload, expected revision check, fsync of the staged file and atomic rename. First write expects 0 and publishes 1; later writes require exactly current+1. Stale updates fail with REVISION_CONFLICT. Removed definitions must remain disabled tombstones. Local artifact content is written first; only the successful settings CAS makes it authoritative. A failed CAS can leave an inert content-addressed artifact. No write waits for turn completion. A process crash can leave a .lock file; verify no writer is running before manually recovering that lock. Corrupt files fail rather than becoming an empty store.

The Skills panel has explicit portable account/project/conversation targets and origin labels for inherited skills. Legacy installation remains folder/device scoped and is labelled accordingly; it is not automatically migrated. Bundled skills retain their existing read-only behavior. Portable v1 installation accepts one reviewed Markdown file <=64 KiB, rejects symlinks/directories and does not recursively ingest assets/scripts. These checks and digests do NOT prove that Markdown contains no secrets. There is no automatic upload. Any future upload must require an explicit reviewed artifact boundary; arbitrary skill/script archives are not certified secret-free by redaction.

Portable settings are persisted now, but scoped skill activation is explicitly unavailable. Enabling saved portable skills prevents a subsequent local turn from silently ignoring them; disable them to continue with existing local skills. Global account skills also stay out of the legacy device-global extra roots, because that would leak across organizations.

A turn captures its environment at its start; later edits remain persisted for the next boundary. Connector availability never expands the existing MCP selection. Current upstream app-wide connected catalogue defaults and manual per-conversation selections are preserved; scoped disablement checks both. A newly inherited connector disablement that conflicts with an already loaded selection blocks the next turn with ENGINE_ENVIRONMENT_RELOAD_REQUIRED. This release does not claim live engine reload. Existing local MCP-switch handling remains unchanged. Scoped/global mutations affecting a cloud-bound or unresolved handoff are rejected until a remote CAS adapter can handle them; no local-only success is reported.

Compatible activation strategy still to implement/verify: immutable content-addressed global/project/conversation artifact stores with a thread-specific skill-root/config view, installed before the next turn, not shared extraRoots and not a second runtime per conversation. Rust/Relay must provide thread-scoped roots or a verified isolated config-layer materializer, preserve dynamic tools on unload/resume, and filter connector allowlists/disabled plugins per thread. Do not copy scoped skills into shared repository folders: conversation-specific overrides could then affect another thread using the same checkout.

## Connector export boundary

exportConnector accepts a narrow metadata projection. command, args, environment values, headers, inline config/scripts, OAuth client secrets and all unknown fields are rejected, rather than copied/redacted. Local auth configuration requires an explicitly registered portable credentialRef; arbitrary argument/env strings are never assumed non-secret. Existing local connector functionality is unchanged.

v1 portable endpoints are canonical lowercase HTTPS DNS endpoints with /, /mcp or /sse only. The original spelling is validated without URL normalization, rejecting dot segments, empty userinfo, default ports and hidden path content: no URL userinfo, query, fragment, explicit port, literal IP or localhost. This is structural validation, not DNS/SSRF protection or proof a hostname is public; the future runtime network policy/catalogue must authorize destinations. Connector names respect the wrapper's 24-character limit. Credential references are opaque identifiers only, with no vault resolution. A referenced connector is omitted from active wrapper configuration and reports sign-in required. Anonymous definitions in explicit startup are installed disabled, awaiting a conversation allowlist.

## Wrapper explicit startup

No manifest flags: existing ResolveKey, home, MCP and inherited environment behavior remains intact.

With --environment-manifest /absolute/manifest.json --runtime-paths /absolute/paths.json: both files are mandatory, bounded regular files with no symlink components. A developer HOME is not required in this mode. The key must be explicitly supplied as UNBIASED_API_KEY; ~/.unbiased credentials and mcp-servers.json are not read. Provider URL is the production default, not an inherited local override. Arbitrary engine arguments are refused. All manifest/path/artifact/digest/config validation precedes generated output. Engine home must be a new directory inside the dedicated runtime root; existing runtime state is never replaced. Skills, the manifest and the complete generated configuration are staged into a sibling directory and published together into the new home only after setup succeeds; a failed setup leaves the destination absent and retryable. User-authored environment/artifact inputs remain separate from generated home/config. Reconstruct replacement runtimes with a new home/generation, not by overwriting an active home.

This shared-member bootstrap currently accepts global scope only. Project/conversation activation fails explicitly rather than changing shared settings for other threads. That is a blocker, not a claim of completed scoped execution. Enabled non-global skill/connector origins are also refused. Exact repository path mappings must stay inside the runtime root; mappings do not mount repositories or expand the sandbox. The child receives only HOME, CODEX_HOME, fixed PATH/LANG, the explicit Unbiased key and the request-size limit. Its cwd is runtimeRoot. No macOS/local executable connector is exported; only remote HTTPS definitions are representable.

The runtime root must be private and dedicated. Symlink checks are not protection against an adversarial same-UID process concurrently replacing trusted directories. This wrapper is NOT added to cloud deployment: Relay currently starts Rust directly and must implement equivalent validated materialization from this contract.

## Cloud boundary

CloudBindings persists local/preparing_cloud/cloud/preparation_failed, attempt identity, owner/project, remote conversation identity and durable event cursor. Preparing/failed states pause execution; a timeout is not permission to run locally. Retry requires confirming the prior server preparation aborted. cloud cannot transition back to local. Owner mismatch, corrupt binding or missing adapter fails closed. A pre-existing cloud binding is respected on restart even with no adapter. ConversationRouter routes thread RPCs, approval decisions, environment mutations and event reconnection through its injected remote port. No concrete remote port is configured in the app.

Preparation must check root, side and subagent turns/approvals are quiescent before server import. The store's active guard is a primitive, not that full orchestration. Event replay starts from persisted cursor, ignores already applied cursors, rejects gaps and serializes consumers per store instance. The event sink must be idempotent by cloud conversation/cursor: a crash after applying a presentation event but before persisting its cursor can replay it. Multiple app processes must not consume the same binding simultaneously. Durable command idempotency and stream snapshot recovery remain Relay adapter work.

The Account page reports readiness. Continue in cloud stays disabled, even if a local flag is changed, because the APIs/adapter are absent. Git readiness checks run read-only across EVERY attached repository and the active worktree binding. They reject missing Git/upstream, detached HEAD, dirty/untracked files and commits differing from cached origin upstream. They never init, commit, push, fetch, publish or upload. Local refs cannot prove current remote reachability: Relay must verify the exact commit on the approved origin ref. The UI asks the user to commit/push and refresh; a separate explicit approval would be required for an automated workflow.

Successful identity checks are cached for the exact key during one app session; sign-out or a changed key invalidates them, including in-flight responses. Cloud authorization is still the Relay server's responsibility.

Eligibility requires recorded platform-device provenance bound to the exact key fingerprint plus organization/user identity returned by whoami. Pasted/environment/file API keys do not gain eligibility from storage location. Local provenance is UI gating only, never authorization. Relay must independently validate identity and an approved sign-in claim. No local computer/browser-session capability or one-time approval is transferable to cloud. No WorkOS Vault, Secrets Manager, credential broker, S3 archive service or infrastructure is implemented.

See RELAY-INTEGRATION.md for the required remote contract and unresolved work.

## Review validation

Desktop commands (run in an isolated checkout of latest main with the reviewed changes):

```
npm ci --ignore-scripts
npm run typecheck
npm run build
node --import tsx --test src/main/project-identity.test.ts src/main/environment.test.ts src/main/cloud-binding.test.ts src/main/scoped-identity.test.ts
npm test
```

Typecheck/build and all 21 focused foundation tests passed. The standalone full suite passed with two optional sidecar tests skipped; counts are recorded in the PR. The checkout does not have the optional sibling connector catalogue. Explicitly rerunning the unchanged published-catalogue test with UNBIASED_CONNECTORS_DIR pointing at the existing sibling reproduces its old failure: NOW=2026-08-31 predates publishedAt=2026-09-22T22:53:09.841Z. This unrelated test/fixture was not changed. GUI behavior was not interactively exercised.

Wrapper commands:

```
go vet ./...
go test ./...
go test -race ./...
```

All passed. Tests use a fake executable and temporary directories, including local/default compatibility, no developer HOME or local-config fallback in explicit mode, atomic publication/retry of generated homes, path separation, shared contract fixtures and environment isolation. Fixture files were compared byte-for-byte. No added test calls a real model, publishes repositories, uploads user data or modifies AWS. No live cloud handoff or deployment was performed.
