.PHONY: build test release-local bullseye

build:
	cd go && go build -ldflags="-s -w -X main.version=dev" -o ../bin/sawmill ./cmd/sawmill

# Mirror CI's exact invocation (.github/workflows/go.yml) so failures
# surface locally before a push, not on the PR.
test:
	cd go && go test ./... -count=1 -race

release-local:
	goreleaser release --snapshot --clean

bullseye:
	@cd go && go build ./... && echo "✅ build"
	@cd go && go test ./... -count=1 -race 2>&1 | tail -1 && echo "✅ tests"
	@test -z "$$(git status --porcelain)" && echo "✅ clean" || \
	 (echo "❌ dirty tree"; git status --short; exit 1)
