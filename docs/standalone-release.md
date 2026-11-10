# Standalone engine releases

The wrapper and its pinned app-server ship together. Root `VERSION` versions
the combined distribution; `engine.lock` independently pins app-server and its
upstream archive checksums. Do not assume those version numbers are equal.

## First release prerequisites

- Merge the installer/release workflow and source changes through normal review.
- Make the existing release GitHub App's `RELEASE_APP_ID` and
  `RELEASE_APP_PRIVATE_KEY` secrets available to this repository.
- Confirm that App has contents write access to the public
  `circuitandchisel/unbiased-app-releases` repository, including main-branch
  updates to the engine release pointer.
- Obtain explicit authorization before pushing a release tag. A merged PR does
  not itself publish the installer.

The public command is reserved as:

```bash
curl -fsSL https://raw.githubusercontent.com/circuitandchisel/unbiased-app-engine/main/install.sh | bash
```

The installer is served directly from this public app-engine repository; it is
not copied to the desktop releases repository. It becomes usable only once the
installer is merged and the first workflow publishes both binaries and commits
`engine-latest.txt` to the public app-releases repository.
Do not announce this as live before testing that exact public command.

## Release steps

1. Update `VERSION` in a reviewed PR. Update `engine.lock` only when also
   changing app-server. Run `make test`, `python3 scripts/test_install.py`,
   and a native Linux bundle/install smoke.
2. Pull main after merge. Tag that exact main commit `engine-v<VERSION>`
   and push the tag when authorized.
3. The workflow runs tests and builds Linux x64 and ARM64 on native runners.
   Each archive includes both executables, the app-server pin, version/platform
   metadata, file checksums, and redistributed licenses.
4. The workflow creates a draft public release, verifies downloaded asset
   checksums, publishes it, then updates the engine release pointer. Engine
   releases always use `--latest=false` so the desktop
   installer's existing `releases/latest` lookup remains on a desktop release.
5. Confirm the public pointer, both assets, downloaded checksums, and exact
   public install command on a clean Linux user profile. Verify the desktop
   latest release is unchanged. A green build alone is not publication.

Published releases are immutable. A workflow rerun fails if its release already
exists rather than replacing artifacts. If publication or the pointer update
fails, inspect the existing release before repairing it; never automatically
delete or overwrite a published tag. A later main commit requires a new version.

## Installation and rollback

The installer verifies the release SHA256SUMS before extracting any executables.
It permits only the expected regular files, verifies their inner manifest and
native versions, and atomically switches `current` after validation.
Versions live under `~/.local/share/unbiased-engine/versions`, while a small
launcher in `~/.local/bin` executes the active wrapper beside its server.
An upgrade keeps the old version; `--rollback` validates it before switching.
These checks detect corrupted or mismatched downloads; checksums do not provide
independent protection against a compromised release publisher.

Optional absolute-path overrides: `UNBIASED_ENGINE_INSTALL_ROOT` and
`UNBIASED_ENGINE_BIN_DIR`. The installer refuses to overwrite an unmanaged
`unbiased-engine` command. It never edits credentials or shell startup files.
It does not migrate existing desktop-managed SSH profiles to this runtime;
desktop bootstrap integration is a separate change.
