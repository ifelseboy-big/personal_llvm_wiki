.PHONY: build test test-race release-build clean

build:
	CGO_ENABLED=1 CC=clang CXX=clang++ go build -tags "fts5 sqlite_omit_load_extension" -trimpath -ldflags "-s -w" -o llm-wiki ./cmd/llm-wiki

test:
	CGO_ENABLED=1 CC=clang CXX=clang++ go test -tags "fts5 sqlite_omit_load_extension" ./...

test-race:
	CGO_ENABLED=1 CC=clang CXX=clang++ go test -race -tags "fts5 sqlite_omit_load_extension" ./...

release-build:
	mkdir -p dist
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 CC=clang CXX=clang++ go build -tags "fts5 sqlite_omit_load_extension" -trimpath -o dist/llm-wiki_darwin_arm64 ./cmd/llm-wiki

clean:
	go clean
