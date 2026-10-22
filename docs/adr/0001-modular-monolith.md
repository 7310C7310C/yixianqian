# ADR 0001：模组化单体

## 状态
已采用（P0）

## 背景
P0 需要登录、启用、session、媒体上传与管理；团队规模与流量未知。此时导入
微服务、信息伫列或 Redis 会增加部署与一致性成本，却没有对应收益。

## 决策
单一 Go 执行档（`cmd/server`），依职责分套件：

- `internal/config`：环境变数唯一入口。
- `internal/db`：schema、migration、全部 SQL 与交易。
- `internal/auth`：密码杂凑、opaque token、CSRF、rate limit。
- `internal/media`：图片验证/缩图、S3 界面。
- `internal/httpserver`：路由、middleware、handler、模板资料。
- `internal/platform`：client IP、随机 id、日志、时钟。

Handler 不直接写 SQL；DB 套件不知道 HTTP。外部依赖以窄界面注入
（`media.Store`），方便测试与日后替换。

## 后果
- 部署简单（一个 binary + Postgres + R2）。
- 交易可跨用户/凭证/token/session，无分散式一致性问题。
- 扩充时可先水平复制服务；但 in-memory rate limiter 只在单实例精确，
  需要多实例时再换共享存储（Redis 或 Postgres）。
