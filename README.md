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
scripts/fetch-engine.sh                       # download + verify the pinned engine
go build -o bin/unbiased-app-engine ./cmd/unbiased-app-engine
bin/unbiased-app-engine                       # speaks JSONL JSON-RPC on stdio
```

Smoke it end to end (live Pareto turns; needs `unbiased login` first):

```bash
conformance/run.sh
```

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

## Design notes

- **Why wrap a binary instead of forking or linking the Rust crates?** The
  JSON-RPC protocol is the documented, stable boundary; the crate APIs are
  not. Pinning a release binary gives exact reproducibility with zero rebase
  burden. codex is Apache-2.0 and the engine is downloaded at setup, not
  redistributed.
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
