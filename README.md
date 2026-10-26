# unbiased-app-engine

A drop-in `codex app-server` that is locked to the Unbiased gateway (Pareto).

This repo wraps a **pinned Unbiased fork** of codex-app-server behind a small
Go supervisor that makes it Pareto-only *by
construction*: on every start it regenerates a private `CODEX_HOME`
(`~/.unbiased/app-engine/home`) from an embedded template, loads the Unbiased
credential (an explicit `UNBIASED_API_KEY`, else the sign-in token in
`~/.unbiased/credentials.json`), and execs the engine with stdio passed through. It never
reads `~/.codex`, so no user config can point it anywhere but the gateway.

Any client that speaks the [codex app-server JSON-RPC protocol][protocol] can
spawn `bin/unbiased-app-engine` instead of `codex app-server` and inherit
login, model, and gateway policy for free. This is the engine layer for an
Unbiased desktop app.

[protocol]: https://github.com/openai/codex/blob/main/codex-rs/app-server/README.md

## Quickstart

```bash
make fetch build                              # verified engine + supervisor in bin/
bin/unbiased-app-engine                       # speaks JSONL JSON-RPC on stdio
```

Smoke it end to end (live Pareto turns; needs `unbiased login` first):

```bash
make conformance
```

## Sign-in credentials

An explicit, nonempty `UNBIASED_API_KEY` keeps the existing API-key provider
configuration. Otherwise the wrapper uses the existing platform sign-in file
without exporting its token as `UNBIASED_API_KEY`. Both the legacy desktop
`{"apiKey":"..."}` and explicit `{"accessToken":"..."}` formats are supported.
The provider asks the wrapper's `auth-token` command for the current credential;
its cache refreshes after one second or through the existing 401 recovery path.
Replacing the file therefore does not require an engine restart. This does not
mint or refresh platform tokens; the signed-in host owns their lifecycle.

Use `--oauth-credentials /absolute/path/to/credentials.json` to select a different
file. Portable mode requires this flag when no explicit API key is supplied and
never falls back to a developer's local sign-in. Mount that file outside the
new engine home and update it atomically when rotating credentials. The token
is not written into generated TOML or passed in process arguments/environment.
The `auth-token --credentials-file PATH` subcommand writes the bearer token to
stdout for the provider; do not pipe it into logs.

Model calls use the app-server's existing primary bearer provider. OAuth-authenticated
Jev compaction additionally requires the companion app-server change that resolves
its credential through that same provider rather than reading the API-key environment.

## Bundling for unbiased-app

```bash
make bundle                                   # → dist/bundle/
```

`dist/bundle/` holds `unbiased-app-engine` + `pareto-app-server` side by
side — the complete brain as one droppable artifact. The desktop app's build
copies it into the app package (Tauri resources / Electron extraResources)
and spawns `unbiased-app-engine` from there; the supervisor finds the engine
next to its own executable, so no paths, config, or network are needed at
runtime. When shipping a signed macOS app, remember both binaries must be
re-signed with the app's Developer ID during packaging.

## Layout

| Path | Purpose |
|---|---|
| `engine.lock` | The pinned engine version + per-platform tarball SHA-256s. Single source of truth. |
| `scripts/fetch-engine.sh` | Downloads the pinned release binary, verifies the checksum, installs `bin/pareto-app-server`. |
| `cmd/unbiased-app-engine` | The supervisor: key resolution → home materialization → exec. |
| `internal/engine` | The testable pieces (config template, key/env handling). |
| `scripts/gen-schema.sh` | Regenerates `schema/` from the full fork source at the pinned release tag. |
| `schema/` | A generated protocol snapshot for client development; it is not used by the runtime. Regenerate after pinning a new release. |
| `conformance/` | Live protocol tests: stream, multi-turn, interrupt, command-approval round-trip. |

## Bumping the engine

Version drift is real (the upstream README and binary disagreed on enum
casing within five days of each other), so a bump is a deliberate, verified
event:

1. Edit `version` in `engine.lock`; update the four tarball checksums.
2. `scripts/fetch-engine.sh`
3. `scripts/gen-schema.sh` (requires the matching `unbiased-app-server` source checkout and Rust toolchain) — review the schema diff for breaking protocol changes.
4. `conformance/run.sh` — all checks green.

## License and redistribution

This project is licensed under Apache-2.0. The desktop release redistributes
the pinned Unbiased fork of Codex app-server. `make bundle` includes this
repository's license and OpenAI Codex's license and NOTICE alongside that
binary; see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## Design notes

- **Why wrap a binary instead of linking the Rust crates?** The JSON-RPC
  protocol is the documented boundary; the crate APIs are not. The forked
  engine is released separately, and pinning its binary keeps desktop builds
  reproducible. Codex is Apache-2.0; downloads are checksum-verified and bundled
  with the notices required for redistribution.
- **Why regenerate the home every start?** The 2026-08-12 spike against the
  Codex desktop app showed engines/hosts happily rewrite `config.toml`
  underneath you. Reclaiming the file at startup makes the provider config
  ours no matter what the previous run left behind.
- **Why exec instead of spawn?** One PID for the client to supervise; signals
  (app quit, Ctrl-C) reach the engine directly.
- **Clean tool surface**: because we are the client, none of the desktop
  app's `namespace`-type tools (`mcp__node_repl`, `codex_app`) ever enter a
  request — nothing needs stripping at the gateway. The config also pins
  `multi_agent = false` and `web_search = "disabled"` to match what the
  gateway/Pareto supports today.

## Explicit portable environments

The optional `--environment-manifest /absolute/manifest.json --runtime-paths
/absolute/paths.json` startup path validates a portable v1 environment before
materializing a new runtime home. It requires an explicit `UNBIASED_API_KEY`,
uses an allowlisted environment, and never falls back to local credentials/MCP
configuration. Existing startup without these flags is unchanged.

Only global bootstrap activation is supported: project/conversation activation
is rejected until a verified per-thread configuration adapter exists. Connector
definitions remain disabled until explicitly selected for a conversation;
opaque credential references have no resolver. This wrapper is not added to the
cloud deployment.

See [the contract notes](contracts/environment/DESKTOP-INTEGRATION.md),
[Relay requirements](contracts/environment/RELAY-INTEGRATION.md), and shared
[fixtures](contracts/environment/fixtures). No cloud continuation is deployed by
these interfaces.
