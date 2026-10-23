# Staging runbook（next.nantou.love）

<!-- doc-meta
owner: maintainer
status: current
last_verified: 2026-10-23
sources:
  - deploy/**
  - .env.example
  - migrations/**
  - .github/workflows/ci.yml
-->

> v2 专用；旧 `yixianqian-deploy` skill 只适用 v1。当前 staging 已是 post-0005
> （migration 5、`unchecked=0`），常规部署见「常规部署」一节。人脸首次上线已于 2026-10-22 完成，
> 重建/新环境步骤见 git 历史旧版 runbook；**不要回滚 pre-0005**。

## 当前部署快照（2026-10-23）

| 项目 | 值 |
|---|---|
| 公开入口 | `https://next.nantou.love`（server2 HK relay → server1） |
| 部署 HEAD | `a2ce99a` |
| `/healthz` version | `staging-a2ce99a` |
| migration 最大版本 | `5` |
| `profile_photos` | 总数 `1028` / `has_face=1019` / `unchecked=0` |
| 人脸定时增量 | `/etc/cron.d/yxq-facebackfill` 已安装（2026-10-22），每 10 分钟 |
| nginx 请求体上限 | server1 `next.nantou.love` 与 server2 `relay-next.conf` 均为 `96m` |
| v2 生产 | 未切换；`app.nantou.love` 仍是 v1 |

> 实时状态与 P0 缺口以 `docs/STATUS.md` 为准；ID 门禁与迁移限制见 `docs/MIGRATION.md`。

## 拓扑与路径

```text
用户 → server2（HK, 45.125.16.205, nginx TLS）
      → server1（LA, 172.245.223.118, nginx TLS）
      → 127.0.0.1:8093（Go app）
      → PostgreSQL yixianqian_staging
      → R2 yixianqian-media-staging（app 代理串流，不直连）
```

- DNS：`next.nantou.love A 45.125.16.205`（server2，DNS-only）。
- server1 nginx：`/etc/nginx/sites-available/next.nantou.love`；allow：`/etc/nginx/conf.d/00-yxq-next-allow.conf`。
- server2 nginx：`/etc/nginx/sites-available/relay-next.conf` + `/etc/nginx/snippets/relay-next-proxy.conf`。
- 证书：server1 `/etc/ssl/yxq-next/{fullchain,privkey}.pem`；server2 `/etc/letsencrypt/live/next.nantou.love/`。
- app：`/opt/yixianqian-next/bin/server`；systemd `yixianqian-next-test.service`（User=yxq，127.0.0.1:8093）。
- DB `yixianqian_staging`（owner `yxq`，peer auth）；R2 `yixianqian-media-staging`（private）。

## 构建与部署

```bash
# 在 server1 或构建机；部署前确认当前 SHA 的 push run 与 Docs run 均为 success
cd <repo>
SHA=$(git rev-parse --short HEAD)
make ci
cp -f /opt/yixianqian-next/bin/server /opt/yixianqian-next/bin/server.prev
go build -buildvcs=false -trimpath \
  -ldflags "-X github.com/7310C7310C/yixianqian/internal/platform.Version=staging-$SHA" \
  -o /opt/yixianqian-next/bin/server.next ./cmd/server
go build -buildvcs=false -trimpath -o /opt/yixianqian-next/bin/seed ./cmd/seed
go build -buildvcs=false -trimpath -o /opt/yixianqian-next/bin/token ./cmd/token
go build -buildvcs=false -trimpath -o /opt/yixianqian-next/bin/facebackfill ./cmd/facebackfill
chown yxq:yxq /opt/yixianqian-next/bin/*
chmod 755 /opt/yixianqian-next/bin/*
```

> `-buildvcs=false`：repo 由 yxq 拥有、build 由 root 执行时 git VCS stamping 会因 ownership 失败。

### 常规部署（当前默认）

```bash
mv /opt/yixianqian-next/bin/server.next /opt/yixianqian-next/bin/server
systemctl restart yixianqian-next-test
```

切换后按「健康检查」确认版本，再跑人脸 `unchecked=0` 与用户端/管理台 smoke。

### 分支与交付

- 仓库只有 `main`；功能在独立 worktree 分支开发，由维护者本地合并后 push `main`。
- 不使用 PR 流程（除非 PM 明确要求）；部署前不得有未合并改动。

## 健康检查

```bash
curl -sS http://127.0.0.1:8093/healthz
curl -sS https://next.nantou.love/healthz   # 期望 version=staging-<HEAD short sha>
systemctl status yixianqian-next-test --no-pager
journalctl -u yixianqian-next-test -n 50 --no-pager
```

## 启用/登录

- U-0006 已在 staging 激活，直接用现有密码登录；**不要对 U-0006 运行 `bin/seed`**（默认值会覆盖 phone/name，且已有密码时不会输出 activation URL）。
- 只有**新账号或尚未激活的迁移用户**才发一次性 activation token（`-legacy-user-id` 可指定既有未激活用户）：

```bash
sudo -u yxq bash -c 'set -a; . /opt/yixianqian-next/config/staging.cli.env; set +a; /opt/yixianqian-next/bin/token -phone <手机号> -name <姓名>'
# stdout 是 activation URL（一次性、24h）；在浏览器打开设定密码
```

- 已启用账号 `cmd/token`（线上为 `bin/token`）会拒绝（`already has a password`）；改用正常登录或管理员临时密码。

## 凭证（摘要）

- staging 由 server2 certbot 签 `next.nantou.love`，把 `fullchain.pem` + `privkey.pem` 同步到
  server1 `/etc/ssl/yxq-next/`（privkey 600），`nginx -t && systemctl reload nginx`。
- server2 的 next 专用 proxy 参数在 `deploy/nginx/snippets/relay-next-proxy.conf`：
  `proxy_ssl_session_reuse on`、20s 超时、SNI + 证书校验。不要覆盖共用的 `relay-proxy.conf`。
- production 改用 DNS-01 或自动同步，不长期手动搬 key。

## 回滚

- 失败：`cp /opt/yixianqian-next/bin/server.prev /opt/yixianqian-next/bin/server && systemctl restart yixianqian-next-test`。
- DB migration 只增不改；**只回滚到上一版 post-0005 binary**，不要回滚 pre-0005，也不要用 binary 回滚 0005 schema。

## nginx 与 allow

```bash
# server1：repo 文件名不带 00-，线上必须装成 00-
install -m 0644 deploy/nginx/conf.d/yxq-next-allow.conf /etc/nginx/conf.d/00-yxq-next-allow.conf
install -m 0644 deploy/nginx/next.nantou.love.conf /etc/nginx/sites-available/
ln -sf /etc/nginx/sites-available/next.nantou.love /etc/nginx/sites-enabled/
nginx -t && systemctl reload nginx

# server2
install -D -m 0644 deploy/nginx/snippets/relay-next-proxy.conf /etc/nginx/snippets/relay-next-proxy.conf
install -m 0644 deploy/nginx/relay-next.conf /etc/nginx/sites-available/
ln -sf /etc/nginx/sites-available/relay-next.conf /etc/nginx/sites-enabled/
nginx -t && systemctl reload nginx
```

- allow gate 必须用 `$realip_remote_addr`；不要改回 `allow`，否则所有外部请求 403。
- server1 allow 只包含 server2（`45.125.16.205/32`）与 localhost（`127.0.0.1/32`）；localhost 是本机健康检查/nginx 所需，不要删。
- 请求体上限必须两端一致：理论值 `9*10MiB+2MiB=92MiB`，向上取整为 `96m` 留 multipart 余量：

```bash
# server1
grep -H 'client_max_body_size' /etc/nginx/sites-available/next.nantou.love
# server2
grep -H 'client_max_body_size' /etc/nginx/sites-available/relay-next.conf
# 期望两处都是 96m；两个文件在不同主机，不要用一条命令同时查
```

## 用户端 smoke

```bash
curl -sS -o /dev/null -w '%{http_code}\n' https://next.nantou.love/          # 未登录 303 /login
curl -sS -o /dev/null -w '%{http_code}\n' https://next.nantou.love/login     # 200
curl -sS -o /dev/null -w '%{http_code}\n' -b /tmp/yxq_cookies https://next.nantou.love/  # 已登录 200
```

- 已登录 `/` 必须返回 v1 Vue SPA；`/legacy/api/home` 200；四 tab 用 hash 切换。
- 四 tab / 活动详情 / 弹层 / 管理台 iframe 的滚动隔离（防 tab 间串位、返回丢位置）用
  `tools/browser/tab_scroll_smoke.py` 回归；需真实账号 cookie，命令见脚本头注释：
  `BASE_URL=https://next.nantou.love SESSION_COOKIE=<yxq_session> python3 tools/browser/tab_scroll_smoke.py`。
- `/cards`、`/likes`、`/activities`、`/profile` 等旧用户页返回 404。
- `/cards/photo/{id}`、`/profile/photo/{id}`、`/activities/{id}/poster` 等媒体路由仍可用。
- 顶层 document 访问 `/admin` 页面一律 303 到 `/?admin=1`，由 SPA 自动打开管理台 overlay；受权媒体串流（审核照片原图、活动海报原图）是显式例外，直接串流，仍需对应权限。

## 管理台 `/admin` smoke

```bash
# 顶层入口：期望 303，Location 为 /?admin=1（不渲染顶层页面，由 SPA 自动开 overlay）
curl -sS -o /dev/null -w '%{http_code} %{redirect_url}\n' \
  -H 'Sec-Fetch-Dest: document' \
  -b /tmp/yxq_cookies https://next.nantou.love/admin

# 顶层深链与 ?embed=1 同样 303
curl -sS -o /dev/null -w '%{http_code} %{redirect_url}\n' \
  -H 'Sec-Fetch-Dest: document' \
  -b /tmp/yxq_cookies https://next.nantou.love/admin/users
curl -sS -o /dev/null -w '%{http_code} %{redirect_url}\n' \
  -H 'Sec-Fetch-Dest: document' \
  -b /tmp/yxq_cookies 'https://next.nantou.love/admin?embed=1'
# 期望均为 303 https://next.nantou.love/?admin=1

# 管理台真实入口：用 iframe 语义访问（需管理员 session；未登录 303 到 /login，无权限 403）
for p in /admin/users /admin/reports /admin/roles \
         /admin/matching /admin/points /admin/audit; do
  printf '%s ' "$p"
  curl -sS -o /dev/null -w '%{http_code}\n' -b /tmp/yxq_cookies \
    -H 'Sec-Fetch-Dest: iframe' "https://next.nantou.love$p"
done
# 期望全部 200

# 等价 iframe 首载（?embed=1 只有 iframe/无 header 才渲染）
curl -sS -o /dev/null -w '%{http_code}\n' -b /tmp/yxq_cookies \
  -H 'Sec-Fetch-Dest: iframe' 'https://next.nantou.love/admin?embed=1'
# 期望 200
```

顶层 document 访问 `/admin...` 页面一律 303 到 `/?admin=1`，服务端不渲染顶层页面；管理台只经 iframe 承载。iframe 未登录 303 到 `/login?next=<原路径>`，无权限 403。`/admin/users/{id}` 需真实用户 ID。受权媒体串流（`/admin/users/{id}/photos/{mediaID}`、`/admin/activities/{id}/poster`）不受顶层 303 约束（新窗口看原图），直接串流，但仍先鉴权并校验权限。

## 人脸检测（日常增量）

2026-10-22 已完成首次上线：migration 5、全量回填、独立 venv 与 cron 均已就绪。
日常只跑增量，不重跑全量；首次安装/回填命令见 git 历史旧版 runbook。

```bash
# 未检测数量（期望 0；非 0 说明 probe/回填异常）
runuser -u yxq -- psql -d yixianqian_staging -Atc \
  "SELECT count(*) FROM profile_photos WHERE NOT face_checked;"

# 手动增量（cron 每 10 分钟也会跑；advisory lock 防重入）
sudo -u yxq /opt/yixianqian-next/bin/facebackfill-incremental

# 单用户补测 / 强制重扫（谨慎，避免周期性全量）
# bin/facebackfill -user U-0006 / -all -user U-0006

ls -l /etc/cron.d/yxq-facebackfill
tail -n 5 /var/log/yxq-facebackfill.log   # 正常接近空跑：count=0 include_checked=false
```

- 回填候选按 `media_id` 去重；`-limit`/`-dry-run` 的数量不要与 `profile_photos` 行数对账。
- 检测失败的照片保持 `face_checked=false`，可直接重跑；日志只含 media_id。
- 不要给 cron 加 `-all`；不要重跑全量。首次上线的 venv/FACE_* 细节已从本文件移除。

## 门禁与已知限制

- **身份证门禁死锁（P0，2026-10-23）**：staging 1115 个用户中，1103 个 active + 4 个 observer `id_card_valid=false`；迁移不写 ciphertext，用户端无补录字段。PM 已裁定不 grandfather、不豁免 observer；补录/放行未决。详见 `docs/STATUS.md` §4，部署/验收不得绕过。
- observer 沿用 v1 的 `has_face` 放行；`yxq_view_role=observer` 只是 UI 视角，不会让 `has_face` 变 true。
- observer 的身份证不豁免；`has_face=true` 但 `id_valid=false` 属预期。
- 卡片照片 `faces` 只回传 `{c,box,tb:[0,0],lr:[0,0]}`；v1 黑边裁切未移植。
- 腾讯云照片内容合规（宠物/卡通/活体）未实现；`has_face` 仅代表本地 YuNet/Haar 检出人脸。
- 人脸照片缺口：104 个 active 普通用户没有 `has_face=true` 照片（101 个无照片），可通过资料页上传修复。

## 安全与限制

- staging 只放测试资料，不接生产个资；R2 私有，不把 URL 给浏览器。
- `TRUSTED_PROXY=127.0.0.1`；server2 设 `X-Real-IP`，server1 nginx 转传。
- app 目前在 LA，staging 延迟不代表最终；上线前搬到 HK app server。
