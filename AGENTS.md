# AGENTS.md — yixianqian

> 专案宪章（Source of Truth）：`docs/PROJECT_CHARTER.md`
> 用户/管理端验收清单：`docs/product-spec.md`
> 任何实作若与上述文件冲突，先改文件/加 ADR，不要先写代码。

## 专案定位
把「一线牵」改造成不依赖飞书的独立网页产品。Go 模组化单体、PostgreSQL、R2、server2 HK relay。
**用户端复用 v1 Vue 3 + Vant SPA；管理端使用 Go 服务端渲染。不做 POC；用户功能必须完整。**
目前 Go/Postgres/R2、auth/activation/media、管理端 SSR 已完成，不代表产品完成；
完成标准见 `docs/product-spec.md` 的 Must-Have。资料迁移来源是 testapp。

## 常用指令
```sh
make tidy        # go mod tidy
make test        # go test ./...
make vet         # go vet ./...
make build       # go build ./...
make run         # go run ./cmd/server
make seed        # go run ./cmd/seed（建立 U-0006 测试用户并印出启用 URL）
go run ./cmd/token -legacy-user-id U-0006
```

## 结构
- `cmd/`：server、seed、token 三个入口。
- `internal/config`：唯一环境变数入口；不得在别处直接读 `os.Getenv`。
- `internal/db`：pgxpool + SQL；交易逻辑（启用）放这里，不放 handler。
- `internal/auth`：Argon2id、session/activation token 杂凑、CSRF、in-memory rate limit。
- `internal/media`：图片验证/缩图与 S3(R2) store 界面。
- `internal/httpserver`：路由、middleware、handler、管理端模板渲染、`/legacy/api/*` 适配。
- `web/`：`webassets` 套件 embed 模板与静态档；`web/static/legacy/` 放 v1 用户端 SPA 资产与兼容适配。
- `migrations/`：embed 的 SQL migration，档名 `NNNN_name.sql`，只增不改。
- `docs/adr/`：设计决策纪录。

## GitHub 文字规范
- commit message、PR 标题/说明、Release/变更摘要一律使用**简体中文**。
- 分支名可保留英文（如 `feat/m3b-grouping`），便于工具与 CI 操作；分支说明用中文。
- 不重写已推送的历史（禁止 force-push）；需要汇总时更新中文 `CHANGELOG.md`。

## 语言与界面
- 全项目（文档、代码注释、用户端与管理端界面）使用**简体中文**。
- 用户端直接复用原版「一线牵」Vue 3 + Vant 前端：四 tab、卡片滑动、喜欢/点赞/留言、
  活动报名、分组志愿、一对一结果。只改登录/API/媒体/飞书文案适配，不重画 UI。
  管理端可自行设计，不受原版限制。
- 用户 SPA 不承诺无 JS；登录、注册、管理端仍须服务端可用。

## 不可违反的规则
- 所有设定从环境变数读；不得写死任何 DB/R2/session 密钥。
- 密码只存 Argon2id PHC 字串；session/activation token 只存 SHA-256 hash。
- 不得在日志、错误信息、audit metadata 中输出密码、原始 token、cookie。
- session cookie：HttpOnly + Secure（可 env 关闭供本机 HTTP）+ SameSite=Lax。
- 所有写入（含 logout、上传）必须验 CSRF；`/api/*` 写入只接受 `X-CSRF-Token` header。
- R2 物件路径不得外泄给浏览器；一律经 `GET /media/{id}` 授权后串流。
- 只有 `TRUSTED_PROXY` 来源可提供 `X-Real-IP` / `X-Forwarded-For`。
- 图片只接受 JPEG/PNG；HEIC 回 400；不可信任上传的 Content-Type，需 sniff + decode。
- 不新增 Redis；session 与 rate-limit 状态以 Postgres / 进程内为限。
- 用户端浏览器范围：iOS 14.5+/Safari 14.1+、Chrome/WebView 84+、Firefox 74+；
  不维护第二套完整用户 UI，低于范围只提供登录/管理端基本可用性。

## 变更后必跑
- 集成到 `main` 后、push 前，必須在整合後的樹上跑 `make ci`（`gofmt`/vet/`go test -p 1`/build/迁移测试）；不可只測 feature branch 就 push。
- **push 後 CI 閉環（不可跳過）**：
  1. 以 `SHA=$(git rev-parse HEAD)` 為準，只認 `event=push` 的 run；
  2. `gh run list --commit "$SHA" --event push` 找到 run，`gh run watch <id> --exit-status` 等綠；
  3. 紅燈立即 `gh run view <id> --log-failed`，修根因後再 push；
  4. 不得用 `workflow_dispatch` 取代 push run，也不得在 push run 紅/取消時部署；
  5. 部署前再次確認當前 HEAD 的 push run `conclusion=success`。
- 若 push run 被取消/不存在：先查 workflow 觸發與 concurrency，不要手動 dispatch 掩蓋。
- docs-only push 可能因 `paths-ignore` 不產生 run；部署只認最後一個含程式碼的 push run，不為 docs 手動 dispatch。
- `go mod tidy && go vet ./... && go test -p 1 ./... && go build ./...`
- CI 的 `go test` 使用 `-p 1 -count=1`，避免多套件共用同一個 Postgres 測試庫造成競態。
（改 migration 另需对真 Postgres 跑 `TEST_DATABASE_URL=... go test ./internal/db -run Integration -v`）

## 部署（staging / 未来 production）
- 旧 `yixianqian-deploy` skill **不适用**；不要套用其 fix/*、PR、生产步骤。
- 公开入口固定是 server2（HK）→ app server；不要让 `next`/`app` 直指 server1
  或开 Cloudflare 橙云（运营商可能封锁）。
- staging：server1 `/opt/yixianqian-next`、`yixianqian-next-test`、8093；
  DB `yixianqian_staging`；R2 `yixianqian-media-staging`。
- 媒体只经 app 的 `/media/{id}` 串流；R2 URL 不外泄。
- 凭证：server2 签 `next.nantou.love`，同步到 server1 `/etc/ssl/yxq-next/`；
  详见 `docs/runbooks/staging.md`。
- 部署前保留上一版 binary；DB migration 只增不改。
- 部署/CI 闭环使用 `yixianqian-deploy-v2` skill；详细步骤也见 `docs/runbooks/staging.md`。
