# Staging runbook（next.nantou.love）

<!-- doc-meta
owner: maintainer
status: current
last_verified: 2026-10-24
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

- **身份证门禁死锁（P0，2026-10-23）**：staging 1115 个用户中，1103 个 active + 4 个 observer `id_card_valid=false`；迁移不写 ciphertext，用户端无补录字段。PM 已裁定不 grandfather、不豁免 observer；补录/放行未决。详见 `docs/STATUS.md` §4，部署/验收不得绕过。
- observer 沿用 v1 的 `has_face` 放行；`yxq_view_role=observer` 只是 UI 视角，不会让 `has_face` 变 true。
- observer 的身份证不豁免；`has_face=true` 但 `id_valid=false` 属预期。
- 卡片照片 `faces` 只回传 `{c,box,tb:[0,0],lr:[0,0]}`；v1 黑边裁切未移植。
- 腾讯云照片内容合规（宠物/卡通/活体）未实现；`has_face` 仅代表本地 YuNet/Haar 检出人脸。
- 人脸照片缺口：104 个 active 普通用户没有 `has_face=true` 照片（101 个无照片），可通过资料页上传修复。

## 安全与限制

- 管理台纵向权限（2026-10-24）：临时密码、停用/恢复、审核通过/拒绝对 `super_admin`/`admin` 目标仅 `super_admin` 可执行；`approve`/`reject` 只接受 `pending`/`rejected`，active 账号必须走 `user.manage` 停用/启用。handler 与 DB 层双重校验；回归 `TEST_DATABASE_URL=... go test ./internal/db ./internal/httpserver -run 'TestIntegrationAdminRBAC|TestIntegrationAdminPrivilegeBoundaries'`。
- staging 只放测试资料，不接生产个资；R2 私有，不把 URL 给浏览器。
- `TRUSTED_PROXY=127.0.0.1`；server2 设 `X-Real-IP`，server1 nginx 转传。
- app 目前在 LA，staging 延迟不代表最终；上线前搬到 HK app server。
