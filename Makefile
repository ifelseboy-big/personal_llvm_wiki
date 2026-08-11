.PHONY: build install test test-race vet fmt-check schema-check agents-check mod-verify verify eval benchmark-index clean

GO_TAGS := fts5 sqlite_omit_load_extension
CC := $(if $(filter default,$(origin CC)),$(shell go env CC),$(CC))
CXX := $(if $(filter default,$(origin CXX)),$(shell go env CXX),$(CXX))
CGO_ENV := CGO_ENABLED=1 CC="$(CC)" CXX="$(CXX)"
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short=12 HEAD)
DATE ?= $(shell git show -s --format=%cI HEAD)
VERSION_LDFLAGS := -s -w -X llm-wiki/internal/app.Version=$(VERSION) -X llm-wiki/internal/app.Commit=$(COMMIT) -X llm-wiki/internal/app.Date=$(DATE)
BUILD_OUTPUT ?= llm-wiki
DEFAULT_INSTALL_DIR := $(shell go env GOBIN)
ifeq ($(strip $(DEFAULT_INSTALL_DIR)),)
DEFAULT_INSTALL_DIR := $(shell go env GOPATH)/bin
endif
INSTALL_DIR ?= $(DEFAULT_INSTALL_DIR)

build:
	$(CGO_ENV) go build -tags "$(GO_TAGS)" -trimpath -ldflags "$(VERSION_LDFLAGS)" -o "$(BUILD_OUTPUT)" ./cmd/llm-wiki

install:
	test -x "$(BUILD_OUTPUT)"
	mkdir -p "$(INSTALL_DIR)"
	install -m 0755 "$(BUILD_OUTPUT)" "$(INSTALL_DIR)/llm-wiki"

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

verify: fmt-check schema-check agents-check mod-verify vet test test-race
	git diff --check

eval:
	$(CGO_ENV) go test -count=1 -tags "$(GO_TAGS)" -run '^TestRetrievalQualityEvaluation$$' ./internal/index

benchmark-index:
	$(CGO_ENV) go test -tags "$(GO_TAGS)" -run '^$$' -bench '^BenchmarkSearchScale$$' -benchmem -benchtime=3x ./internal/index

clean:
	go clean
