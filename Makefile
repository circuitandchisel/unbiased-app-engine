# unbiased-app-engine build targets.
#
# `make bundle` is the contract with unbiased-app: it produces dist/bundle/,
# a self-contained directory holding the supervisor plus the pinned engine.
# The desktop app's build copies that directory into its package and spawns
# unbiased-app-engine from it — nothing else crosses the repo boundary.

.PHONY: build test fetch bundle conformance clean

build:
	go build -o bin/unbiased-app-engine ./cmd/unbiased-app-engine

test:
	go vet ./...
	go test ./...

# Engine binaries only ever enter the tree through the engine.lock checksum
# gate in fetch-engine.sh. The binary depends on the lock (and the script),
# so bumping the pin re-fetches — a bare existence check would silently
# bundle the previous engine.
fetch: bin/pareto-app-server
bin/pareto-app-server: engine.lock scripts/fetch-engine.sh
	scripts/fetch-engine.sh
	touch bin/pareto-app-server

bundle: bin/pareto-app-server LICENSE NOTICE THIRD_PARTY_NOTICES.md third_party/openai-codex/NOTICE
	rm -rf dist/bundle
	mkdir -p dist/bundle/licenses/openai-codex
	go build -o dist/bundle/unbiased-app-engine ./cmd/unbiased-app-engine
	cp bin/pareto-app-server dist/bundle/
	cp LICENSE NOTICE THIRD_PARTY_NOTICES.md dist/bundle/
	cp LICENSE dist/bundle/licenses/openai-codex/LICENSE
	cp third_party/openai-codex/NOTICE dist/bundle/licenses/openai-codex/NOTICE
	@echo "bundle ready: dist/bundle/ ($$(du -sh dist/bundle | cut -f1))"

# Live suite: spends a handful of small Pareto turns through the production
# gateway. Needs `unbiased login` (or UNBIASED_API_KEY).
conformance:
	conformance/run.sh

clean:
	rm -rf bin dist
