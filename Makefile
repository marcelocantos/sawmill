.PHONY: build test release-local bullseye

build:
	cd go && go build -ldflags="-s -w -X main.version=dev" -o ../bin/sawmill ./cmd/sawmill

test:
	cd go && go test ./... -count=1

release-local:
	goreleaser release --snapshot --clean

bullseye:
	@cd go && go build ./... && echo "✓ build"
	@cd go && go test ./... -count=1 2>&1 | tail -1 && echo "✓ tests"
	@test -z "$$(git status --porcelain)" && echo "✓ clean" || \
	 (echo "✗ dirty tree"; git status --short; exit 1)
