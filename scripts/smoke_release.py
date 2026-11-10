"""Install a real native release in an isolated user profile; no inference."""
import json
import os
from pathlib import Path
import select
import shutil
import subprocess
import sys
import tempfile

archive = Path(sys.argv[1]).resolve()
scripts = Path(__file__).resolve().parent
with tempfile.TemporaryDirectory(prefix="engine-release-smoke-") as temp:
    base = Path(temp)
    tools = base / "tools"
    tools.mkdir()
    curl = tools / "curl"
    curl.write_text("""#!/usr/bin/env python3
import os, pathlib, shutil, sys
args = sys.argv[1:]
name = args[-1].rsplit("/", 1)[-1]
source = pathlib.Path(os.environ["SMOKE_RELEASE_DIR"]) / name
shutil.copyfile(source, args[args.index("-o") + 1])
""")
    curl.chmod(0o755)
    downloads = base / "downloads"
    downloads.mkdir()
    shutil.copyfile(archive, downloads / archive.name)
    shutil.copyfile(str(archive) + ".sha256", downloads / "SHA256SUMS")
    version = archive.name.split("-v", 1)[1].split("-linux-", 1)[0]
    env = dict(os.environ, HOME=str(base), SMOKE_RELEASE_DIR=str(downloads),
               UNBIASED_ENGINE_INSTALL_ROOT=str(base / "runtime"),
               UNBIASED_ENGINE_BIN_DIR=str(base / "bin"),
               PATH=str(tools) + os.pathsep + os.environ["PATH"])
    subprocess.run(["bash", str(scripts.parent / "install.sh"), "--version", version],
                   env=env, check=True)
    launcher = base / "bin" / "unbiased-engine"
    env["UNBIASED_API_KEY"] = "offline-install-smoke-not-a-real-key"
    # Initialization exercises stdio transport without sending a model request.
    process = subprocess.Popen([str(launcher), "--home", str(base / "home")],
                               env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                               stderr=subprocess.DEVNULL, text=True)
    try:
        process.stdin.write(json.dumps({
            "id": 1, "method": "initialize",
            "params": {"clientInfo": {"name": "release-smoke", "version": "1.0.0"}}
        }) + "\n")
        process.stdin.flush()
        ready, _, _ = select.select([process.stdout], [], [], 20)
        if not ready:
            raise RuntimeError("No initialization response in 20 seconds")
        response = json.loads(process.stdout.readline())
        if response.get("id") != 1 or "result" not in response:
            raise RuntimeError("Unexpected initialization response: " + str(response))
        print("Real bundle installed; offline JSON-RPC initialization passed.")
    finally:
        process.terminate()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
