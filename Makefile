GO      ?= $(shell command -v go 2>/dev/null || echo /usr/local/go/bin/go)
GOFMT   ?= $(shell command -v gofmt 2>/dev/null || echo /usr/local/go/bin/gofmt)
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

.PHONY: all tidy generate fmt fmt-check vet test test-race test-race-full test-integration test-migration \
        test-broadcast-ui build run seed token docs-gen docs-check docs-check-stale prepush ci \
        static-gzip static-gzip-check spa-check spa-check-node \
        perf-budget perf-guard-test perf-bench perf-lcp \
        fast smoke-fast smoke-full smoke-viewer-arrows deploy-wait review-packet

all: ci

tidy:
	$(GO) mod tidy

generate:
	$(GO) generate ./...

fmt:
	$(GOFMT) -w $$(find . -name '*.go' -not -path './vendor/*')

fmt-check:
	@test -z "$$($(GOFMT) -l .)" || (echo "gofmt needed:"; $(GOFMT) -l .; exit 1)

vet:
	$(GO) vet ./...

test:
	$(GO) test -p 4 -count=1 ./...

# 兼容历史文档；真正的 race 覆盖在 CI。发版前用 test-race-full。
test-race:
	@echo "test-race: deprecated; CI 已覆盖全量 race；发版前用 make test-race-full"

test-race-full:
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

# ---- Agent fast path (2026-10-27) -----------------------------------------
# 每次 edit 后用；只跑受影响的检查，<30s。不是 CI 的替代品。
fast:
	./tools/agent/fast.sh

# 每个 batch 一次；默认 390x844 + 390x700 并行。
# 服务器上跑更快：BASE_URL=http://127.0.0.1:8093 SESSION_COOKIE_FILE=/path ...
smoke-fast:
	$(PERF_PYTHON) tools/agent/smoke_parallel.py --suite fast

# nightly / release：完整矩阵。
smoke-full:
	$(PERF_PYTHON) tools/agent/smoke_parallel.py --suite full

# 大图左右按键/循环（390 触摸 + 1280 鼠标）；需 BASE_URL + SESSION_COOKIE(_FILE)。
smoke-viewer-arrows:
	$(PERF_PYTHON) tools/browser/viewer_arrows_smoke.py

# 一个阻塞命令等 CI + autodeploy；不再用模型轮询。
deploy-wait:
	./tools/agent/deploy_wait.sh $(if $(SHA),$(SHA),)

review-packet:
	./tools/agent/review_packet.sh $(if $(BASE),$(BASE),origin/main)
