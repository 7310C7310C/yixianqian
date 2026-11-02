# HTTP/API 局部规则（internal/httpserver）

只在本目录工作时读；项目级流程/门禁见 `../../AGENTS.md`，行为决策见 `../../docs/DECISIONS.md`。

## 门禁与错误码

- 身份证/地区/人脸门禁在服务端执行：`/legacy/api/*`、`/app`、`/admin` 未通过时返回 403；JSON 带 `code`（`id_card_required`/`region_required`），HTML 带 `X-Error-Code`。
- 白名单只放行首页、版本/埋点、资料（含照片/封面）、`/legacy/api/user/me`、通知、改密、退出与 mutual 微信号表单；不要扩大到业务 API。
- observer 只豁免 `has_face`，不豁免身份证；地区字段只接受 vendored 数据集值。

## 媒体与权限

- PostgreSQL 是唯一权威；R2 私有，浏览器只经 `/media/{id}`；媒体授权先于磁盘/边缘缓存。
- 不得整体放宽 `/media/{id}`；只有已被 `broadcasts` 引用的媒体可对登录用户放宽。
- 照片原图、活动海报在 document 直接打开时允许串流（受权例外），其余管理页仍走 iframe SSR。

## 管理台

- 浏览器顶层 `/admin`（含 `?embed=1`）一律 303 到 `/?admin=1`；iframe（`Sec-Fetch-Dest: iframe`）才渲染 SSR。
- `/admin/activities` 的 iframe 请求 303 到 `/app?native_admin=1&p=group_admin`；`native_admin` 只对 `can_admin` 开放。

## 缓存、SSE 与迁移

- `/legacy/api/home`、`/legacy/api/cards` 用 10s 进程内读缓存 + 全局 epoch；成功写请求后 epoch++；不要把错误响应写进缓存。
- 通知 SSE 用 PostgreSQL `LISTEN/NOTIFY` + 30s 兜底/15s 心跳；不要恢复轮询，多实例前先做共享 fan-out。
- 迁移只增不改；回滚 binary 不等于回滚 schema；新热查询必须附 `EXPLAIN (ANALYZE, BUFFERS)`。

## 验证

`make fast`；定向测试例如 `go test ./internal/httpserver -run 'TestIntegration(IDCardGate|RegionGate|LegacyHomeSelfCard|MessageInbox)' -count=1`。
