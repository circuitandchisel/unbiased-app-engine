# Required Relay integration (not implemented endpoints)

Owner is platform organization.id + platform user.id. workload.id remains session provenance, not member identity. Relay must authorize every operation, reconnect and artifact access; it cannot trust desktop owner fields, a local provenance flag or an S3 prefix.

Existing inspected API:

- POST /api/v2/threads accepts workspaceId/access/parentId, not projectId or environment refs.
- POST /api/v2/threads/:id/turns accepts text and an Idempotency-Key.
- POST /api/v2/threads/:id/turns/:turn/stop.
- POST /api/v2/threads/:id/turns/:turn/approvals/:approval/decision accepts approve/deny.
- GET /api/v2/threads/:id and GET .../events with after or Last-Event-ID.

Those routes do not provide desktop history import or an environment transaction. The desktop currently instantiates ConversationRouter with no RemoteTransport. No host URL or feature flag turns this into a working integration.

Required additions, proposed names:

1. GET /api/v2/capabilities returns supported handoff/environment schema versions, thread configuration/reload behavior and remote capability inventory. Desktop computer control and local Chrome sessions default unavailable. Cloud browser/desktop tools require a separately advertised remote equivalent.
2. POST /api/v2/handoffs with idempotency/attempt ID; server-validated sign-in eligibility; local conversation ID; projectId or null; stable repository/source/checkout identities; per-repository access (never inferred from inclusion); branch/commit/sanitized origin reference/logical worktree name; all environment scope/revision/digest references; and a separately approved conversation checkpoint reference. No uncommitted files, credentials or raw engine-home snapshot. Preserve raw rollout/compaction records through the eventual history import.
3. GET /api/v2/handoffs/:attempt reports preparing/committed/aborted and remote conversation ID. POST .../commit atomically publishes the imported conversation/binding only once, using the frozen source revision. POST .../abort must prove no remote execution can occur. Network loss leaves desktop paused until this authoritative status is known. Repeating prepare/commit cannot create duplicate conversations or execute prompts twice.
4. GET/PUT /api/v2/environments/:scope/:id, with expectedRevision (If-Match) and immutable revision/digest result. Separate global/project/conversation documents scoped by authenticated owner. Reject stale writes (409), unsupported schema (422), invalid/secret-bearing content (422), missing credentials (a distinct availability state), unauthorized owner/project/repository (403), unavailable runtime (503). Deletions are durable disabled tombstones. Store edits at successful write time. A manifest reference must never implicitly enable a connector/tool or enlarge sandbox access.
5. Thread/turn binding API applies a resolved vector of global/project/conversation revisions before a new turn. Running turns retain their snapshot. One member runtime serves multiple threads. Updating one thread must not restart or replace an engine serving unrelated work. Resume must preserve history, dynamic tools, MCP selection, approvals and stable project membership, or reject the operation with a capability error.
6. Extend existing text turns for typed attachments/input and environment revision vector; preserve command idempotency keys across retries and restart. Return the same turn identity for duplicate submissions. Extend remote approval mapping without acceptForSession semantics for a one-time approval.
7. Existing ordered SSE events need a documented contiguous per-conversation cursor or explicit snapshot/reset protocol. Include cloud thread/turn IDs, item lifecycle/deltas, pending approvals, binding/environment changes and checkpoint outcomes. Revalidate owner/sign-in session on reconnect. Desktop event sinks must deduplicate by conversation/cursor, recover gaps from an authoritative snapshot, and persist cursor with durable presentation state where needed.

RemoteTransport in src/main/cloud-binding.ts is the injectable boundary for request, decide, environment and events. The future adapter must map local/remote IDs, filter unavailable desktop tools, handle server status/error mapping and snapshot recovery. Its test adapters are not a live service.

Credential references intentionally have no resolver in this change. Referenced connectors stay unavailable/sign-in required until a separate authenticated credential design exists. No archive or inference endpoint is called by the added tests. Infrastructure allocation, permission mounts, persistent cloud settings storage and web/mobile consumption are outside these two repository changes.
