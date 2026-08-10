.PHONY: build test test-race vet fmt-check schema-check agents-check mod-verify goreleaser-check verify eval benchmark-index release-build release-snapshot release clean

GO_TAGS := fts5 sqlite_omit_load_extension
CGO_ENV := CGO_ENABLED=1 CC=clang CXX=clang++

build:
	$(CGO_ENV) go build -tags "$(GO_TAGS)" -trimpath -ldflags "-s -w" -o llm-wiki ./cmd/llm-wiki

test:
	$(CGO_ENV) go test -tags "$(GO_TAGS)" ./...

test-race:
	$(CGO_ENV) go test -race -tags "$(GO_TAGS)" ./...

vet:
	$(CGO_ENV) go vet -tags "$(GO_TAGS)" ./...

fmt-check:
	test -z "$$(gofmt -l .)"

schema-check:
	jq empty schemas/*.json

agents-check:
	$(CGO_ENV) go test -tags "$(GO_TAGS)" ./tests/architecture
	$(CGO_ENV) go test -tags "$(GO_TAGS)" ./internal/templates -run '^TestPersonalTemplateMatchesVersionedDesignBaseline$$'
	git diff --check

mod-verify:
	go mod verify

goreleaser-check:
	goreleaser check

verify: fmt-check schema-check agents-check mod-verify vet test test-race goreleaser-check
	git diff --check

eval:
	$(CGO_ENV) go test -count=1 -tags "$(GO_TAGS)" -run '^TestRetrievalQualityEvaluation$$' ./internal/index

benchmark-index:
	$(CGO_ENV) go test -tags "$(GO_TAGS)" -run '^$$' -bench '^BenchmarkSearchScale$$' -benchmem -benchtime=3x ./internal/index

release-build:
	mkdir -p dist
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 CC=clang CXX=clang++ go build -tags "$(GO_TAGS)" -trimpath -o dist/llm-wiki_darwin_arm64 ./cmd/llm-wiki

release-snapshot: goreleaser-check
	goreleaser release --snapshot --clean

release: goreleaser-check
	goreleaser release --clean

clean:
	go clean
