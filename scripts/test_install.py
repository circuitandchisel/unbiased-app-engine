"""Offline installer regressions; never contacts GitHub or starts model requests."""
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest

INSTALLER = Path(__file__).resolve().parent.parent / "install.sh"


class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="engine install ")
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.downloads = self.base / "downloads"
        self.downloads.mkdir()
        self.tools = self.base / "tools"
        self.tools.mkdir()
        self.root = self.base / "runtime"
        self.bin = self.base / "bin"
        self.env = dict(os.environ, HOME=str(self.base),
                        PATH=str(self.tools) + os.pathsep + os.environ["PATH"],
                        UNBIASED_ENGINE_INSTALL_ROOT=str(self.root),
                        UNBIASED_ENGINE_BIN_DIR=str(self.bin),
                        FIXTURES=str(self.downloads))
        self.script("uname", '#!/bin/sh\n[ "$1" = -s ] && echo Linux || echo x86_64\n')
        self.script("curl", """#!/usr/bin/env python3
import os, pathlib, shutil, sys, urllib.parse
args = sys.argv[1:]
url = urllib.parse.urlparse(args[-1])
if url.hostname == "api.github.com":
    page = urllib.parse.parse_qs(url.query)["page"][0]
    name = "releases-" + page + ".json"
else:
    name = url.path.rsplit("/", 1)[-1]
source = pathlib.Path(os.environ["FIXTURES"]) / name
if "-o" in args:
    shutil.copyfile(source, args[args.index("-o") + 1])
else:
    sys.stdout.write(source.read_text())
""")
        self.bundle("0.1.0")

    def script(self, name, content):
        path = self.tools / name
        path.write_text(content)
        path.chmod(0o755)

    def bundle(self, version, bad_entry=None, corrupt_manifest=False, platform="linux-x64"):
        files = {
            "unbiased-app-engine": '#!/bin/sh\necho "unbiased-app-engine ' + version + '"\n',
            "pareto-app-server": '#!/bin/sh\necho "codex-app-server 0.1.7"\n',
            "VERSION": version + "\n", "APP_SERVER_VERSION": "0.1.7\n",
            "PLATFORM": platform + "\n", "engine.lock": "version = 0.1.7\n",
            "LICENSE": "license\n", "NOTICE": "notice\n",
            "THIRD_PARTY_NOTICES.md": "notices\n",
            "licenses/openai-codex/LICENSE": "license\n",
            "licenses/openai-codex/NOTICE": "notice\n",
        }
        files["FILES.sha256"] = "".join(
            hashlib.sha256(content.encode()).hexdigest() + "  ./" + name + "\n"
            for name, content in sorted(files.items()))
        if corrupt_manifest:
            files["NOTICE"] = "modified"
        name = "unbiased-app-engine-v" + version + "-linux-x64.tar.gz"
        archive = self.downloads / name
        with tarfile.open(archive, "w:gz") as tar:
            for filename, content in files.items():
                data = content.encode()
                entry = tarfile.TarInfo("./" + filename)
                entry.size = len(data)
                tar.addfile(entry, io.BytesIO(data))
            if bad_entry:
                tar.addfile(bad_entry)
        checksum = hashlib.sha256(archive.read_bytes()).hexdigest()
        (self.downloads / "SHA256SUMS").write_text(checksum + "  " + name + "\n")
        self.write_releases([self.release(version)])

    def release(self, version):
        return {"tag_name": "engine-v" + version, "published_at": "2026-11-10T00:00:00Z",
                "draft": False, "prerelease": False,
                "assets": [{"name": name, "state": "uploaded"} for name in (
                    "SHA256SUMS", "unbiased-app-engine-v" + version + "-linux-x64.tar.gz")]}

    def write_releases(self, releases, page=1):
        (self.downloads / ("releases-" + str(page) + ".json")).write_text(json.dumps(releases))

    def run_install(self, *args, success=True):
        result = subprocess.run(["bash", str(INSTALLER), *args], env=self.env,
                                capture_output=True, text=True)
        if success:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0)
        return result

    def active(self):
        return (self.root / "current" / "VERSION").read_text().strip()

    def test_install_upgrade_and_rollback(self):
        credential = self.base / ".unbiased" / "credentials.json"
        credential.parent.mkdir()
        credential.write_text("keep existing credentials")
        self.run_install()
        self.assertEqual(self.active(), "0.1.0")
        result = subprocess.run([str(self.bin / "unbiased-engine"), "--version"],
                                capture_output=True, text=True)
        self.assertEqual(result.stdout.strip(), "unbiased-app-engine 0.1.0")
        self.bundle("0.1.1")
        self.run_install("--version", "0.1.1")
        self.assertEqual(self.active(), "0.1.1")
        # Rollback is offline; no downloads are needed.
        shutil.rmtree(self.downloads)
        self.run_install("--rollback")
        self.assertEqual(self.active(), "0.1.0")
        self.assertEqual(credential.read_text(), "keep existing credentials")

    def test_checksum_failure_keeps_current(self):
        self.run_install()
        self.bundle("0.1.1")
        (self.downloads / "SHA256SUMS").write_text("0" * 64 + "  unbiased-app-engine-v0.1.1-linux-x64.tar.gz\n")
        self.run_install(success=False)
        self.assertEqual(self.active(), "0.1.0")

    def test_missing_checksum_rejected(self):
        (self.downloads / "SHA256SUMS").write_text("")
        self.run_install(success=False)
        self.assertFalse((self.root / "current").exists())

    def test_corrupt_manifest_rejected(self):
        self.bundle("0.1.0", corrupt_manifest=True)
        self.run_install(success=False)
        self.assertFalse((self.root / "current").exists())

    def test_archive_traversal_and_symlink_rejected(self):
        for name, kind in (("../escape", tarfile.REGTYPE), ("unexpected", tarfile.SYMTYPE)):
            with self.subTest(name=name):
                entry = tarfile.TarInfo(name)
                entry.type = kind
                entry.linkname = "/tmp/unrelated"
                self.bundle("0.1.0", bad_entry=entry)
                self.run_install(success=False)
                self.assertFalse((self.root / "current").exists())

    def test_platform_mismatch_rejected(self):
        self.bundle("0.1.0", platform="linux-arm64")
        self.run_install(success=False)

    def test_invalid_version_and_metadata_rejected(self):
        self.run_install("--version", "../other", success=False)
        self.write_releases({"message": "API error"})
        self.run_install(success=False)

    def test_filters_desktop_draft_prerelease_and_incomplete_releases(self):
        draft = dict(self.release("99.0.0"), draft=True)
        prerelease = dict(self.release("98.0.0"), prerelease=True)
        incomplete = dict(self.release("97.0.0"), assets=[])
        self.write_releases([{"tag_name": "v1.13.35"}, draft, prerelease, incomplete,
                             self.release("0.1.0")])
        self.run_install()
        self.assertEqual(self.active(), "0.1.0")

    def test_discovers_older_pages_and_sorts_versions_numerically(self):
        self.bundle("0.10.0")
        self.write_releases([{"tag_name": "v1.0.0"}] * 99 + [self.release("0.9.0")])
        self.write_releases([self.release("0.10.0")], page=2)
        self.run_install()
        self.assertEqual(self.active(), "0.10.0")

    def test_pinned_install_does_not_need_release_listing(self):
        (self.downloads / "releases-1.json").unlink()
        self.run_install("--version", "0.1.0")
        self.assertEqual(self.active(), "0.1.0")

    def test_no_usable_release_keeps_existing_install(self):
        self.run_install()
        self.write_releases([])
        self.run_install(success=False)
        self.assertEqual(self.active(), "0.1.0")

    def test_existing_unmanaged_command_preserved(self):
        self.bin.mkdir()
        launcher = self.bin / "unbiased-engine"
        launcher.write_text("my own command")
        self.run_install(success=False)
        self.assertEqual(launcher.read_text(), "my own command")

    def test_idempotent_install_verifies_existing_files(self):
        self.run_install()
        self.run_install()
        (self.root / "current" / "NOTICE").write_text("corrupted")
        self.run_install(success=False)

    def test_unsupported_os_and_lock(self):
        self.script("uname", "#!/bin/sh\necho Darwin\n")
        self.run_install(success=False)
        self.script("uname", '#!/bin/sh\n[ "$1" = -s ] && echo Linux || echo x86_64\n')
        self.root.mkdir()
        (self.root / ".install-lock").mkdir()
        self.run_install(success=False)


if __name__ == "__main__":
    unittest.main()
