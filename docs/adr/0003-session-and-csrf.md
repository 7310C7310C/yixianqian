# ADR 0003：DB session 与双重提交 CSRF

## 状态
已采用（P0）

## 背景
需要可撤销的登录状态、退出登录、防 CSRF，且 P0 不引入 Redis。Session 资料必须
能在多个请求间共享并可即时失效（启用后撤销旧 session）。

## 决策

### Session
- 登录/启用成功后产生 32 bytes 随机 token（base64url），只把 SHA-256 hex 存
  `sessions.token_hash`。
- Cookie `yxq_session`（可 env 改名）：HttpOnly、SameSite=Lax、Path=/、
  Secure 由 `SESSION_COOKIE_SECURE` 控制（本机 HTTP 可关）。
- 每次请求查 DB，条件包含 `revoked_at IS NULL`、`expires_at > now()`、
  user status active。`last_seen_at` 最多每 5 分钟更新一次。
- 退出登录、启用、重新登录都撤销对应 session。
- 过期/撤销列保留 7 天供追查，由 server 每小时清理。

### CSRF
- 签名式 double-submit：HMAC-SHA256(key, nonce||expiry)，key 由
  `CSRF_SECRET` 或 `SESSION_SECRET` 派生。
- Cookie `yxq_csrf` 非 HttpOnly（仅供表单回填），值本身已签名，攻击者无法
  伪造；提交值必须与 cookie 常数时间相等且签名有效。
- HTML 表单放 hidden `csrf_token`；JSON API 写入只接受 `X-CSRF-Token`。
- 登录/退出登录/启用/上传皆验证；登录成功后轮换 token。

## 后果
- Session 可即时撤销，不需 Redis。
- 每个带 cookie 的请求多一次 DB 查询；以索引 `token_hash` 覆盖。
- CSRF cookie 可被同站子网域覆写，但无签名金钥无法产生有效 token。
