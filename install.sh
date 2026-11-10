#!/usr/bin/env bash
# Standalone Linux runtime. Served directly from the public app-engine repo.
set -euo pipefail
repo="circuitandchisel/unbiased-app-releases"
fail() { printf 'Error: %s\n' "$*" >&2; exit 1; }
version=""
rollback=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    --version) [ "$#" -ge 2 ] || fail "--version needs X.Y.Z"; version="$2"; shift 2;;
    --rollback) rollback=1; shift;;
    --help) echo "Usage: bash install.sh [--version X.Y.Z | --rollback]"; exit 0;;
    *) fail "Unknown option: $1";;
  esac
done
[ "$rollback" = 0 ] || [ -z "$version" ] || fail "Choose --version or --rollback"
[ "$(uname -s)" = Linux ] || fail "The standalone runtime currently supports Linux only"
case "$(uname -m)" in
  x86_64|amd64) platform=linux-x64;;
  aarch64|arm64) platform=linux-arm64;;
  *) fail "Unsupported CPU architecture";;
esac
for dependency in curl python3; do
  command -v "$dependency" >/dev/null 2>&1 || fail "Install $dependency first"
done
root="${UNBIASED_ENGINE_INSTALL_ROOT:-$HOME/.local/share/unbiased-engine}"
bin="${UNBIASED_ENGINE_BIN_DIR:-$HOME/.local/bin}"
[[ "$root" = /* && "$bin" = /* ]] || fail "Install paths must be absolute"
mkdir -p "$root"
mkdir "$root/.install-lock" 2>/dev/null || fail "Another installer is running; if it was interrupted, remove $root/.install-lock"
trap 'rmdir "$root/.install-lock"' EXIT
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"; rmdir "$root/.install-lock"' EXIT
if [ "$rollback" = 0 ]; then
  if [ -z "$version" ]; then
    tag="$(curl -fsSL --retry 3 "https://raw.githubusercontent.com/$repo/main/engine-latest.txt")"
    [[ "$tag" =~ ^engine-v[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "Invalid engine release pointer"
    version="${tag#engine-v}"
  fi
  [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "Invalid version; expected X.Y.Z"
  tag="engine-v$version"
  asset="unbiased-app-engine-v$version-$platform.tar.gz"
  base="https://github.com/$repo/releases/download/$tag"
  curl -fsSL --retry 3 -o "$tmp/SHA256SUMS" "$base/SHA256SUMS"
  curl -fsSL --retry 3 -o "$tmp/$asset" "$base/$asset"
fi
python3 - "$root" "$bin" "$tmp" "$platform" "$version" "$rollback" <<'PY'
import hashlib
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import tarfile
import tempfile

root, bin_dir, tmp = (Path(p) for p in sys.argv[1:4])
platform, version, rollback = sys.argv[4:]
marker = "# Managed by Unbiased engine installer"
launcher = bin_dir / "unbiased-engine"
required = {
    "unbiased-app-engine", "pareto-app-server", "VERSION", "APP_SERVER_VERSION",
    "PLATFORM", "engine.lock", "LICENSE", "NOTICE", "THIRD_PARTY_NOTICES.md",
    "licenses/openai-codex/LICENSE", "licenses/openai-codex/NOTICE", "FILES.sha256",
}

def abort(message):
    raise SystemExit("Error: " + message)

def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as f:
        for block in iter(lambda: f.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()

def verify(directory, expected_version=None):
    if directory.is_symlink() or not directory.is_dir():
        abort("Invalid version directory")
    files = {str(p.relative_to(directory)) for p in directory.rglob("*") if p.is_file()}
    if files != required or any(p.is_symlink() for p in directory.rglob("*")):
        abort("Unexpected runtime files")
    checks = {}
    for line in (directory / "FILES.sha256").read_text().splitlines():
        match = re.fullmatch(r"([a-f0-9]{64})  \./(.+)", line)
        if not match or match[2] in checks:
            abort("Invalid runtime manifest")
        checks[match[2]] = match[1]
    if set(checks) != required - {"FILES.sha256"}:
        abort("Incomplete runtime manifest")
    for name, expected in checks.items():
        if digest(directory / name) != expected:
            abort("Runtime checksum mismatch: " + name)
    installed = (directory / "VERSION").read_text().strip()
    server = (directory / "APP_SERVER_VERSION").read_text().strip()
    if not re.fullmatch(r"\d+\.\d+\.\d+", installed) or not re.fullmatch(r"\d+\.\d+\.\d+", server):
        abort("Invalid runtime version")
    if expected_version and installed != expected_version:
        abort("Runtime version differs from requested version")
    if (directory / "PLATFORM").read_text().strip() != platform:
        abort("Runtime platform mismatch")
    for name, expected in (("unbiased-app-engine", "unbiased-app-engine " + installed),
                           ("pareto-app-server", "codex-app-server " + server)):
        result = subprocess.run([str(directory / name), "--version"], capture_output=True, text=True, timeout=20)
        if result.returncode or result.stdout.strip() != expected:
            abort("Runtime version check failed: " + name)
    return installed

def target(link):
    if not link.exists() and not link.is_symlink():
        return None
    if not link.is_symlink():
        abort("Refusing to replace unmanaged " + str(link))
    resolved = link.resolve()
    if resolved.parent != (root / "versions").resolve():
        abort("Invalid runtime link")
    return resolved

def set_link(name, destination):
    staged = root / (name + ".new")
    if staged.exists() or staged.is_symlink():
        abort("Unexpected installer temporary link")
    staged.symlink_to(destination)
    os.replace(staged, root / name)

if launcher.exists() or launcher.is_symlink():
    if launcher.is_symlink() or not launcher.is_file() or marker not in launcher.read_text():
        abort("Refusing to overwrite an unmanaged unbiased-engine command")
current = target(root / "current")
previous = target(root / "previous")
if rollback == "1":
    if previous is None:
        abort("No previous installation to restore")
    destination = previous
    version = verify(destination)
else:
    asset = "unbiased-app-engine-v" + version + "-" + platform + ".tar.gz"
    matches = []
    for line in (tmp / "SHA256SUMS").read_text().splitlines():
        match = re.fullmatch(r"([a-f0-9]{64})  (.+)", line)
        if match and match[2] == asset:
            matches.append(match[1])
    if len(matches) != 1 or digest(tmp / asset) != matches[0]:
        abort("Release checksum missing or mismatched")
    versions = root / "versions"
    versions.mkdir(exist_ok=True)
    destination = versions / ("engine-v" + version)
    if destination.exists() or destination.is_symlink():
        verify(destination, version)
    else:
        stage = Path(tempfile.mkdtemp(prefix=".install-", dir=versions))
        try:
            with tarfile.open(tmp / asset, "r:gz") as archive:
                seen = set()
                total = 0
                for member in archive:
                    name = member.name[2:] if member.name.startswith("./") else member.name
                    if member.isdir() and name in ("", ".", "licenses", "licenses/openai-codex"):
                        continue
                    if name not in required or not member.isfile() or name in seen:
                        abort("Unsafe or unexpected archive entry")
                    total += member.size
                    if total > 2 * 1024 * 1024 * 1024:
                        abort("Runtime archive is too large")
                    seen.add(name)
                    output = stage / name
                    output.parent.mkdir(parents=True, exist_ok=True)
                    with archive.extractfile(member) as source, output.open("wb") as out:
                        shutil.copyfileobj(source, out)
                    output.chmod(0o755 if name in ("unbiased-app-engine", "pareto-app-server") else 0o644)
            verify(stage, version)
            stage.rename(destination)
        finally:
            if stage.exists():
                shutil.rmtree(stage)
bin_dir.mkdir(parents=True, exist_ok=True)
fd, staged_name = tempfile.mkstemp(prefix=".unbiased-engine-", dir=bin_dir)
try:
    with os.fdopen(fd, "w") as f:
        f.write("#!/usr/bin/env bash\n" + marker + "\nexec " +
                shlex.quote(str(root / "current" / "unbiased-app-engine")) + ' "$@"\n')
    os.chmod(staged_name, 0o755)
    if current and current != destination:
        set_link("previous", current)
    set_link("current", destination)
    os.replace(staged_name, launcher)
finally:
    if os.path.exists(staged_name):
        os.unlink(staged_name)
print("Installed unbiased-app-engine " + version + " at " + str(launcher))
print("Authentication: set UNBIASED_API_KEY or use your existing remote Unbiased credentials.")
print("This is a JSON-RPC runtime for the desktop SSH connection, not an interactive chat CLI.")
PY
case ":$PATH:" in
  *":$bin:"*) ;;
  *) printf 'Add this directory to your PATH: %s\n' "$bin";;
esac
