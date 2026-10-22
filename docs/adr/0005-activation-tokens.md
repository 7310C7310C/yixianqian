# ADR 0005：一次性启用 token

## 状态
已采用（P0）

## 背景
既有用户（legacy id）与新用户都需要在没有初始密码的情况下设定第一组密码；
管理员临时密码生成暂不做，P0 由 CLI 发链接。

## 决策
- `cmd/seed` 建立/更新 `legacy_user_id='U-0006'`、phone `13800000006`、
  name `测试用户`，发行 token 并印出 `BASE_URL/activate?token=...`。
- `cmd/token` 可对既有 user（`-legacy-user-id` 或已存在 phone）或新 phone
  （`-phone` + `-name`）发行 token。
- Token 为 32 bytes CSPRNG，base64url；DB 只存 SHA-256 hex，明文仅输出一次。
- 默认 TTL 24h（`ACTIVATION_TTL`），DB 端 `expires_at` 判断。
- 发行新 token 时，同一 user（或同一未绑定 phone）的旧未用 token 立即作废。
- 启用交易（`ActivateUser`）：
  1. `SELECT ... FOR UPDATE` 锁 token，检查未用且未过期。
  2. 既有 user 直接锁定；新用户以 token 的 phone/name 建立。
  3. 检查无 credential（已启用则拒绝）。
  4. 写入 Argon2id credential、把 token（含同 user 其他 token）标记 consumed。
  5. 撤销该 user 所有 session、写 welcome notification 与 audit。
  6. commit 后建立新 session 自动登录。
- Token 透过 URL query 传递；全站 `Referrer-Policy: no-referrer`，
  页面 `Cache-Control: no-store`，不写入 access log（只记 path）。

## 后果
- 一次性与并发安全由列锁 + 交易保证。
- 链接遗失只能重新发行；没有自助重寄流程。
