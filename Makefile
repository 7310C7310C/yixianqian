GO      ?= go
VERSION ?= dev
USER_ID ?=
TEST_DATABASE_URL ?=
BASE_URL ?=
SESSION_COOKIE ?=
ACTIVITY_ID ?= 1
DOCS_BASE ?= origin/main

LDFLAGS := -X github.com/7310C7310C/yixianqian/internal/platform.Version=$(VERSION)

.PHONY: all tidy generate fmt fmt-check vet test test-race test-integration test-migration \
        test-broadcast-ui build run seed token docs-gen docs-check docs-check-stale prepush ci \
        static-gzip static-gzip-check

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
	$(GO) test -p 1 -count=1 ./...

test-race:
	@PKGS="$$( $(GO) list ./... | grep -v '/internal/db$$' | grep -v '/internal/httpserver$$' )"; \
	$(GO) test -race -p 1 -count=1 $$PKGS

# -run Integration 只跑 TestIntegration*；完整 db/httpserver 套件由 CI integration job 跑。
test-integration:
	@test -n "$(TEST_DATABASE_URL)" || (echo "TEST_DATABASE_URL is required"; exit 1)
	TEST_DATABASE_URL=$(TEST_DATABASE_URL) $(GO) test -p 1 -count=1 ./internal/db ./internal/httpserver -run Integration

test-migration:
	python3 -m unittest discover -s tools -p 'test_*.py' -v

docs-gen:
	python3 tools/gen_docs.py

docs-check:
	python3 tools/check_docs.py --strict

docs-check-stale:
	python3 tools/check_docs.py --strict --base=$(DOCS_BASE)

test-broadcast-ui:
	@test -n "$(BASE_URL)" || (echo "BASE_URL is required"; exit 1)
	@test -n "$(SESSION_COOKIE)" || (echo "SESSION_COOKIE is required"; exit 1)
	@BASE_URL="$(BASE_URL)" SESSION_COOKIE="$(SESSION_COOKIE)" ACTIVITY_ID="$(ACTIVITY_ID)" \
		python3 tools/browser/admin_broadcast_layout.py

# web/static 下文本资源的 .gz 预压缩变体（随 go:embed 发布，生产不需要额外步骤）。
# 改了 web/static 里任何 .js/.css/.html/.svg 后必须重跑；-check 由 make ci 执行。
static-gzip:
	$(GO) run ./tools/static_gzip

static-gzip-check:
	$(GO) run ./tools/static_gzip -check

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

# 快速本地门禁：push 前必跑；全量仍用 make ci（多 test/test-migration）
prepush: fmt-check static-gzip-check docs-check vet build

ci: fmt-check static-gzip-check docs-check vet test test-migration build
