#!/usr/bin/env bash
# Regenerate schema/ from the pinned engine version.
#
#   scripts/gen-schema.sh
#
# The standalone codex-app-server binary has no generate subcommand; schema
# generation lives in the full codex CLI. This script requires a `codex` on
# PATH whose version EXACTLY matches engine.lock — generated types must match
# the pinned engine by construction, not by hope. (Docs drift is real: the
# repo README documented `workspaceWrite` while the 0.147.0 binary wanted
# `read-only`.)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="$(sed -n 's/^version = //p' "$ROOT/engine.lock")"

if ! command -v codex >/dev/null 2>&1; then
  echo "error: no \`codex\` CLI on PATH (needed only for schema generation)" >&2
  exit 1
fi
actual="$(codex --version | awk '{print $2}')"
if [ "$actual" != "$VERSION" ]; then
  echo "error: codex CLI is $actual but engine.lock pins $VERSION" >&2
  echo "  install the matching CLI or bump engine.lock deliberately" >&2
  exit 1
fi

rm -rf "$ROOT/schema"
mkdir -p "$ROOT/schema"
codex app-server generate-json-schema --out "$ROOT/schema"
echo "regenerated schema/ from codex $actual"
