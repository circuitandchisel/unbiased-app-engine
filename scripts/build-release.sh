#!/usr/bin/env bash
# Build one self-contained, independently versioned Linux runtime.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
version="$(cat VERSION)"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "Invalid VERSION" >&2; exit 1; }
server_version="$(sed -n 's/^version = //p' engine.lock)"
[[ "$server_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "Invalid engine.lock version" >&2; exit 1; }
target="${1:-}"
if [ -z "$target" ]; then
  [ "$(uname -s)" = Linux ] || { echo "Pass linux-x64 or linux-arm64 to cross-build" >&2; exit 1; }
  case "$(uname -m)" in x86_64|amd64) target=linux-x64;; aarch64|arm64) target=linux-arm64;; *) exit 1;; esac
fi
case "$target" in
  linux-x64) goarch=amd64; rustarch=x86_64;;
  linux-arm64) goarch=arm64; rustarch=aarch64;;
  *) echo "Unsupported target: $target" >&2; exit 1;;
esac
asset="codex-app-server-$rustarch-unknown-linux-musl.tar.gz"
expected="$(awk -v name="$asset" '$1=="sha256" && $2==name && $3=="=" {print $4}' engine.lock)"
[[ "$expected" =~ ^[a-f0-9]{64}$ ]] || { echo "Missing pinned checksum: $asset" >&2; exit 1; }
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
curl -fsSL --retry 3 -o "$tmp/server.tar.gz" "https://github.com/circuitandchisel/unbiased-app-server/releases/download/unbiased-app-server-v$server_version/$asset"
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/server.tar.gz" | awk '{print $1}')"
else
  actual="$(shasum -a 256 "$tmp/server.tar.gz" | awk '{print $1}')"
fi
[ "$actual" = "$expected" ] || { echo "App-server checksum mismatch" >&2; exit 1; }
payload="$tmp/payload"
mkdir -p "$payload/licenses/openai-codex"
tar -xzf "$tmp/server.tar.gz" -C "$tmp" "codex-app-server-$rustarch-unknown-linux-musl"
mv "$tmp/codex-app-server-$rustarch-unknown-linux-musl" "$payload/pareto-app-server"
chmod 755 "$payload/pareto-app-server"
CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" go build -trimpath -ldflags "-X main.version=$version" -o "$payload/unbiased-app-engine" ./cmd/unbiased-app-engine
cp VERSION engine.lock LICENSE NOTICE THIRD_PARTY_NOTICES.md "$payload/"
cp LICENSE "$payload/licenses/openai-codex/LICENSE"
cp third_party/openai-codex/NOTICE "$payload/licenses/openai-codex/NOTICE"
printf '%s\n' "$server_version" > "$payload/APP_SERVER_VERSION"
printf '%s\n' "$target" > "$payload/PLATFORM"
(
  cd "$payload"
  if command -v sha256sum >/dev/null 2>&1; then
    find . -type f ! -name FILES.sha256 -exec sha256sum {} \; | LC_ALL=C sort > FILES.sha256
  else
    find . -type f ! -name FILES.sha256 -exec shasum -a 256 {} \; | LC_ALL=C sort > FILES.sha256
  fi
)
mkdir -p dist/release
archive="unbiased-app-engine-v$version-$target.tar.gz"
COPYFILE_DISABLE=1 tar -czf "dist/release/$archive" -C "$payload" .
if command -v sha256sum >/dev/null 2>&1; then
  (cd dist/release && sha256sum "$archive" > "$archive.sha256")
else
  (cd dist/release && shasum -a 256 "$archive" > "$archive.sha256")
fi
echo "Built dist/release/$archive"
