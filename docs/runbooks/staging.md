# Staging runbook（next.nantou.love）

<!-- doc-meta
owner: maintainer
status: current
last_verified: 2026-10-27
sources:
  - deploy/**
  - .env.example
  - migrations/**
  - .github/workflows/ci.yml
  - internal/tencentface/**
  - internal/face/**
  - cmd/facebackfill/main.go
  - cmd/facebackfill/main_guard.go
-->

> v2 专用；旧 `yixianqian-deploy` skill 只适用 v1。当前 staging 已是 post-0005
> （migration 5、`unchecked=0`），常规部署见「常规部署」一节。人脸首次上线已于 2026-10-22 完成，
> 重建/新环境步骤见 git 历史旧版 runbook；**不要回滚 pre-0005**。

## 当前部署快照（2026-10-26）

| 项目 | 值 |
|---|---|
| 公开入口 | `https://next.nantou.love`（server2 HK relay → server1） |
| 部署 HEAD（二进制） | `0f14252`（批次10 hotfix：assetVersion 15、likes 立即预取、门禁/部署闸） |
| `/healthz` version | `staging-0f14252` |
| migration 最大版本 | `10`（`0010_photo_authz_indexes.sql` 四索引已应用） |
| 回滚点 | `bin/server.prev=staging-9bc7888` |
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

公开首页只保留飞书入口链接，不再有二维码资产或 env；若旧 `config/staging.env` 仍有
`PUBLIC_QR_IMAGE`，删除。切换后按「健康检查」确认版本，再跑人脸
`unchecked=0` 与用户端/管理台 smoke。

### 自动部署（push → CI 绿 → 自动部署）

server1 上的 `yxq-autodeploy.timer` 每 60 秒检查一次 `origin/main`：与
`/opt/yixianqian-next/deployed.sha` 相同则直接退出；不同则要求该提交的 CI
（`lint-build`/`unit`/`integration`）与 Docs 均 success，再执行
gofmt/vet → 构建 4 个 binary（`version=staging-<short>`）→ 备份 `server.prev` → 切换 →
`systemctl restart yixianqian-next-test` → 轮询 `/healthz` 等到版本匹配；任何一步失败都
不写 `deployed.sha`，切换后失败则用 `server.prev` 自动回滚并重启。

- 脚本：`/opt/yixianqian-next/autodeploy.sh`（仓库 `deploy/autodeploy/`，root:755，支持 `--dry-run`）。
- 单实例：`flock /run/yxq-autodeploy.lock`；CI 未完成、`git fetch` 失败只等下一轮，不算失败。
- 仅文档提交（只改 `docs/`、`*.md`、`.team/`）：只推进 `deployed.sha`，不构建、不重启。
- **部署闸（Reviewed-By trailer）**：CI lint-build 运行 `deploy/autodeploy/test_autodeploy.sh`
  沙箱单测；脚本对 `deployed.sha..origin/main` 内每个代码提交校验 trailer。代码提交必须在 commit message 含
  `Reviewed-By: <reviewer>`（独立 Reviewer PASS 的落地凭证）；缺失时 autodeploy 拒绝部署并记录
  `missing Reviewed-By`。docs-only 提交豁免；`--force` 仅限回滚/热修，会打印警告后继续；
- **docs-only tip**：CI workflow 的 `paths-ignore` 不会为纯 docs/.team 变更产生 CI run；
  autodeploy 会回退到最近一个 code commit 的 CI run（`latest_code_commit`），不再空等
  `MISSING_GRACE`。tip 本身仍可作为 `deployed.sha`，healthz 显示实际二进制版本。
  测试可设 `YXQ_REQUIRE_REVIEW=0`。安装：备份旧版 → `install -m 755` → `bash -n` → `--dry-run`。
- **静态资源版本门禁**：CI lint-build 用 push before / PR base 覆盖多提交范围运行
  `tools/check_asset_version.py`；`web/static/**` 有变更但 `assetVersion` 未 bump 即红。
- **性能护栏门禁**：CI lint-build 跑 `make perf-budget`（首载 gzip 预算 + 19 项 nginx
  keepalive/静态缓存/gzip/TLS/SSE/HSTS 断言）；`prepush` 跑 `make perf-guard-test`，
  `make ci` 由 `test-migration` 的 discover 统一跑 24 个自测（不重复调用 target）。
  改 `deploy/nginx/**` 后先在目标机 `nginx -t` 并备份，再按本 runbook apply；**不得给
  `/legacy/api` 加边缘 proxy_cache**，也不得让 `proxy_cache_key` 含 `$cookie_`。热路径
  改动须按 `AGENTS.md`「性能护栏」附 `make perf-bench` / `make perf-lcp` 前后数字。
- 失败退避：同一提交失败后 15 分钟内不重试（`/opt/yixianqian-next/autodeploy.failed`），
  `--force` 可立即重试。
- 日志：`/var/log/yxq-autodeploy.log`（logrotate 每日或 10MB 轮转；脚本内置 20MB 自截断兜底）。
- 状态文件：`/opt/yixianqian-next/deployed.sha`（实际已部署提交，唯一部署指针）。

启用（维护者/PM 批准后执行一次）：

```bash
systemctl daemon-reload
systemctl enable --now yxq-autodeploy.timer
systemctl list-timers yxq-autodeploy.timer --no-pager
```

紧急冻结（push 后不想自动部署时先做这个）：

```bash
systemctl stop yxq-autodeploy.timer      # 立即停止触发；已在跑的一轮会跑完
systemctl disable yxq-autodeploy.timer   # 重启后也不再自动跑
systemctl is-active yxq-autodeploy.timer # 期望 inactive
```

排障与手动验证：

```bash
/opt/yixianqian-next/autodeploy.sh --dry-run      # 只判断+打印计划，不改任何文件/服务
tail -n 50 /var/log/yxq-autodeploy.log
systemctl status yxq-autodeploy.service --no-pager
bash /opt/yixianqian-next/tests/test_autodeploy.sh # 沙箱单测（临时仓库+假 gh，不碰生产）
```

人工回滚与自动部署的关系：自动部署失败会自行回滚且不写 `deployed.sha`，下一轮会重试同一提交
（15 分钟退避）。若人工按上一节回滚了 binary，必须把 `deployed.sha` 同步改成实际运行的提交，
否则下一轮会重新部署 `origin/main`：

```bash
git -C /opt/yixianqian-next/src rev-parse <实际运行的提交> > /opt/yixianqian-next/deployed.sha
```

自动部署只部署已 push 的 `origin/main`；migration 只增不改，回滚 binary 不回滚 schema。

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
- 媒体上限（应用默认）：`MEDIA_MAX_BYTES=10MiB`、`MEDIA_MAX_PIXELS=12_500_000`、`MEDIA_MAX_DIMENSION=8000`、`MAX_CONCURRENT_MEDIA=1`。12.5MP 覆盖 iPhone 12MP（4032×3024=12,192,768）；更高像素或 HEIC/HEIF 返回 400（提示转 JPEG）。改任一上限需同步 nginx 与注册页文案。
- 上传原图在服务端重编码：去 EXIF/GPS/XMP/文本元数据并按 EXIF 方向校正，缩图同样；迁移导入的历史原图未批量重写（见 `docs/MIGRATION.md`）。
- 部署本版本前检查 `/opt/yixianqian-next/config/staging.env`：若仍显式设置 `MEDIA_MAX_PIXELS=12000000`（或 `13000000`），改为 `12500000` 或删除该行以采用新默认，否则 12MP iPhone 原图仍会被拒。
- 请求体上限必须两端一致：理论值 `9*10MiB+2MiB=92MiB`，向上取整为 `96m` 留 multipart 余量：

```bash
# server1
grep -H 'client_max_body_size' /etc/nginx/sites-available/next.nantou.love
# server2
grep -H 'client_max_body_size' /etc/nginx/sites-available/relay-next.conf
# 期望两处都是 96m；两个文件在不同主机，不要用一条命令同时查
```

## 性能批次7：PostgreSQL 参数（staging）

> 2026-10-25 **18:04:07** 已在 server1 应用并重启（postmaster 启动时间可核对）；staging 单机
> 只有一个 cluster `16/main`，**同实例上的所有数据库（`yixianqian_staging`、`yixianqian_test`
> 等）共享这些参数**；业务代码未部署前也已生效。生产集群不在本批范围，需另行批准。

**生效值（`pg_settings`，source=`postgresql.auto.conf`）**

| 参数 | 值 | 批次7 前 |
|---|---|---|
| `shared_buffers` | 512MB | 128MB（`/etc/postgresql` 内的值） |
| `work_mem` | 8MB | 4MB（PG 默认） |
| `random_page_cost` | 1.1 | 4.0（默认） |
| `effective_io_concurrency` | 200 | 1（默认） |
| `jit` | off | on（默认） |
| `track_io_timing` | on | off（默认） |
| `effective_cache_size` | **4GB（维持默认，未调）** | 4GB |

`effective_cache_size` 维持 4GB 的理由：它只影响 planner 的成本估算、不影响正确性；本机实测
`buff/cache` 约 3.5GB，4GB 略乐观但同量级；先观察 `shared_buffers`/`jit` 的实际收益，避免一次
改太多难归因。若后续 `pg_stat_statements` 显示大表顺序扫描被低估，再降到 2–3GB（同样用
`ALTER SYSTEM` + 重启）。

```bash
# 备份：本次只备份「未被修改的 /etc 配置」，不是 auto.conf 的改前快照
#   /var/backups/yxq-pg-b7/postgresql.conf.20261025-180401  ==  /etc/postgresql/16/main/postgresql.conf 副本
#   （30248B，mtime Oct 21，内容与 /etc 版本一致；批次7 之前 auto.conf 不存在）
TS=20261025-180401
ls -l /var/backups/yxq-pg-b7/postgresql.conf.$TS
sha256sum /var/backups/yxq-pg-b7/postgresql.conf.$TS /etc/postgresql/16/main/postgresql.conf

# 应用（ALTER SYSTEM 写入数据目录下的 postgresql.auto.conf）
sudo -u postgres psql -c "ALTER SYSTEM SET shared_buffers = '512MB'"
sudo -u postgres psql -c "ALTER SYSTEM SET work_mem = '8MB'"
sudo -u postgres psql -c "ALTER SYSTEM SET random_page_cost = 1.1"
sudo -u postgres psql -c "ALTER SYSTEM SET effective_io_concurrency = 200"
sudo -u postgres psql -c "ALTER SYSTEM SET jit = off"
sudo -u postgres psql -c "ALTER SYSTEM SET track_io_timing = on"
systemctl restart postgresql   # 重启窗口数秒，影响本机全部 DB 连接

# 验证（source 应为 configuration file，且值如上表）
sudo -u postgres psql -tAc "select name||'='||setting||coalesce(unit,'')||' src='||source from pg_settings where name in ('shared_buffers','work_mem','random_page_cost','effective_io_concurrency','jit','track_io_timing','effective_cache_size')"
sudo -u postgres psql -tAc "select date_trunc('second', now() - pg_postmaster_start_time())"
curl -s http://127.0.0.1:8093/healthz
```

回滚（恢复到批次7 前，**不需要 `.bak` 文件**）：

```bash
# 批次7 之前 postgresql.auto.conf 不存在，六项的改前值都是 PG/默认或 /etc 里的值，
# 因此逐项 RESET + 重启即可精确回到改前状态（Reviewer 已逐项对照）。
sudo -u postgres psql -c "ALTER SYSTEM RESET shared_buffers"
sudo -u postgres psql -c "ALTER SYSTEM RESET work_mem"
sudo -u postgres psql -c "ALTER SYSTEM RESET random_page_cost"
sudo -u postgres psql -c "ALTER SYSTEM RESET effective_io_concurrency"
sudo -u postgres psql -c "ALTER SYSTEM RESET jit"
sudo -u postgres psql -c "ALTER SYSTEM RESET track_io_timing"
sudo -u postgres psql -tAc "select name||'='||setting from pg_settings where name in ('shared_buffers','work_mem','random_page_cost','effective_io_concurrency','jit','track_io_timing')"
systemctl restart postgresql
```

> **没有 `postgresql.conf.bak` 是正常的**：`/etc/postgresql/16/main/postgresql.conf` 全程未改
> （mtime 仍为 Oct 21），参数写在数据目录的 `/var/lib/postgresql/16/main/postgresql.auto.conf`
> （`ALTER SYSTEM` 生成，不要手改）。`/var/backups/yxq-pg-b7/postgresql.conf.$TS` 是**未修改的
> /etc 配置副本**，用于确认 `/etc` 没被动过；回滚点是「RESET + 重启」，不是拷文件。
> 若要额外留痕，可 `cp postgresql.auto.conf` 到 `/var/backups/yxq-pg-b7/auto.conf.<ts>`（可选）。

## 性能批次8：缩略图磁盘缓存（staging）

> **目录不可写时降级关闭**（批次9）：`MEDIA_CACHE_DIR` 不存在/不可写只 WARN 并关闭缓存，**不会拒绝启动**；
> 批次8 首轮部署曾因缓存目录落在 unit 只读的系统路径导致 autodeploy 回滚；运行路径必须在 `ReadWritePaths` 内。


只缓存 `?size=thumb` 的 JPEG（原图不进缓存）；授权在缓存读取之前完成（handler 先查
会话/关系，再调 `serveMediaObject`）。缓存 key = `mediaID + sha256(thumb object key|size)`：
替换照片会换 object key → 旧条目自然失效；删除照片走 `removeObject` → `purgeThumbs`
按 mediaID 前缀清理；未授权请求 404 且不写缓存。

| 项 | 值 |
|---|---|
| 环境变量 | `MEDIA_CACHE_DIR`（空=关闭）、`MEDIA_CACHE_BYTES`（默认 256MiB，16MiB–4GiB） |
| 目录 | `/opt/yixianqian-next/cache/thumbs`（unit `ProtectSystem=strict` + `ReadWritePaths=/opt/yixianqian-next`，服务只能写该目录），属主 `yxq:yxq`、权限 `700` |
| 淘汰 | 按总字节 LRU，单条 > 上限不缓存 |
| 预热 | `sudo -u yxq /opt/yixianqian-next/bin/thumbwarm -workers 8`（只读 R2/DB，写缓存）；`-dry-run` 只统计 |
| 观察 | 启动日志 `thumb cache ready dir=… entries=… bytes=…`；命中/未命中在进程内计数 |

```bash
# 建目录（部署 runbook 前置；必须在 ReadWritePaths 内）
install -d -o yxq -g yxq -m 700 /opt/yixianqian-next/cache/thumbs
# 预热（可后台；并发 ≤8）
sudo -u yxq /opt/yixianqian-next/bin/thumbwarm -workers 8
du -sh /opt/yixianqian-next/cache/thumbs

# 回滚：关闭缓存（环境变量置空）+ 重启即可；磁盘目录可保留或删除
sudo -u postgres psql -c "select 1" >/dev/null   # 占位，无 DB 变更
# 关闭：编辑 config/staging.env 去掉 MEDIA_CACHE_DIR → systemctl restart yixianqian-next-test
rm -rf /opt/yixianqian-next/cache/thumbs                      # 彻底清理（不影响 DB/R2）
```

> 实测（2026-10-25，server1，9 张自有缩略图）：无缓存 p50 255ms；冷 miss 236ms；
> 命中 p50 **9.9–16ms**。`media` 表当前 37 张（`profile_photos` 1028 行引用 29 个不同
> media_id），预热 37 张共 1.23MB、耗时 ~6s。

pgx pool 参数（代码默认，无需 env）：`MaxConns=20`、`MinConns=2`、`statement_timeout=5000`；迁移走独立连接 `SET statement_timeout=0`，`cmd/seed`/`cmd/facebackfill` 用 `db.OpenMaintenance`。回滚：改回 `internal/db/db.go` 常量并重启 app（无需 DB 重启）。

## 性能批次9：读缓存与边缘静态 gzip 变体（staging）

- Go 侧 `/legacy/api/home`、`/legacy/api/cards` 10s 进程内缓存：`READ_CACHE_ENABLED`（默认 true）、
  `READ_CACHE_TTL`（10s）、`READ_CACHE_ITEMS`（1024）、`READ_CACHE_BYTES`（默认 32MiB、硬上限 clamp 64MiB；home+cards **共享同一份**预算，按字节 LRU + 键长计入），
  单条上限 256KiB；**ENABLED=false 或任一项为 0 → 完全关闭（零值 Config 也关闭，不回落默认）**。
- 失效：任何**成功的非 GET/HEAD/OPTIONS** 请求后全局 `epoch++`（覆盖 `/legacy/api/*`、`/api/*`、`/admin/*`、`/media/*`），
  排除 `/static/`、`/healthz`、埋点（`/legacy/api/track*`、`/legacy/api/client-events`）；4xx/5xx 不 bump；
  构建期间若 epoch 变化则丢弃回填（CAS）。多实例需 pub/sub 传播 epoch（未实现）。
- 响应头仍是 `no-store`；`/legacy/api/*` 的 `Vary` **实测为空**（不要声称有 `Vary: Cookie`/`Accept-Encoding`）。
- **不做边缘 API 缓存**：曾评估在 server2 对 `/legacy/api/` 做 proxy_cache，但 key 若含 `$cookie_yxq_session`
  会把**明文 session token 落盘**（违反「session token 只存 hash」），key 不含会话又会跨用户串数据；
  方案已删除、未 apply。API 提速依赖应用内 10s 读缓存 + 上游 keepalive（目标 p95 ≤220ms）。
  未来若确需边缘 API 缓存，只能先用 `HMAC(server_secret, session)` 派生不可逆 key（不在本批）。
- **边缘静态 gzip 变体缓存（已部署，批次1 起）**：`/static/` 回源统一 `Accept-Encoding: $yxq_upstream_ae`，
  cache key 含 `$yxq_static_enc`（gzip/identity 分变体）；命中直接回已压缩字节，nginx 不再压缩。
  2026-10-26 复核（server2）：gzip 客户端 47,033B（= `app.min.js.gz`）、`gzip -t` 单层、解码 147,145B；
  q=0 / `gzip;q=abc` / `x-gzip` / identity 均落 identity（147,145B、无 Content-Encoding）；
  同连接命中 TTFB **0.87ms（gzip）/1.89ms（identity）**，首连 ~96–103ms（TLS 握手）。

## 批次10：渐进启动与照片授权（已部署 staging-0f14252）

- 壳启动顺序：三个主脚本移除静态 `defer` 与全部 script/style preload，hero 绘制后按序注入；`/app` 首屏逻辑不变。
  部署后 smoke 必须覆盖：首卡 hero 出现、无白屏、管理台 iframe 正常、Vue 缺失时可见错误页。
- 照片授权：`/cards/photo/{id}`、`/profile/photo/{id}` 默认拒绝；授权对象是画廊主人 `profile_photos.user_id`（共享 media 的 media.user_id 只用于取对象）；本人、管理员、**任一方向有效喜欢（含互喜）**、同活动报名、**任一方向消息对手方**放行；牌堆候选由 cards/home 下发时按目标用户登记 15 分钟进程内 grant。匿名 401、无权/不存在 404。
  公开资料页 `/legacy/api/users/{id}/public` 仅按 viewer 与目标本人的关系（不复用 media 级关系）返回照片 URL；跨用户 owner 仅限 active/matched/observer
  （pending/rejected 仅本人可见），observer 本人照片可读。
- migration `0010_photo_authz_indexes.sql` 只加索引：`profile_photos(media_id)`、`likes(target_id,initiator_id)`、
  `signups(activity_id,user_id)`、`messages(author_id,target_user_id)`；自动部署时 runner 在事务内执行普通
  `CREATE INDEX`（当前表小），生产大表另走手工 `CONCURRENTLY`。
- 回滚：binary 回退到上一版即可（新增索引对旧 binary 无害）；不要回滚 migration 0010，schema 只增不改。
- 多实例注意：grant 是进程内内存；多实例部署前必须改为共享 grant 或 HMAC 签名 URL（见 `docs/DECISIONS.md` §33）。

## 性能批次1：边缘/传输（nginx）

- 目标：server2 上游 keepalive（跨洋每请求 465ms → ~155ms + 源站）、`/static/` 边缘缓存（TTL 1h）、边缘 gzip、SSE 例外；
  server1 本地 upstream keepalive、恢复 proxy buffering、响应不落临时文件（磁盘余量小）。
- 变更文件：`deploy/nginx/relay-next.conf`、`deploy/nginx/snippets/relay-next-proxy.conf`、`deploy/nginx/next.nantou.love.conf`。
- 缓存目录（server2）：`/var/cache/nginx/yxq`，属主用 nginx worker 实际用户（Ubuntu 默认 `www-data`）。
- 回滚粒度：单文件回滚；两台可独立回滚，互不阻塞。

```bash
# ===== apply（先 server1，再 server2；每台先备份、nginx -t 通过才 reload）=====
TS=$(date +%Y%m%d-%H%M)
install -d -m 0755 /var/backups/yxq-nginx

# --- server1（repo 根目录执行）---
cp -a /etc/nginx/sites-available/next.nantou.love /var/backups/yxq-nginx/next.nantou.love.$TS
install -m 0644 deploy/nginx/next.nantou.love.conf /etc/nginx/sites-available/next.nantou.love
nginx -t && systemctl reload nginx

# --- server2：server2 没有仓库副本，从 server1 传文件 ---
RELAY=root@45.125.16.205
KEY=~/.ssh/yixianqian_relay
ssh -i "$KEY" "$RELAY" "install -d -m 0755 /var/backups/yxq-nginx /tmp/yxq-nginx-$TS && install -d -o www-data -g www-data /var/cache/nginx/yxq"
scp -i "$KEY" deploy/nginx/snippets/relay-next-proxy.conf deploy/nginx/relay-next.conf \
  "$RELAY:/tmp/yxq-nginx-$TS/"
ssh -i "$KEY" "$RELAY" "set -e
  TS=$TS
  cp -a /etc/nginx/sites-available/relay-next.conf /var/backups/yxq-nginx/relay-next.conf.\$TS
  cp -a /etc/nginx/snippets/relay-next-proxy.conf /var/backups/yxq-nginx/relay-next-proxy.conf.\$TS
  install -D -m 0644 /tmp/yxq-nginx-\$TS/relay-next-proxy.conf /etc/nginx/snippets/relay-next-proxy.conf
  install -m 0644 /tmp/yxq-nginx-\$TS/relay-next.conf /etc/nginx/sites-available/relay-next.conf
  nginx -t && systemctl reload nginx"
```

> 顺序要求：`/var/cache/nginx/yxq` 必须在 `nginx -t` 之前创建（缺目录会 reload 失败）；server1 与 server2 可独立回滚。
> **`nginx -t` 失败分支**：任一台 `nginx -t` 非 0 时不要 reload，先把该台备份 `cp` 回原路径并再次 `nginx -t`（让磁盘配置回到旧版）；若 server1 已 reload 而 server2 失败，立即回滚 server1（见下），再排查 server2。

```bash
# ===== rollback（每台单独执行）=====
# 示例：apply 时的 TS=20261025-0215；回滚时显式设置同一值（不要写 <TS>，shell 会当成重定向）
TS=20261025-0215
cp -a "/var/backups/yxq-nginx/next.nantou.love.$TS" /etc/nginx/sites-available/next.nantou.love
nginx -t && systemctl reload nginx

# server2（从 server1 执行）
RELAY=root@45.125.16.205
KEY=~/.ssh/yixianqian_relay
TS=20261025-0215
ssh -i "$KEY" "$RELAY" "set -e
  TS=$TS
  cp -a /var/backups/yxq-nginx/relay-next.conf.\$TS /etc/nginx/sites-available/relay-next.conf
  cp -a /var/backups/yxq-nginx/relay-next-proxy.conf.\$TS /etc/nginx/snippets/relay-next-proxy.conf
  nginx -t && systemctl reload nginx"
```

```bash
# ===== 验证 =====
# 1) 同连接连续两个请求：第 2 条 TTFB 期望 ≤200ms（改前 ~465ms）
curl -s -o /dev/null -o /dev/null -w 'ttfb=%{time_starttransfer}\n' \
  https://next.nantou.love/healthz https://next.nantou.love/healthz

# 2) 静态：Go 直接回 .gz 预压缩变体，边缘按编码分开缓存、命中不再压缩
#    （批次4；gzip/identity 是两个 cache key，见 relay-next-proxy.conf 的 proxy_cache_key）
V=$(date +%s)
for f in legacy/app.js legacy/app.css legacy/lib/vant.min.js legacy/lib/vue.global.prod.js legacy/lib/vant.min.css legacy/app.render.js; do
  U="https://next.nantou.love/static/$f?v=$V"
  echo "== $f"
  echo -n 'gz MISS '; curl -sS -D - -o /dev/null -H 'Accept-Encoding: gzip' "$U" | tr -d '\r' \
    | grep -iE 'content-encoding|content-length|x-cache-status' | tr '\n' ' '; echo
  echo -n 'gz HIT  '; curl -sS -D - -o /dev/null -H 'Accept-Encoding: gzip' "$U" | tr -d '\r' \
    | grep -iE 'content-encoding|content-length|x-cache-status' | tr '\n' ' '; echo
  echo -n 'ident   '; curl -sS -D - -o /dev/null "$U" | tr -d '\r' \
    | grep -iE 'content-encoding|content-length|x-cache-status' | tr '\n' ' '; echo
done
# 期望：gz 的 MISS/HIT 都有 content-encoding: gzip 且 content-length 与 web/static/<f>.gz 一致；
# identity 无 content-encoding、content-length 为源文件大小、且它有自己的 MISS/HIT。
# 直接用 HEAD 会把 HEAD 结果写进缓存，务必用 -D - -o /dev/null（GET）。

# 4) HSTS 在 /static/ location 仍生效（location 内有 add_header 会覆盖 server 级）
curl -sSI https://next.nantou.love/static/legacy/lib/vant.min.js | tr -d '\r' \
  | grep -i 'strict-transport-security'   # 期望且仅 1 条

# 5) 回源路径压缩（server1 nginx，不经边缘缓存）
curl -sk -H 'Host: next.nantou.love' -H 'Accept-Encoding: gzip' -sI \
  https://127.0.0.1/static/legacy/lib/vant.min.js | tr -d '\r' | grep -i 'content-encoding'

# 6) SSE 仍能收到事件（需登录 cookie；浏览器端或 curl -N）
curl -N -H "Cookie: <yxq_session>" https://next.nantou.love/legacy/api/notifications/stream | head -3

# 7) 边缘连接占用（SSE 每连接占 1 客户端 + 1 上游；worker_connections 768）
ssh -i ~/.ssh/yixianqian_relay root@45.125.16.205 \
  "ss -tn state established '( sport = :443 )' | wc -l"
```

- `/static/` location 内有 `add_header`，会覆盖 server 级 HSTS，因此该 location 显式重复 HSTS；新增 location 时同理。
- `ssl_session_cache` 的 zone 名是全局的：server2 另有 vhost 使用 `shared:SSL:10m`，因此 relay-next 用 `shared:YXQNEXT:20m`；改名/调大小前先 `nginx -T | grep ssl_session_cache` 确认不会同名冲突。
- server2 的 `gzip_types` 在 `deploy/nginx/relay-next.conf` 的 server 段显式声明（主配置 `/etc/nginx/nginx.conf` 的 gzip_types 默认是注释）；改压缩策略时同步这里，否则 js/css 会退回 identity。
- `proxy_cache_path` 目录必须存在且可写，否则 reload 失败。
- SSE 会长期占用一个上游连接；关注 server2 `worker_connections`（768，单 worker）与上面第 7 条计数。
- 静态 TTL：带 `?v=` 的应用/库资源由 Go 发 `public, max-age=31536000, immutable`，边缘按上游 TTL 缓存 1 年；裸路径仍 1h。改静态文件必须同时 bump `httpserver.assetVersion`。
- 壳 `/app` 为 `private, no-cache` + `ETag`，回访 304；app.css/app.js 与 4 个库均带 `?v=<assetVersion>`。
- 壳内嵌首页 bootstrap（批次4）：`Vary: Cookie`、`private, no-cache`，ETag 覆盖 `assetVersion`+用户 id+bootstrap 字节 hash（64 bit），因此 304 只会复用**逐字节相同**的个人数据；禁止让边缘缓存 `/`、`/app`。

- migration `0008_perf_indexes.sql` 只加索引；自动部署时 runner 在事务内执行普通 `CREATE INDEX`（当前表小，锁窗口毫秒级）；生产大表另走手工 `CONCURRENTLY`。

## 性能批次2：媒体缓存与 SPA 首屏

- 媒体：`/media/{id}`、`/cards/photo/{id}`、`/profile/photo/{id}`、活动海报、管理台受权照片统一 `ETag`（`<id>-thumb|orig`）与 `Cache-Control: private, max-age=<thumb 24h|orig 1h>, immutable`；`If-None-Match` 返回 304；**仍为授权后私有串流，未改 public、未做 Range**。
- R2 读取去掉 `Stat()` 额外 HEAD：单次 GET，先读首块再写响应头，缺对象仍 404。
- SPA：内联 CSS/JS 拆为 `/static/legacy/app.<assetVersion>.css|js`（实际文件名 `app.css`/`app.js` + `?v=`），壳保留启动配置与骨架；4 个库改 `defer` + `preload`（批次10 起移除 script/style preload）；PhotoSwipe 首次打开图片时动态加载（失败降级直接开原图）；卡片首图 `eager`+`fetchpriority=high`，其余 `lazy`，列表用 `?size=thumb`。
- 壳 `private, no-cache` + `ETag`；`/static` 带 `?v=` 的上游响应为 1y immutable（nginx 不再 `proxy_ignore_headers Cache-Control`）。
- 回滚：静态资源已 `go:embed` 进二进制，**改磁盘文件无效**。回滚 = 回退提交（或用上一版 binary）后重新构建/部署；不要保留公开可取的旧壳文件。需要旧壳时从 git 历史取（`git show <commit>:web/static/legacy/index.html`）。

```bash
# 验证：壳 304、静态 immutable、媒体 304
C=<yxq_session>
E=$(curl -sI -H "Cookie: yxq_session=$C" https://next.nantou.love/app | tr -d '\r' | awk 'tolower($1)=="etag:"{print $2}')
curl -s -o /dev/null -w 'shell 304=%{http_code}\n' -H "Cookie: yxq_session=$C" -H "If-None-Match: $E" https://next.nantou.love/app
curl -sI "https://next.nantou.love/static/legacy/app.js?v=<assetVersion>" | tr -d '\r' | grep -iE 'cache-control|etag'
ME=$(curl -sI -H "Cookie: yxq_session=$C" "https://next.nantou.love/cards/photo/<mediaID>?size=thumb" | tr -d '\r' | awk 'tolower($1)=="etag:"{print $2}')
curl -s -o /dev/null -w 'media 304=%{http_code}\n' -H "Cookie: yxq_session=$C" -H "If-None-Match: $ME" "https://next.nantou.love/cards/photo/<mediaID>?size=thumb"
```

## SPA 根模板预编译（批次3）

- 生成物 `web/static/legacy/app.render.js` 已提交；生产部署不需要 Node。
- 改 `index.html` 的 `#app` 模板后：
  ```bash
  cd tools/spa_precompile
  npm ci
  npm run build
  npm run check
  ```
  然后按静态资源变更 bump `internal/httpserver/templates.go` 的 `assetVersion` 并重新构建部署。
- `@vue/compiler-dom` 必须与 `lib/vue.global.prod.js` 版本一致（当前 3.4.27）；升级 Vue 必须同步升级并重新生成。
- **回滚不依赖运行时编译**（批次4 起 Vue 为 runtime-only，没有编译器）：`app.render.js` 缺失/损坏时
  `app.js` 不白屏，直接渲染「页面加载失败 + 重新加载」错误页并 `console.error`；恢复靠重新部署正确
  `app.render.js`（或 `git revert` 预编译提交后重建）。

## 性能批次4：首屏数据内联 + 静态预压缩 + runtime-only Vue

- **首屏 bootstrap**：`serveLegacyApp` 复用 `/legacy/api/home` 的同一聚合函数（`buildLegacyHomePayload`，handler 只是 HTTP 包装）生成 `window.__YXQ_BOOTSTRAP__`，800ms 超时；失败/超时只跳过注入，壳照常渲染，前端回退 `fetch('/legacy/api/home')`。匿名请求仍 303 到登录页，不注入任何个人数据。首卡缩图在 `<head>` 通过 `<link rel="preload" as="image" fetchpriority="high">` 提前发起（同源、带 cookie）。
- 安全：`json.Marshal` 默认转义 HTML，再把 `</` 替换成 `<\/`，昵称含 `</script>` 也不会逃出脚本块；集成测试覆盖 XSS 转义、匿名、ETag/304。
- **PII 边界**：`sanitizeBootstrap` 按键名模式剔除（含 `wechat`/`phone` 子串、`id_card`/`idcard` 前缀），互喜的 `my_wechat`/`their_wechat` 也在其中；这些字段只经 `no-store` 的 `/legacy/api/home`、`/legacy/api/likes/me`、`/legacy/api/profile` 下发。互喜页预填与展示都取 likes API，壳里丢了也不影响页面。新增首页字段时，若属于联系方式/证件，命名包含上述模式即自动被剔除；否则要同步 `sensitiveBootstrapKey` 与测试。
- **静态预压缩**：`make static-gzip`（`go run ./tools/static_gzip`）为 `web/static/**` 的 `.js/.css/.html/.svg` 生成 gzip -9 兄弟文件并随 `go:embed` 发布；Go 静态 handler 按 `Accept-Encoding` 返回 `.gz`（`Content-Encoding: gzip`、`Vary: Accept-Encoding`、`ETag` 带 `-gzip/-identity`、`If-None-Match` 304），无变体时回退 `FileServer`；直接请求 `*.gz` 返回 404。`make ci` 跑 `static-gzip-check`，`TestStaticGzipFilesMatchSources` 校验每个 `.gz` 解压后与源文件逐字节一致。
- 兼容：nginx `map $http_accept_encoding $yxq_static_enc` 把 `gzip;q=0` 判为 identity（拒绝规则排在 `~*gzip` 前），`proxy_cache_key` 追加该变量；否则 q=0 客户端会把 identity 字节写进 gzip 缓存条目。
- **runtime-only Vue**：`lib/vue.global.prod.js` 换成同版本 3.4.27 `vue.runtime.global.prod.js`（147,796→90,713B）；
  全仓已无运行时模板（根模板走 `app.render.js`，`GaLoading` 改为 `render()`）。
  **不再有 template 兜底**：`Vue` 未加载或 `window.__YXQ_RENDER__` 不是函数时，`app.js` 渲染可见错误页
  （`.boot-fail`，含详情与「重新加载」）并 `console.error('[yxq] boot failed')`，不再是白屏。
  `app.render.js` 自身也带 `typeof Vue` 守衛（运行库缺失时直接 return，不抛 pageerror）；
  **Vant 改由 `app.js` 在 Vue 就绪后按需注入**（批次6 时 `<link rel=preload>` 仍在壳里；批次10 起移除）——
  Vant 是依赖 Vue 的 UMD，Vue 缺失时提前执行只会在 vendor 里抛 pageerror 并盖住错误页。
  三种缺失（`app.render.js` / `vue.global.prod.js` / `vant.min.js`）都只显示错误页、无 pageerror，
  由 `app_integration` smoke 的 `run_render_missing` 三个用例覆盖。
  彻底回退 = `git revert` 批次4（同时换回带编译器的 Vue 与静态 Vant 标签）。
- **静态预压缩门禁**：`-check` 只校验「`.gz` 存在且解压后与源文件逐字节一致」，因此与生成它的 Go 版本无关
  （flate 输出跨版本不同；Go 1.24.7 与 1.27.3 的字节不同但内容相同）；`-strict-bytes` 仅用于本机刚重生后的严格比对。
  `ci.yml` lint-build 已执行 `go run ./tools/static_gzip -check`，新增源文件忘生成 `.gz` 会红。
- **边缘 Accept-Encoding 规整**：`map $yxq_static_enc $yxq_upstream_ae` 只回源 `gzip` 或空值，
  两条 map 不可能与 Go 判定分叉（否则会出现 identity 键里存 gzip 字节）；`x-gzip`、`q=0`、非法 q 一律 identity。
- 回滚：`git revert` 本批次提交后重建部署（静态资源与模板都在二进制里，改磁盘无效）；nginx 两个配置文件回滚备份见批次1 §6，回滚后需 `nginx -t && systemctl reload nginx`。

## 用户端 smoke

```bash
curl -sS -o /dev/null -w '%{http_code}\n' https://next.nantou.love/          # 未登录 200 公开首页
curl -sS -o /dev/null -w '%{http_code}\n' https://next.nantou.love/login     # 200
curl -sS -o /dev/null -w '%{http_code}\n' https://next.nantou.love/app       # 未登录 303 /login?next=/
curl -sS -o /dev/null -w '%{http_code}\n' -b /tmp/yxq_cookies https://next.nantou.love/  # 已登录 200 SPA
curl -sS https://next.nantou.love/ | grep -q '立即注册'                       # 公开首页注册主按钮
curl -sS https://next.nantou.love/ | grep -q '已在飞书版使用过'               # 飞书老用户说明
test -z "$(curl -sS https://next.nantou.love/ | grep -o 'feishu-qr\|二维码')"  # 公开首页不再有二维码
curl -sS https://next.nantou.love/login | grep -q '注册新账号'                 # 登录页注册入口
```

- 匿名 `/` 必须是服务端公开首页：品牌/卖点、注册主按钮、登录入口、飞书入口链接；不展示二维码图片，也不展示用户照片/资料。
- 已登录 `/` 必须返回 v1 Vue SPA；`/legacy/api/home` 200；四 tab 用 hash 切换。
- 公开首页/登录页真实浏览器回归（390×844 与 1280×900，含截图与「无二维码」检查）：

```bash
BASE_URL=https://next.nantou.love SESSION_COOKIE=<yxq_session> \
  SHOT_DIR=/tmp/opencode/public-home-smoke \
  python3 tools/browser/public_home_smoke.py
```
- 四 tab / 活动详情 / 弹层 / 管理台 iframe 的滚动隔离（防 tab 间串位、返回丢位置）用
  `tools/browser/tab_scroll_smoke.py` 回归；需真实账号 cookie，命令见脚本头注释：
  `BASE_URL=https://next.nantou.love SESSION_COOKIE=<yxq_session> python3 tools/browser/tab_scroll_smoke.py`。
- 「我的→管理」只保留「🛠️ 管理后台」单一入口；活动/麦穗/举报的 v1 SPA 页面暂不暴露。
  管理权限与 `/legacy/api/admin/*` 形状用 `tools/admin_legacy_api_smoke.py` 回归；需管理员与普通用户两个 cookie：
  `BASE_URL=https://next.nantou.love SESSION_COOKIE=<管理员> USER_SESSION_COOKIE=<普通用户> python3 tools/admin_legacy_api_smoke.py`。
  期望：管理员 SPA 无三个单列入口、三端点 200 且形状正确；普通用户三端点 403 且返回 v1 error 信封。
- `/cards`、`/likes`、`/activities`、`/profile` 等旧用户页返回 404。
- `/cards/photo/{id}`、`/profile/photo/{id}`、`/activities/{id}/poster` 等媒体路由仍可用。
- 顶层 document 访问 `/admin` 页面一律 303 到 `/?admin=1`，由 SPA 自动打开管理台 overlay；受权媒体串流（审核照片原图、活动海报原图）是显式例外，直接串流，仍需对应权限。

## 身份证门禁与微信号交换 smoke

```bash
# 用一个 id_card_valid=false 的测试账号 cookie（部署后第一次登录）
curl -sS -o /dev/null -w '%{http_code}\n' -b /tmp/yxq_cookies \
  https://next.nantou.love/legacy/api/cards          # 期望 403
curl -sS -b /tmp/yxq_cookies https://next.nantou.love/legacy/api/cards | grep -q id_card_required
curl -sS -o /dev/null -w '%{http_code}\n' -b /tmp/yxq_cookies \
  https://next.nantou.love/legacy/api/profile        # 期望 200（资料页可打开）
curl -sS -o /dev/null -w '%{http_code}\n' -b /tmp/yxq_cookies \
  https://next.nantou.love/legacy/api/notifications  # 期望 200
curl -sS -o /dev/null -w '%{http_code}\n' -b /tmp/yxq_cookies \
  https://next.nantou.love/app                       # 期望 403（页面入口）

# 资料页补填身份证后（PUT /legacy/api/profile，字段名「身份证号」）：
curl -sS -b /tmp/yxq_cookies https://next.nantou.love/legacy/api/profile | grep -o '"id_valid":true'
curl -sS -o /dev/null -w '%{http_code}\n' -b /tmp/yxq_cookies \
  https://next.nantou.love/legacy/api/cards          # 期望 200
```

- 相互喜欢交换：准备两个已相互喜欢的测试账号。只有一方 `contact_consent` 时，双方 `/legacy/api/likes/me` 的 `mutual[].their_wechat` 必须为空且 `wechat_status` 为 `waiting_other`/`need_consent`；另一方在 SPA「相互喜欢」页填写并打开同意开关后，双方应看到对方微信号，且各收到 1 条 `wechat_exchange` 通知（重复刷新不增加）。匿名喜欢者不得出现在 mutual 列表。
- observer 账号 `has_face=true`、`id_valid=false` 且 `/legacy/api/cards` 403，属预期。
- 无生日账号回归（B1）：挑 `birthday IS NULL AND id_card_valid=false` 的账号（staging 现有 4 个观察员 + 100 个 active 测试账号），补填合法 18 位证号后应 200，`users.birthday` 写成证内生日、`id_valid=true`、`/legacy/api/cards` 200；已有生日且与证号不一致仍应 400 `id_card_birthday_mismatch`。

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

# 单用户补测（安全）
# bin/facebackfill -user U-0006
# 强制重扫：必须同时 TENCENT_FACE_BACKFILL=true（有凭证），否则程序直接拒绝；
# 不要给 cron 加 -all。
# TENCENT_FACE_BACKFILL=true bin/facebackfill -all -user U-0006

ls -l /etc/cron.d/yxq-facebackfill
tail -n 5 /var/log/yxq-facebackfill.log   # 正常接近空跑：count=0 include_checked=false
```

- 回填候选按 `media_id` 去重；`-limit`/`-dry-run` 的数量不要与 `profile_photos` 行数对账。
- 检测失败的照片保持 `face_checked=false`，可直接重跑；日志只含 media_id。
- **`-all` 守卫（2026-10-27）**：`-all` 会重扫 `face_checked=true` 的照片。若只跑本地 YuNet，
  会把腾讯云已判为非人类（动物/卡通，`has_face=false`）的照片翻回 true，等于无声撤销合规结论。
  因此 `-all` 在未设 `TENCENT_FACE_BACKFILL=true` 时**直接拒绝（exit 1）**；`-dry-run` 也不例外。
  未设该开关时只允许增量模式（不带 `-all`），cron 不受影响。
- 同理，只要检测到 `TENCENT_FACE_ENABLED=true` 或 `TENCENT_FACE_BACKFILL=true` 却缺凭证，
  回填也会 fail-fast（拒绝运行），不会静默跳过合规判定。
- 不要给 cron 加 `-all`；不要重跑全量。首次上线的 venv/FACE_* 细节已从本文件移除。

## 腾讯云照片合规判定（可选，默认关闭）

语义照 v1 `web/backend/tencent_face.py`：`DetectLabel`（tiia 2019-05-29）命中动物
（二级分类属 `ANIMAL_C2`，或名称含 猫/狗/宠物/哺乳动物/兔/仓鼠/动物，置信度 ≥30）
或明确卡通（名称含 动漫/卡通/漫画/插画/手办/玩偶/表情包，置信度 ≥80）时，再调
`DetectLiveFaceAccurate`（iai 2020-03-03）；只有「标签命中 + 活体返回
`NoFaceInPhoto`」才判为非人类照片。**效果**：照片仍可上传，但
`has_face=false`（v2 不返回 400；v1 当时直接拒绝）。任何异常（超时/鉴权/限频/
5xx/无凭证）一律 fail-open，只记固定码。

> **边界（务必对外说清）**：这里的「静默活体」只判断**照片里有没有人脸**，
> 不是用户活体检测，也不是身份认证；它不确认照片是否本人、不防翻拍/合成，
> 结果只用于 `has_face` 门禁，不产生任何身份结论。

| 环境变量 | 作用 |
|---|---|
| `TENCENT_FACE_ENABLED` | 上传路径开关；默认 false，staging 设 true |
| `TENCENT_SECRET_ID` / `TENCENT_SECRET_KEY` | 只放 600 权限 env；不得 echo/进 git |
| `TENCENT_REGION` | 默认 `ap-guangzhou` |
| `TENCENT_FACE_TIMEOUT` | 一次完整判定（最多两次调用）总预算，默认 8s，500ms–30s |
| `TENCENT_FACE_BACKFILL` | 只给 `cmd/facebackfill`；默认 false（历史照片按次计费）；**`-all` 必须为 true** |

凭证来源：v1 生产配置 `/opt/yixianqian/local_config.py`（root 600）的
`TENCENT_SECRET_ID`/`TENCENT_SECRET_KEY`/`TENCENT_REGION`，写入 staging env 时不要在
终端回显。变更步骤（先备份，再原子替换，保持 600）：

```bash
TS=$(date +%Y%m%d-%H%M%S)
cp -p /opt/yixianqian-next/config/staging.env /opt/yixianqian-next/config/staging.env.bak-tencent-$TS
# 用编辑器/umask 077 写入下列 key（值从 v1 local_config.py 读取，不打印）：
#   TENCENT_FACE_ENABLED=true
#   TENCENT_SECRET_ID=…  TENCENT_SECRET_KEY=…  TENCENT_REGION=ap-guangzhou
#   TENCENT_FACE_TIMEOUT=8s
systemctl restart yixianqian-next-test
curl -sS http://127.0.0.1:8093/healthz
```

验证（不泄露任何密钥）：

```bash
# 上传一张合成图后，日志只应有固定字段。staging 的 LOG_LEVEL=info：
# 每次判定只有 1 行汇总，action=check（result=pass|blocked|skipped:<code>）。
journalctl -u yixianqian-next-test --since '5 min ago' | grep 'tencent face' | tail -5
# 期望：tencent face photo_id=… action=check result=… latency_ms=…
# 需要逐次调用的细分（action=DetectLabel / DetectLiveFaceAccurate / result=hit|no_hit|face|skipped:*）
# 时临时把 LOG_LEVEL 调成 debug；info 级别不输出这两行，属预期，不是故障。
```

`TENCENT_FACE_TIMEOUT` 覆盖一次判定的两次调用；超时后 fail-open（放行）。
**成本**：每张上传至少 1 次 `DetectLabel`；命中动物/卡通再 1 次活体。staging 实测
各约 1.0s / 0.7s。`TENCENT_FACE_BACKFILL=true` 会对候选照片逐张调用（1000+ 张 =
1000+ 次标签，命中再翻倍），只在明确预算时短时开启，跑完立刻置回 false。

**回填守卫（2026-10-27）**：`cmd/facebackfill -all` 在 `TENCENT_FACE_BACKFILL!=true` 时
**拒绝执行**（exit 1，stderr 明确说明原因），因为只跑本地 YuNet 会把已判非人类的照片
`has_face` 翻回 true；`-dry-run` 同样拒绝。另有 fail-fast：只要 `TENCENT_FACE_ENABLED=true`
或 `TENCENT_FACE_BACKFILL=true` 但缺 `TENCENT_SECRET_ID`/`TENCENT_SECRET_KEY`（或 Checker
初始化失败），回填直接拒绝，不会静默跳过合规判定。上传路径仍是 fail-open（缺凭证只告警）。

```bash
# 反例（应 exit 1，且不连接 DB/R2）
bin/facebackfill -all; echo "exit=$?"
# 正例：显式带上合规开关与凭证（值从 env 读取，不打印）
TENCENT_FACE_BACKFILL=true bin/facebackfill -all -dry-run
# 日常增量（cron 用法）不受影响：
bin/facebackfill
```

**回滚**：`TENCENT_FACE_ENABLED=false`（或删除该行）→ `systemctl restart
yixianqian-next-test`。无需数据回滚：已判定的照片保留现有 `has_face`；要纠正历史
结果必须显式 `TENCENT_FACE_BACKFILL=true facebackfill -all`（按次计费，谨慎；
守卫会拒绝缺少该开关的 `-all`）。凭证轮换同样改 env 后重启，旧值不保留在
repo/日志。**隐私**：启用后照片会压缩到最长边 1080 的 JPEG q85 发往腾讯云
（tiia/iai）；不选该开关则照片不出 R2/应用（详见 `docs/DECISIONS.md` 决策 41、43）。

## 每周推荐位（D10）

服务启动时补跑「本周缺失」的名单，之后整点检查一次；ISO 周切换（周一）换批：
保留 4 个算法位、其余换新，必显（匿名喜欢你的人）永远在且排最前。同周重跑只
剔除失效并补空位，不会打乱本周名单。生成在事务内完成并用 advisory lock 串行化，
名单未变化时不重写（`created_at` 即生成时间）。

```bash
# 本周名单概况（期望：cover 人数与条目数 > 0；日期是本周生成时间）
runuser -u yxq -- psql -d yixianqian_staging -Atc \
  "SELECT week, count(DISTINCT user_id) AS viewers, count(*) AS entries, max(created_at)
     FROM weekly_recommendations
    WHERE week = to_char(now() AT TIME ZONE 'Asia/Shanghai', 'IYYY-\"W\"IW')
    GROUP BY week;"

# 某人的本周名单（只输出被推荐对象 ID，不含必显原因）
runuser -u yxq -- psql -d yixianqian_staging -Atc \
  "SELECT w.position, w.target_user_id, (w.pinned_by_user_id IS NOT NULL) AS pinned
     FROM weekly_recommendations w
     JOIN users u ON u.id = w.user_id
    WHERE u.legacy_user_id = 'U-0006'
      AND w.week = to_char(now() AT TIME ZONE 'Asia/Shanghai', 'IYYY-\"W\"IW')
    ORDER BY w.position;"
```

- 管理端：`/admin/matching` 底部「本周推荐位」只读展示覆盖人数/条目数/生成时间与最近 20 人；
  「重新生成本周名单」需 `matching.run`、CSRF，写审计 `admin.recommend.run`。管理端不展示
  必显原因，`pinned_by_user_id` 只允许在服务端查询。
- 排障：`journalctl -u yixianqian-next-test | grep 'weekly recommendations'`；启动/整点日志只在
  名单实际变化时打印，安静是正常的。若本周 0 行，先确认有 `account_status='active'`、
  `account_kind='user'`、性别为「男性/女性」的用户，再点手动重跑。
- 回滚：名单是派生数据，可直接 `DELETE FROM weekly_recommendations WHERE week='<week>'` 后重跑；
  不需要回滚 migration（旧 binary 不读该表）。
- 隐私：`pinned_by_user_id` 记录「谁暗恋谁」，不得导出、不得进管理台/接口/log。用户接口只暴露
  `recommended` 布尔值与置顶顺序。

## 门禁与已知限制

- **身份证门禁（P0，2026-10-24 已实现，待部署验收）**：非观察员且 `id_card_valid=false` 时，除首页/资料/通知/改密/退出（及版本/埋点/mutual 微信号表单）外的 `/legacy/api/*` 返回 403 `id_card_required`；`/app` 与 `/admin` 页面入口返回 403 HTML。资料编辑页可补填 18 位身份证，成功后解除。部署后先用一个 `id_card_valid=false` 的测试账号按下方命令验证，再用真实迁移账号抽查。
- observer 沿用 v1 的 `has_face` 放行；`yxq_view_role=observer` 只是 UI 视角，不会让 `has_face` 变 true。
- observer 的身份证不豁免；`has_face=true` 但 `id_valid=false` 属预期，`/legacy/api/cards` 仍 403。
- 卡片照片 `faces` 只回传 `{c,box,tb:[0,0],lr:[0,0]}`；v1 黑边裁切未移植。
- 腾讯云照片内容合规（宠物/卡通 + 静默活体）已实现（2026-10-27，`internal/tencentface`，决策 41）：
  启用 `TENCENT_FACE_ENABLED=true` 后 `has_face = 本地 YuNet/Haar 检出 && 未被判为非人类`；
  未启用或判定失败（fail-open）时 `has_face` 仍只代表本地检出。静默活体只判断「照片里有没有人脸」，
  不是用户活体/身份认证。
- 人脸照片缺口：104 个 active 普通用户没有 `has_face=true` 照片（101 个无照片），可通过资料页上传修复。

## 安全与限制

- 管理台纵向权限（2026-10-24）：临时密码、停用/恢复、审核通过/拒绝对 `super_admin`/`admin` 目标仅 `super_admin` 可执行；`approve`/`reject` 只接受 `pending`/`rejected`，active 账号必须走 `user.manage` 停用/启用。handler 与 DB 层双重校验；回归 `TEST_DATABASE_URL=... go test ./internal/db ./internal/httpserver -run 'TestIntegrationAdminRBAC|TestIntegrationAdminPrivilegeBoundaries'`。
- 跨用户照片（批次10）：`/cards/photo/{id}`、`/profile/photo/{id}` 默认拒绝，只允许本人、管理员、**任一方向有效喜欢（含互喜）**、
  同活动报名、**任一方向消息对手方**；牌堆候选靠 cards/home 下发时按目标用户登记的 per-viewer 15 分钟 grant。匿名 401、
  无权/不存在 404；公开资料页只在授权时返回照片 URL。回归：
  `TEST_DATABASE_URL=... go test ./internal/httpserver -run 'CardPhotoAuthorization|PublicProfileHides'`。
- staging 只放测试资料，不接生产个资；R2 私有，不把 URL 给浏览器。
- `TRUSTED_PROXY=127.0.0.1`；server2 设 `X-Real-IP`，server1 nginx 转传。
- app 目前在 LA，staging 延迟不代表最终；上线前搬到 HK app server。
