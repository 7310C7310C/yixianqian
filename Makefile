GO      ?= go
VERSION ?= dev
USER_ID ?=
TEST_DATABASE_URL ?=
BASE_URL ?=
SESSION_COOKIE ?=
ACTIVITY_ID ?= 1
DOCS_BASE ?= origin/main
PERF_PYTHON ?= python3
PERF_SOURCE ?= edge
PERF_N ?= 10
PERF_WARMUP ?= 3
PERF_COOKIE_ENV ?= YXQ_SESSION_COOKIE
PERF_COOKIE_FILE ?=
PERF_BENCH_OUT ?= /tmp/yxq-perf/bench.json
PERF_LCP_RUNS ?= 3
PERF_LCP_OUT ?= /tmp/yxq-perf/lcp.json

LDFLAGS := -X github.com/7310C7310C/yixianqian/internal/platform.Version=$(VERSION)

.PHONY: all tidy generate fmt fmt-check vet test test-race test-integration test-migration \
        test-broadcast-ui build run seed token docs-gen docs-check docs-check-stale prepush ci \
        static-gzip static-gzip-check spa-check spa-check-node \
        perf-budget perf-guard-test perf-bench perf-lcp

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

# internal/db 的单元测试（含 notification hub 并发回归 TestNotificationHubFanout*）
# 不需要数据库，可整包 -race（约 8s，TestIntegration* 无 TEST_DATABASE_URL 会 skip）；
# internal/httpserver 多数测试需要真实 Postgres，其 -race 由 CI integration job 覆盖。
test-race:
	@PKGS="$$( $(GO) list ./... | grep -v '/internal/httpserver$$' )"; \
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

# 开发期生成物（Vant 子集 / page chunk）门禁：纯 Go 校验 manifest sha/bytes，
# 部署不依赖 Node；Node 重建后用 spa-check-node 复核。
spa-check:
	$(GO) test ./internal/httpserver -run 'TestGeneratedBundleHashes|TestPageChunksMatchTemplates|TestVantSubsetCoversTemplate' -count=1
	python3 tools/spa_minify/check.py
	python3 tools/css_split/check.py

spa-check-node:
	cd tools/spa_precompile && npm run check
	cd tools/spa_vendor && npm run check
	cd tools/spa_minify && npm run check

build:
	mkdir -p bin
	$(GO) build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o bin/server ./cmd/server
	$(GO) build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o bin/seed ./cmd/seed
	$(GO) build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o bin/token ./cmd/token
	$(GO) build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o bin/facebackfill ./cmd/facebackfill
	$(GO) build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o bin/thumbwarm ./cmd/thumbwarm

run:
	$(GO) run ./cmd/server

seed:
	$(GO) run ./cmd/seed

token:
	@test -n "$(USER_ID)" || (echo "usage: make token USER_ID=U-0006"; exit 1)
	$(GO) run ./cmd/token -legacy-user-id=$(USER_ID)

# 性能护栏：静态 gzip 预算 + nginx 关键指令断言；不依赖 Go/Node/网络。
perf-budget:
	$(PERF_PYTHON) tools/perf/check_budget.py
	$(PERF_PYTHON) tools/perf/check_nginx_guard.py

# 护栏自测：合成超限/删除 nginx 指令必须变红。
perf-guard-test:
	$(PERF_PYTHON) -m unittest discover -s tools -p 'test_perf_guardrails.py' -v

# HTTP 延迟基准（手动；edge 在 server2、origin/app 在 server1 跑）。cookie 只从 env/文件读，不打印。
perf-bench:
	$(PERF_PYTHON) tools/perf/bench.py --source "$(PERF_SOURCE)" --n "$(PERF_N)" --warmup "$(PERF_WARMUP)" \
		$(if $(BASE_URL),--base "$(BASE_URL)",) \
		$(if $(PERF_COOKIE_FILE),--cookie-file "$(PERF_COOKIE_FILE)",) \
		$(if $(PERF_COOKIE_ENV),--cookie-env "$(PERF_COOKIE_ENV)",) \
		--out "$(PERF_BENCH_OUT)"

# 浏览器 LCP 慢机档（手动；需要 Playwright + BASE_URL/SESSION_COOKIE）。
perf-lcp:
	$(PERF_PYTHON) tools/perf/lcp.py --runs "$(PERF_LCP_RUNS)" --out "$(PERF_LCP_OUT)"

# 快速本地门禁：push 前必跑；全量仍用 make ci（多 test/test-migration）
asset-version-check:
	@if [ -n "$$ASSET_VERSION_BASE" ]; then \
		python3 tools/check_asset_version.py --base "$$ASSET_VERSION_BASE"; \
	else \
		python3 tools/check_asset_version.py; \
	fi

prepush: fmt-check static-gzip-check spa-check docs-check asset-version-check perf-budget perf-guard-test vet build

# perf-guard-test 由 test-migration 的 discover 统一执行，ci 不重复显式跑。
ci: fmt-check static-gzip-check spa-check docs-check asset-version-check perf-budget vet test test-migration build
