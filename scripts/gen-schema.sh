#!/usr/bin/env bash
# Regenerate schema/ from the pinned engine version.
#
#   scripts/gen-schema.sh
#
# The server-only release cannot generate schemas. Build the CLI from the
# matching fork source; an unrelated codex CLI may expose a different protocol.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="$(sed -n 's/^version = //p' "$ROOT/engine.lock")"
SOURCE="${UNBIASED_APP_SERVER_SOURCE:-$ROOT/../unbiased-app-server}"
TAG="unbiased-app-server-v$VERSION"

if [ ! -d "$SOURCE/codex-rs" ]; then
  echo "error: fork source missing at $SOURCE (set UNBIASED_APP_SERVER_SOURCE)" >&2
  exit 1
fi
if ! git -C "$SOURCE" rev-parse --verify "$TAG^{tree}" >/dev/null 2>&1; then
  echo "error: fetch $TAG in $SOURCE before generating schemas" >&2
  exit 1
fi
if [ "$(git -C "$SOURCE" rev-parse HEAD^{tree})" != "$(git -C "$SOURCE" rev-parse "$TAG^{tree}")" ]; then
  echo "error: $SOURCE must have the same source tree as $TAG" >&2
  exit 1
fi

rm -rf "$ROOT/schema"
mkdir -p "$ROOT/schema"
(cd "$SOURCE/codex-rs" && cargo run --locked --bin codex -- app-server generate-json-schema --out "$ROOT/schema")
echo "regenerated schema/ from $TAG"
