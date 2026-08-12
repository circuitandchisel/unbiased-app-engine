#!/usr/bin/env bash
# Fetch the pinned codex-app-server engine binary for this machine.
#
#   scripts/fetch-engine.sh
#
# Reads the version and expected checksum from engine.lock, downloads the
# release tarball from openai/codex, verifies the SHA-256, and installs the
# binary at bin/pareto-app-server. Refuses to install anything that does not
# match the lock — an engine we haven't conformance-tested must never run.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOCK="$ROOT/engine.lock"

VERSION="$(sed -n 's/^version = //p' "$LOCK")"
if [ -z "$VERSION" ]; then
  echo "error: no version in $LOCK" >&2; exit 1
fi

os="$(uname -s)"
arch="$(uname -m)"
case "$os" in
  Darwin) suffix=apple-darwin ;;
  Linux)  suffix=unknown-linux-musl ;;
  *) echo "error: unsupported OS: $os" >&2; exit 1 ;;
esac
case "$arch" in
  arm64|aarch64) target="aarch64-$suffix" ;;
  x86_64|amd64)  target="x86_64-$suffix" ;;
  *) echo "error: unsupported architecture: $arch" >&2; exit 1 ;;
esac

asset="codex-app-server-$target.tar.gz"
expected="$(sed -n "s/^sha256 $asset = //p" "$LOCK")"
if [ -z "$expected" ]; then
  echo "error: no checksum for $asset in $LOCK" >&2; exit 1
fi

url="https://github.com/openai/codex/releases/download/rust-v$VERSION/$asset"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "fetching engine v$VERSION ($target)..."
curl -fsSL --retry 3 --retry-delay 2 -o "$tmp/$asset" "$url"

if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/$asset" | awk '{print $1}')"
else
  actual="$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')"
fi
if [ "$expected" != "$actual" ]; then
  echo "error: checksum mismatch for $asset" >&2
  echo "  expected: $expected" >&2
  echo "  actual:   $actual" >&2
  exit 1
fi

tar -xzf "$tmp/$asset" -C "$tmp"
mkdir -p "$ROOT/bin"
# Same-directory temp + mv so a crash mid-copy never leaves a truncated binary.
staged="$(mktemp "$ROOT/bin/.engine.XXXXXX")"
cp "$tmp/codex-app-server-$target" "$staged"
chmod 0755 "$staged"
mv "$staged" "$ROOT/bin/pareto-app-server"

echo "installed bin/pareto-app-server ($("$ROOT/bin/pareto-app-server" --version))"
