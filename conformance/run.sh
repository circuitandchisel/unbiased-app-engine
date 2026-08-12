#!/usr/bin/env bash
# Build the supervisor, ensure the pinned engine is present, run the live
# conformance suite.
#
#   conformance/run.sh
#
# Needs a valid Unbiased key (env UNBIASED_API_KEY or `unbiased login`).
# Spends a handful of small Pareto turns against the production gateway.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [ ! -x "$ROOT/bin/pareto-app-server" ]; then
  "$ROOT/scripts/fetch-engine.sh"
fi

echo "building supervisor..."
(cd "$ROOT" && go build -o bin/unbiased-app-engine ./cmd/unbiased-app-engine)

echo "running conformance (live Pareto turns)..."
cd "$ROOT/conformance" && python3 test_conformance.py
