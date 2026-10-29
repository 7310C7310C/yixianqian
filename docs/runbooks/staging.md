# Staging runbook（next.nantou.love）

<!-- doc-meta
owner: maintainer
status: current
last_verified: 2026-10-28
sources:
  - deploy/**
  - .env.example
  - migrations/**
  - .github/workflows/ci.yml
  - internal/tencentface/**
  - internal/face/**
  - cmd/facebackfill/main.go
  - cmd/facebackfill/main_guard.go
  - tools/migrate_testapp.py
-->

> v2 专用；旧 `yixianqian-deploy` skill 只适用 v1。当前 staging 已越过 0005
> （人脸 migration；当前最大版本见 `docs/STATUS.md`），`unchecked=0`；
> 人脸首次上线已于 2026-10-22 完成，**不要回滚 pre-0005**。
> 当前阶段、P0 与缺口看 `docs/STATUS.md`；运行版本用 `/healthz` 查；本文件只写操作步骤与陷阱。

## 1. 拓扑与路径

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
- 缩略图缓存：`/opt/yixianqian-next/cache/thumbs`（systemd `ProtectSystem=strict` 下必须在此路径）。

## 2. 部署

### 2.1 自动部署（默认）

server1 的 `yxq-autodeploy.timer` 每 60 秒检查 `origin/main`：

- 与 `/opt/yixianqian-next/deployed.sha` 相同 → 退出。
- 不同 → 要求该范围代码提交的 CI（`lint-build`/`unit`/`integration`）与 Docs 均 success；
  再执行 gofmt/vet → 构建 5 个 binary（server/seed/token/facebackfill/thumbwarm）→ 备份 `server.prev` → 切换 → restart → 轮询 `/healthz`。
- 失败不写 `deployed.sha`；切换后失败自动用 `server.prev` 回滚并重启。
- 部署闸：代码提交必须含 `Reviewed-By: <reviewer>`；docs-only 豁免；`--force` 仅限回滚/热修。
- 仅文档提交（`docs/`、`*.md`、`.team/`）：只推进 `deployed.sha`，不构建、不重启。
- 失败退避 15 分钟（`/opt/yixianqian-next/autodeploy.failed`）；`--force` 立即重试。

```sh
curl -sS https://next.nantou.love/healthz            # version=staging-<sha>
systemctl is-active yxq-autodeploy.timer
tail -n 50 /var/log/yxq-autodeploy.log
/opt/yixianqian-next/autodeploy.sh --dry-run         # 只打印计划
systemctl stop yxq-autodeploy.timer                  # 紧急冻结；已在跑的一轮会跑完
```

人工回滚后必须把 `deployed.sha` 改成实际运行的提交，否则下一轮会重新部署 `origin/main`：

```sh
git -C /opt/yixianqian-next/src rev-parse <实际提交> > /opt/yixianqian-next/deployed.sha
```

### 2.2 手动部署（回滚/热修）

```sh
cd /opt/yixianqian-next/src
git -c safe.directory=/opt/yixianqian-next/src checkout -- .
git -c safe.directory=/opt/yixianqian-next/src pull --ff-only origin main
SHA=$(git -c safe.directory=/opt/yixianqian-next/src rev-parse --short HEAD)

gofmt -l .                      # 必须为空
go vet ./...

cp -f /opt/yixianqian-next/bin/server /opt/yixianqian-next/bin/server.prev
go build -buildvcs=false -trimpath \
  -ldflags "-X github.com/7310C7310C/yixianqian/internal/platform.Version=staging-$SHA" \
  -o /opt/yixianqian-next/bin/server.next ./cmd/server
go build -buildvcs=false -trimpath -o /opt/yixianqian-next/bin/seed ./cmd/seed
go build -buildvcs=false -trimpath -o /opt/yixianqian-next/bin/token ./cmd/token
go build -buildvcs=false -trimpath -o /opt/yixianqian-next/bin/facebackfill ./cmd/facebackfill
go build -buildvcs=false -trimpath -o /opt/yixianqian-next/bin/thumbwarm ./cmd/thumbwarm
chown yxq:yxq /opt/yixianqian-next/bin/*
chmod 755 /opt/yixianqian-next/bin/*

mv /opt/yixianqian-next/bin/server.next /opt/yixianqian-next/bin/server
systemctl restart yixianqian-next-test
sleep 2
curl -sS https://next.nantou.love/healthz
```

- `-buildvcs=false`：源码归 `yxq`，root build 时避免 VCS ownership 失败。
- `staging.env` 由 systemd `EnvironmentFile` 读取（root 600）；CLI 用 `staging.cli.env`（yxq 600）。
- DB migration 由 server 启动时自动套用；migration 只增不改。
- 部署失败保留 `server.prev`；回滚见 §3。

## 3. 健康检查与回滚

```sh
curl -sS http://127.0.0.1:8093/healthz
curl -sS https://next.nantou.love/healthz   # 期望 version=staging-<最近一次代码部署提交>
# 注意：docs-only 提交只推进 deployed.sha，不重启；healthz 可能小于 repo HEAD。
systemctl status yixianqian-next-test --no-pager
journalctl -u yixianqian-next-test -n 50 --no-pager
```

回滚：

```sh
cd /opt/yixianqian-next
cp bin/server.prev bin/server
systemctl restart yixianqian-next-test
curl -sS https://next.nantou.love/healthz
```

- migration 只增不改；**只回滚到上一版 post-0005 binary**，不要回滚 pre-0005。
- 回滚 binary 不等于回滚 schema；不要删表/删列。

## 4. nginx、证书与请求体上限

```sh
# server1（repo 根目录）
TS=$(date +%Y%m%d-%H%M%S)
mkdir -p /var/backups/yxq-nginx
cp -a /etc/nginx/sites-available/next.nantou.love /var/backups/yxq-nginx/next.nantou.love.$TS 2>/dev/null || true
cp -a /etc/nginx/conf.d/00-yxq-next-allow.conf /var/backups/yxq-nginx/00-yxq-next-allow.conf.$TS 2>/dev/null || true
install -m 0644 deploy/nginx/conf.d/yxq-next-allow.conf /etc/nginx/conf.d/00-yxq-next-allow.conf
install -m 0644 deploy/nginx/next.nantou.love.conf /etc/nginx/sites-available/next.nantou.love
ln -sf /etc/nginx/sites-available/next.nantou.love /etc/nginx/sites-enabled/next.nantou.love
nginx -t && systemctl reload nginx

# server1 → server2 传输（server2 没有 repo 副本）
KEY=~/.ssh/yixianqian_relay
tar -C deploy/nginx -czf /tmp/yxq-nginx-next.tgz relay-next.conf snippets/relay-next-proxy.conf
scp -i "$KEY" /tmp/yxq-nginx-next.tgz root@45.125.16.205:/tmp/

# server2
TS=$(date +%Y%m%d-%H%M%S)
mkdir -p /var/backups/yxq-nginx /tmp/yxq-nginx-next
cp -a /etc/nginx/sites-available/relay-next.conf /var/backups/yxq-nginx/relay-next.conf.$TS 2>/dev/null || true
cp -a /etc/nginx/snippets/relay-next-proxy.conf /var/backups/yxq-nginx/relay-next-proxy.conf.$TS 2>/dev/null || true
tar -C /tmp/yxq-nginx-next -xzf /tmp/yxq-nginx-next.tgz
install -m 0644 /tmp/yxq-nginx-next/relay-next.conf /etc/nginx/sites-available/relay-next.conf
install -m 0644 /tmp/yxq-nginx-next/snippets/relay-next-proxy.conf /etc/nginx/snippets/relay-next-proxy.conf
ln -sf /etc/nginx/sites-available/relay-next.conf /etc/nginx/sites-enabled/relay-next.conf
nginx -t && systemctl reload nginx
```

- allow gate 用 `geo $realip_remote_addr $yxq_next_allowed`；**不要改回直接 `allow`**（global realip 会把 `$remote_addr` 改成用户 IP，导致全 403）。
- 请求体上限：server1 vhost 与 server2 relay 都必须 `96m`；注册理论上限 `9*10MiB+2MiB=92MiB`。
- 证书：server2 certbot 签 `next.nantou.love`，同步 `fullchain.pem` + `privkey.pem` 到 server1 `/etc/ssl/yxq-next/`（privkey 600），reload。
- server2 的 next 专用 proxy 参数在 `deploy/nginx/snippets/relay-next-proxy.conf`；不要覆盖共用 `relay-proxy.conf`。
- 改 nginx 后先在目标机 `nginx -t`，备份再 reload；护栏细节看 `tools/README.md`。

## 5. 验收 smoke

### 5.1 基础 curl

```sh
curl -sS https://next.nantou.love/healthz
curl -sk -o /dev/null -w '%{http_code} %{redirect_url}\n' https://next.nantou.love/login  # 303 …/?login=1
curl -sS -o /tmp/yxq_home.html -w '%{http_code}\n' https://next.nantou.love/     # 200
curl -sS https://next.nantou.love/register/regions | head -c 40                    # {"provinces":...
curl -sS https://next.nantou.love/register | grep -c 'data-region-field'           # ≥3（匿名源码 0 汉字）
grep -c 'applink.feishu.cn/T996FhMkTERp' /tmp/yxq_home.html                       # ≥1
grep -Eci '<img[^>]+(qr|qrcode)' /tmp/yxq_home.html                               # 0（无二维码）
curl -sk -o /dev/null -w '%{http_code}\n' https://172.245.223.118/ -H 'Host: next.nantou.love'  # 403
```

### 5.2 Playwright / API smoke

```sh
cd tools/browser
BASE_URL=https://next.nantou.love python3 public_home_smoke.py
BASE_URL=https://next.nantou.love SESSION_COOKIE=<yxq_session> python3 tab_scroll_smoke.py
BASE_URL=https://next.nantou.love SESSION_COOKIE=<yxq_session> python3 no_history_smoke.py
BASE_URL=https://next.nantou.love SESSION_COOKIE=<管理员> USER_SESSION_COOKIE=<普通用户> \
  python3 admin_native_activity_smoke.py
```

- 顶层 `/admin`（document）应 303 `/?admin=1`；iframe 请求（`Sec-Fetch-Dest: iframe`）才渲染 SSR。
- 受权媒体（照片原图、活动海报）document 直接串流是例外，应 200 `image/*`。
- HEIC：server1 需 `libheif-examples` + `libheif-plugin-libde265`（HEVC 解码插件；`heif-convert --version`）；env `HEIC_ENABLED`/`HEIF_CONVERT_PATH`/`HEIC_TIMEOUT`；`/api/media` 上传 HEIC 应 201、表单路径 303，媒体为 JPEG。
- 二維碼：`/angel-qr` 上传后统一 JPEG q92 ≤720px；检查 `Cache-Control: max-age=3600` 与 ETag。
- 管理台页面清单：`/admin`、`/admin/users`、`/admin/users/{id}`、`/admin/matching`、`/admin/points`、
  `/admin/audit`、`/admin/reports`、`/admin/suggestions`、`/admin/roles`；`/admin/activities` 的 iframe
  请求应 303 到 `/app?native_admin=1&p=group_admin`。
- 管理 API 兼容层：`python3 tools/admin_legacy_api_smoke.py`（管理员 200、普通用户 403）。
- 5xx/403/空页先看：`journalctl -u yixianqian-next-test -n 100 --no-pager`、`tail -n 100 /var/log/nginx/error.log`；不要靠重启碰运气。

## 6. 人脸、腾讯合规与回填

- 本地 YuNet 优先、Haar 回退；上传后检测、注册后补检测、`has_face` 门禁；observer 豁免 `has_face`，**不豁免身份证**。
- staging 已安装 `/etc/cron.d/yxq-facebackfill`，每 10 分钟增量；`profile_photos.unchecked` 应为 0。

```sh
# 未检测数量（期望 0；非 0 说明 probe/回填异常）
runuser -u yxq -- psql -d yixianqian_staging -Atc \
  "select count(*) from profile_photos where not face_checked;"

# 手动增量（cron 也会跑；advisory lock 防重入）
sudo -u yxq bash -c 'set -a; . /opt/yixianqian-next/config/staging.cli.env; set +a; /opt/yixianqian-next/bin/facebackfill'

# 单用户补测
sudo -u yxq bash -c 'set -a; . /opt/yixianqian-next/config/staging.cli.env; set +a; /opt/yixianqian-next/bin/facebackfill -user U-0006'
```

**腾讯云合规（可选，默认关闭）**：

- `TENCENT_FACE_ENABLED=true` 时上传路径启用；tiia `DetectLabel` + iai `DetectLiveFaceAccurate`；只影响 `has_face`，几何仍用本地检测；异常 fail-open。
- 凭证只放 `/opt/yixianqian-next/config/staging.env`（600）：`TENCENT_SECRET_ID/KEY/REGION/FACE_ENABLED/FACE_TIMEOUT`；不 echo、不进 repo/日志。
- 日志（`LOG_LEVEL=info`）每次判定一行：`action=check result=pass|blocked|skipped:<code> latency_ms=... photo_id=...`；细分调用需临时 `debug`。
- 隐私：启用后照片会压到最长边 1080 的 JPEG q85 发往腾讯云；不启用则不出 R2。

**回填守卫（必须遵守）**：

```sh
# 反例：应 exit 1，且不连接 DB/R2
/opt/yixianqian-next/bin/facebackfill -all; echo "exit=$?"

# 正例：显式带合规开关与凭证；仅短时回填，跑完立刻置回 false
TENCENT_FACE_BACKFILL=true /opt/yixianqian-next/bin/facebackfill -all -dry-run
```

- 只要 `TENCENT_FACE_ENABLED`/`TENCENT_FACE_BACKFILL` 任一为 true，凭证必须齐备且 Checker 初始化成功，否则拒绝运行。
- 不带 `-all`、两个开关都为 false 的 cron 增量保持原行为。
- 回滚：`TENCENT_FACE_ENABLED=false` → restart；无需数据回滚；已判定照片保留现有 `has_face`。

## 7. 每周推荐位（D10）

服务启动时补跑本周缺失名单，之后整点检查；ISO 周切换（周一）换批。同周重跑只剔除失效并补空位，不刷新 `created_at`。

```sh
# 本周名单概况（期望 cover 人数与条目数 > 0）
runuser -u yxq -- psql -d yixianqian_staging -Atc \
  "SELECT week, count(DISTINCT user_id), count(*), max(created_at)
     FROM weekly_recommendations
    WHERE week = to_char(now() AT TIME ZONE 'Asia/Shanghai', 'IYYY-\"W\"IW')
    GROUP BY week;"

# 某人的本周名单（只输出对象 ID，不含必显原因）
runuser -u yxq -- psql -d yixianqian_staging -Atc \
  "SELECT position, target_user_id, (pinned_by_user_id IS NOT NULL)
     FROM weekly_recommendations w JOIN users u ON u.id=w.user_id
    WHERE u.legacy_user_id='U-0006'
      AND week = to_char(now() AT TIME ZONE 'Asia/Shanghai', 'IYYY-\"W\"IW')
    ORDER BY position;"
```

- 管理端 `/admin/matching` 底部有只读汇总与「重新生成本周名单」；需 `matching.run` + CSRF，审计 `admin.recommend.run`。
- 排障：`journalctl -u yixianqian-next-test | grep 'weekly recommendations'`；名单无变化时不打印日志，安静是正常的。
- 回滚：名单是派生数据，可 `DELETE FROM weekly_recommendations WHERE week='<week>'` 后重跑；旧 binary 不读该表。
- 隐私：`pinned_by_user_id` 不得导出/进管理台/接口/log。

## 8. 数据迁移附录（testapp → v2）

- 来源基线：users 1110、observers 4、likes 132、activities 6、signups 248、group_select 238、
  group_result 324、onetoone_result 2972、messages 112、reports 3、suggestions 3、praise 19。
- staging 当前：users 1115（active 普通用户 1111、observer 4）、activities 6、signups 245、
  profile_photos 1028、points_ledger 131。来源 users+observers=1114，staging 多 1 个来源待核对。
- 手机号：staging 用 `1390000xxxx` 测试号；重复手机只保留一笔（优先 U-0006/较小 legacy ID），其余分配新测试号。
  生产必须用真实手机号；缺失/重复人工处理并写报告，不能自动乱配。
- 微信：来源「微信号」写入 `users.wechat`；重复导入时已有非空值不被源值覆盖。
- 身份证：导入只写 `id_card_last4` 与 `id_card_valid`，**不写 ciphertext/hash**；`id_card_valid=false`
  保持原样，由用户在资料页补填 18 位；不 grandfather。存量 1 个有密文账号不回填哈希。
- 照片映射：按 `<photos-dir>/<file_token>.<ext>` 递归匹配；缺失 token 报告并继续；`profile_photos`
  行数 ≠ `media` 数（多行可引用同一 media），对账按 `media_id` 去重。
- 历史原图：迁移工具上传前用 Pillow 重编码（JPEG q90/PNG）、按 EXIF/eXIf 方向 auto-orient、去元数据；
  已入库存量不批量重写，需清洗时另起一次性任务并先备份/清单。
- 积分：`SUM(delta)` 对余额；`balance_after` 不作唯一依据；`redemptions`/`invites`/`historical_participants` 分开核对。
- 对账报告未归档：需补逐表来源→目标笔数、checksum、抽样、照片数与大小、积分余额，并存档。
- migration runner 会读取 `schema_migrations.name` 并与嵌入文件名比对；同一版本号但名字不同直接报错
  （例如旧编号曾把 `id_card_hash` 记为 `0006`）。按错误提示 `dropdb`/`createdb` 重建后重跑；
  当时 staging 从 v5 正常升级，不受影响。

```sh
python3 tools/migrate_testapp.py \
  --export-dir /tmp/yxq_export \
  --database-url "$DATABASE_URL" \
  --dry-run \
  --photos-dir /tmp/yxq_photos \
  --report-out /tmp/migration-report.json
# 正式导入把 --dry-run 换成 --apply；未经 PM 批准不要 --apply
```

- schema 以 `migrations/` 为准；迁移报告需遮罩手机号与身份证。
- 生产大表索引如改 `CONCURRENTLY`，必须走手工窗口；迁移 runner 默认事务内普通 `CREATE INDEX`。

## 9. staging 运维备注

- PG 参数（2026-10-25 已 apply，写在 `postgresql.auto.conf`）：`shared_buffers=512MB`、`work_mem=8MB`、
  `random_page_cost=1.1`、`effective_io_concurrency=200`、`jit=off`、`track_io_timing=on`；
  `effective_cache_size` 维持默认 4GB。回滚 = `ALTER SYSTEM RESET <name>` + 重启；同实例所有 DB 共享。
- 缩略图缓存关闭：编辑 `staging.env` 去掉 `MEDIA_CACHE_DIR` → restart；目录可保留或删除。
- 缩略图缓存目录 `/opt/yixianqian-next/cache/thumbs` 必须存在且 `yxq` 可写：
  `install -d -o yxq -g yxq -m 700 /opt/yixianqian-next/cache/thumbs`；预热用
  `sudo -u yxq /opt/yixianqian-next/bin/thumbwarm`（并发 ≤8，只读 R2/DB）。
- 部署前确认 `staging.env` 没有残留 `MEDIA_MAX_PIXELS=12000000`；需要覆盖时用 `12500000`，或删除该行。
- 读缓存由 `READ_CACHE_*` 控制；默认 10s/1024 条/32MiB，任一为 0 关闭。
- `TRUSTED_PROXY=127.0.0.1`；server2 设 `X-Real-IP`，server1 nginx 转传。
- app 目前在 LA，staging 延迟不代表最终；上线前搬 HK app server，server2 只改 upstream。
- 生产切换、DNS 切换、删数据、凭证/权限变更必须 PM 明确批准；v1 保持可回滚。
