.PHONY: build test test-race release-build clean

build:
	go build -trimpath -ldflags "-s -w" -o llm-wiki ./cmd/llm-wiki

test:
	go test ./...

test-race:
	go test -race ./...

release-build:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -o dist/llm-wiki_darwin_arm64 ./cmd/llm-wiki

clean:
	go clean
