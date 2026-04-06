.PHONY: build test release-local

build:
	cd go && go build -ldflags="-s -w -X main.version=dev" -o ../bin/sawmill ./cmd/sawmill

test:
	cd go && go test ./... -count=1

release-local:
	goreleaser release --snapshot --clean
