.PHONY: build test release-local release-tap bullseye

# The gate must depend only on this repo. A go.work anywhere up the directory
# tree that does not list go/ makes every target here fail with "directory
# prefix . does not contain modules listed in go.work" — which silently took
# out the pre-push hook while an unrelated sibling workspace existed. CI has
# no go.work, so this is a no-op there and a correctness fix locally.
export GOWORK = off

build:
	cd go && go build -ldflags="-s -w -X main.version=dev" -o ../bin/sawmill ./cmd/sawmill

# Mirror CI's exact invocation (.github/workflows/go.yml) so failures
# surface locally before a push, not on the PR.
test:
	cd go && go test ./... -count=1 -race

release-local:
	goreleaser release --snapshot --clean

# Publish the formula to the Homebrew tap for an existing release. Run after
# the release assets exist — tapper reads them to build the formula.
release-tap:
	./scripts/release-tap.sh $(TAG)

bullseye:
	@cd go && go build ./... && echo "✅ build"
	@cd go && go test ./... -count=1 -race 2>&1 | tail -1 && echo "✅ tests"
	@test -z "$$(git status --porcelain)" && echo "✅ clean" || \
	 (echo "❌ dirty tree"; git status --short; exit 1)
