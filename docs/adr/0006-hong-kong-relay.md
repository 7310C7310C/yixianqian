# ADR-0006：香港 relay 入口与 staging 部署

- 状态：Accepted
- 日期：2026-10-21

## 背景

用户实测：Cloudflare 橙云 IP 与 server1 的美国 IP 都曾被中国电信商封锁，
导致用户无法稳定访问。历史上 `app.nantou.love` 一直由 server2（香港）
反向代理到 server1（洛杉矶），这是已被验证可用的公开入口。

同时，v2 需要：
- 先在 server1 做 staging（server1 有 4 核 / 5.8GB / 40GB，足够）；
- 上线前再买一台香港 app server，避免 HK → LA 的 151ms 延迟；
- 不把 R2/Cloudflare 的 URL 直接暴露给用户（同样可能被运营商拦截）。

## 决策

1. **server2（HK）永远是唯一公开入口**，DNS 不直指 server1，也不开 Cloudflare 橙云。
2. `next.nantou.love` 的 DNS 指向 server2；server2 终止 TLS，反向代理到
   server1:443（staging）或未来的 HK app server:443。
3. server1 的 nginx 只接受来自 server2 的请求（allowlist），再 proxy 到
   `127.0.0.1:8093` 的 Go app。
4. Go app 的 `TRUSTED_PROXY=127.0.0.1`（本机 nginx 是直接 peer）；
   server2 设 `X-Real-IP`，server1 nginx 原样转传，app 以它做 rate limit/日志。
5. **媒体只经 app 代理**：浏览器拿 `/media/{id}`，app 从 R2 串流；
   不把 `r2.cloudflarestorage.com`、`r2.dev` 或 R2 custom domain 给浏览器。
6. TLS：
   - staging：server2 用 certbot 签 `next.nantou.love`，凭证复制到 server1；
     server2 → server1 使用 HTTPS + SNI + 凭证验证。
   - 正式：改用 DNS-01（`*.nantou.love` 或 per-host）或自动化凭证同步，
     避免每次换 app server 手动搬 key。
7. Cloudflare DNS 保持 **DNS-only（灰云）**；R2 只用来存储与备份。

## 后果

- staging 仍会有 server2 → server1 的 151ms；**功能与 E2E 可用，但延迟不代表最终**。
- 上线前必须把 app/DB 搬到香港（或离 server2 很近的机房），再做效能 gate；
  server2 只改 upstream，DNS 不动，用户无感。
- server2 只跑 nginx，不装 Go/Postgres/Redis；1C/918MB 足够。
- 凭证同步与 renewal 是运维点；若使用 DNS-01 wildcard 可消除手动复制。
- 媒体多一层代理，但换来避开被封 IP；77MB 级别完全可接受。

## 被否决的方案

- **next 直指 server1**：会被美国 IP 封锁。
- **Cloudflare 橙云直回源**：实测橙云 IP 被封。
- **v2 跑在 server2 现有规格**：1C/918MB 跑 Go+Postgres+Redis+libvips 会 OOM。
- **R2 直连浏览器**：R2/Cloudflare endpoint 可能同样被拦截。
- **SQLite 精简版**：server1 资源足够，先维持 PostgreSQL，避免二次迁移。
