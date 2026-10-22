GO      ?= go
VERSION ?= dev
USER_ID ?=
TEST_DATABASE_URL ?=

LDFLAGS := -X github.com/7310C7310C/yixianqian/internal/platform.Version=$(VERSION)

.PHONY: all tidy generate fmt fmt-check vet test test-integration test-migration build run seed token ci

all: ci

tidy:
	$(GO) mod tidy

generate:
	$(GO) generate ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)

vet:
	$(GO) vet ./...

test:
	$(GO) test -p 1 ./...

test-integration:
	@test -n "$(TEST_DATABASE_URL)" || (echo "TEST_DATABASE_URL is required"; exit 1)
	TEST_DATABASE_URL=$(TEST_DATABASE_URL) $(GO) test ./internal/db -run Integration -v

test-migration:
	python3 -m unittest discover -s tools -p 'test_*.py' -v

build:
	mkdir -p bin
	$(GO) build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o bin/server ./cmd/server
	$(GO) build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o bin/seed ./cmd/seed
	$(GO) build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o bin/token ./cmd/token
	$(GO) build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o bin/facebackfill ./cmd/facebackfill

run:
	$(GO) run ./cmd/server

seed:
	$(GO) run ./cmd/seed

token:
	@test -n "$(USER_ID)" || (echo "usage: make token USER_ID=U-0006"; exit 1)
	$(GO) run ./cmd/token -legacy-user-id=$(USER_ID)

ci: fmt-check vet test test-migration build
