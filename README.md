# unbiased-app-engine

A drop-in `codex app-server` that is locked to the Unbiased gateway (Pareto).

This repo wraps a **pinned, stock** codex-app-server binary — no forked
source — behind a small Go supervisor that makes it Pareto-only *by
construction*: on every start it regenerates a private `CODEX_HOME`
(`~/.unbiased/app-engine/home`) from an embedded template, loads the Unbiased
API key (`UNBIASED_API_KEY`, else `~/.unbiased/credentials.json` from
`unbiased login`), and execs the engine with stdio passed through. It never
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
| `scripts/gen-schema.sh` | Regenerates `schema/` from a `codex` CLI that exactly matches the lock. |
| `schema/` | Generated JSON Schema for the pinned protocol version — typed bindings match the engine by construction. |
| `conformance/` | Live protocol tests: stream, multi-turn, interrupt, command-approval round-trip. |

## Bumping the engine

Version drift is real (the upstream README and binary disagreed on enum
casing within five days of each other), so a bump is a deliberate, verified
event:

1. Edit `version` in `engine.lock`; update the four tarball checksums.
2. `scripts/fetch-engine.sh`
3. `scripts/gen-schema.sh` (requires the matching `codex` CLI) — review the schema diff for breaking protocol changes.
4. `conformance/run.sh` — all checks green.

## License and redistribution

This project is licensed under Apache-2.0. The desktop release redistributes
the pinned, unmodified Codex app-server binary. `make bundle` includes this
repository's license and OpenAI Codex's license and NOTICE alongside that
binary; see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## Design notes

- **Why wrap a binary instead of forking or linking the Rust crates?** The
  JSON-RPC protocol is the documented, stable boundary; the crate APIs are
  not. Pinning a release binary gives exact reproducibility with zero rebase
  burden. Codex is Apache-2.0; downloads are checksum-verified and bundled
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
