# Contributing

Issues and pull requests are welcome. Maintainers decide what enters the
release and retain control of labels, merges, signing, and publishing.

## Development setup

The engine requires Go 1.26.5 or newer. The pinned Codex app-server binary is
downloaded and checksum-verified by the build:

```bash
make fetch
make test
make bundle
```

`make conformance` performs live calls and requires an Unbiased API key. It
is not required for ordinary pull requests.

Do not include credentials, user data, local absolute paths, generated
binaries, or information copied from private systems.

Maintainers may apply the `ai-review` label after an initial review. Outside
contributors do not need to add or request that label.

By submitting a contribution, you agree that it may be distributed under
the repository's Apache-2.0 license. Report vulnerabilities through
[SECURITY.md](SECURITY.md), not a public issue.
