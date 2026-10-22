# Staging runbook（next.nantou.love）

> 旧的 `yixianqian-deploy` skill 仅 v1；v2 使用 `yixianqian-deploy-v2`。
> 这份 runbook 是 P0 staging 的实际操作纪录。

## 拓扑

```text
用户 → server2（HK, 45.125.16.205, nginx TLS）
      → server1（LA, 172.245.223.118, nginx TLS）
      → 127.0.0.1:8093（Go app）
      → PostgreSQL yixianqian_staging
      → R2 yixianqian-media-staging（app 代理串流，不直连）
```

- DNS：`next.nantou.love A 45.125.16.205`（server2，DNS-only）
- server2 设定：`/etc/nginx/sites-available/relay-next.conf`
- server1 设定：`/etc/nginx/sites-available/next.nantou.love`
- server1 凭证：`/etc/ssl/yxq-next/{fullchain,privkey}.pem`
- server2 凭证：`/etc/letsencrypt/live/next.nantou.love/`
- app：`/opt/yixianqian-next/bin/server`
- systemd：`yixianqian-next-test.service`（User=yxq，127.0.0.1:8093）
- DB：`yixianqian_staging`（owner `yxq`，peer auth）
- R2：`yixianqian-media-staging`（private）

## 建置与部署

```bash
# 在 server1 或建置机
cd <repo>
SHA=$(git rev-parse --short HEAD)
make ci
# 先备份当前 binary，再建新 binary；不要直接覆盖正在运行的 server
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

**包含 migration 0005 / 人脸检测的版本**：不能在这里直接 `restart`。必须按
「人脸检测」一节的顺序先安装 venv/tools、设 `FACE_*`、跑 migration + 全量
backfill，最后才 `mv bin/server.next bin/server && systemctl restart`。在
backfill 完成前切到新 binary，会让所有老照片 `has_face=false`，全站被门禁锁死。

普通（不含人脸 schema）的版本：完成建置后可直接

```bash
mv /opt/yixianqian-next/bin/server.next /opt/yixianqian-next/bin/server
systemctl restart yixianqian-next-test
```

> `-buildvcs=false`：server 上的 repo 由 yxq 拥有、build 由 root 执行时，
> git VCS stamping 会因 ownership 失败；P0 用此参数。

## 健康检查

```bash
curl -sS http://127.0.0.1:8093/healthz
curl -sS https://next.nantou.love/healthz
systemctl status yixianqian-next-test --no-pager
journalctl -u yixianqian-next-test -n 50 --no-pager
```

## 启用／登录测试

```bash
# CLI 需要可读 env；staging.cli.env 由 staging.env 产生，权限 600、owner yxq
sudo -u yxq bash -c 'set -a; . /opt/yixianqian-next/config/staging.cli.env; set +a; /opt/yixianqian-next/bin/seed'
# stdout 是一条 activation URL；在浏览器打开设定密码
```

`seed` 的本地默认手机号以 `cmd/seed` 为准；staging 的 U-0006 是 `13800138000`。可用 `-legacy-id`、`-phone`、`-name` 覆盖。
若账号已有密码，seed 不再发 token；用 `cmd/token` 对其他账号发 token。

## 凭证

staging 凭证由 server2 签发，需同步到 server1：

```bash
# server2
certbot certonly --webroot -w /var/www/acme -d next.nantou.love
tar -czhf /tmp/next-cert.tgz -C /etc/letsencrypt/live/next.nantou.love fullchain.pem privkey.pem
# 将 tar 传到 server1，解到 /etc/ssl/yxq-next/，chmod 600 privkey
# server1
systemctl reload nginx
```

server2 的 443 vhost 使用 `/etc/nginx/snippets/relay-proxy.conf`，
以 `proxy_ssl_name $host` + `proxy_ssl_verify on` 连 server1:443。

## 回滚

- 保留上一版 binary：部署前 `cp bin/server bin/server.prev`。
- 失败：`cp bin/server.prev bin/server && systemctl restart yixianqian-next-test`。
- DB migration 只增不改；回滚 binary 不等于回滚 schema。

## 安全与限制

- staging 只放测试资料；不要接生产个资。
- `server1` 的 next vhost 只 allow server2 / 自身 / localhost。
- R2 bucket 为 private；不要把 R2 URL 给浏览器。
- `TRUSTED_PROXY=127.0.0.1`；server2 设 X-Real-IP，server1 nginx 转传。
- 目前 app 在 LA，staging 延迟不代表最终；上线前搬到 HK app server。
- 旧 `yixianqian-deploy` skill 仅 v1；v2 CI/部署流程见 `yixianqian-deploy-v2` 与本 runbook。


## nginx 安装与权限

```bash
# server1
install -m 0644 deploy/nginx/conf.d/yxq-next-allow.conf /etc/nginx/conf.d/
install -m 0644 deploy/nginx/next.nantou.love.conf /etc/nginx/sites-available/
ln -sf /etc/nginx/sites-available/next.nantou.love /etc/nginx/sites-enabled/
nginx -t && systemctl reload nginx

# 不允许 nginx 设定被任何用户改写
test -z "$(find /etc/nginx -type f -perm -0002 -print -quit)"

# server2
install -m 0644 deploy/nginx/relay-next.conf /etc/nginx/sites-available/
ln -sf /etc/nginx/sites-available/relay-next.conf /etc/nginx/sites-enabled/
nginx -t && systemctl reload nginx
```

`/etc/nginx/conf.d/yxq-next-allow.conf` 用 `$realip_remote_addr`（realip 改写前的
原始 peer）做 allow gate；不要改回直接用 `allow`，因为 global realip 会让
`$remote_addr` 变成真实 client IP，导致所有外部请求 403。

## 人脸检测（本地 YuNet/Haar，migration 0005）

运行时依赖 Python 3 + OpenCV（cv2）+ Pillow/numpy。**使用独立 venv，不要假设
v1 的 venv（`/opt/yixianqian-test/web/backend/venv`）永久存在**：

```bash
# server1，root
install -d -o yxq -g yxq /opt/yixianqian-next/tools
install -m 0644 -o yxq -g yxq \
  /opt/yixianqian-next/src/tools/face_probe.py \
  /opt/yixianqian-next/src/tools/face_detection_yunet.onnx \
  /opt/yixianqian-next/src/tools/face_detection_yunet.LICENSE \
  /opt/yixianqian-next/tools/
python3 -m venv /opt/yixianqian-next/venv
/opt/yixianqian-next/venv/bin/pip install --no-cache-dir \
  'opencv-python-headless>=4.8,<5' 'numpy>=1.26,<3' 'Pillow>=10,<13'
chown -R yxq:yxq /opt/yixianqian-next/venv /opt/yixianqian-next/tools
runuser -u yxq -- /opt/yixianqian-next/venv/bin/python -c \
  'import cv2, numpy, PIL; print(cv2.__version__)'
```

`staging.env`（root 600）追加：

```sh
FACE_PROBE_CMD=/opt/yixianqian-next/venv/bin/python /opt/yixianqian-next/tools/face_probe.py
FACE_MODEL_PATH=/opt/yixianqian-next/tools/face_detection_yunet.onnx
FACE_PROBE_TIMEOUT=8s
```

健康检查（有脸图应 `has_face=true`，纯色图应 false，均不应报错）：

```bash
/opt/yixianqian-next/venv/bin/python \
  /opt/yixianqian-next/tools/face_probe.py /tmp/face.jpg
```

### 切换顺序（关键）

migration 0005 只加列；旧 binary 不认识这些列，仍按旧逻辑返回 `has_face`，
所以**先在旧 binary 运行时完成 migration + 全量回填，再切新 binary**。顺序不能反：

1. 建置 `bin/server.next` 与 `bin/facebackfill`（见“建置与部署”），备份 `bin/server.prev`；
2. 安装 venv/tools/model，确认 `FACE_*` 已写入 `staging.env`；
3. 生成 `facebackfill.cli.env`（含 DB/R2/FACE_*）；
4. `facebackfill -dry-run`：它会执行 `database.Migrate`，把 0005 落在旧 binary 运行期间，并打印待处理数量；
5. 旧 binary 继续服务时跑**全量回填**（`-workers 2`，约 1028 张、25-30 分钟）；
6. 回填成功且有脸用户已落库后，才 `mv bin/server.next bin/server && systemctl restart yixianqian-next-test`；
7. 切后跑一次**增量回填**（不带 `-all`），补上第 5 步期间旧 binary 接收的新照片；
8. 验证 U-0006 `has_face=true`、U-0003 `has_face=false`，再走用户端 smoke。

任何一步失败：保持旧 binary 运行，修复后重跑；不要在回填未完成时切 binary。
回填失败的照片保持 `face_checked=false`（可重跑），不影响旧 binary。

批量命令需要 DB、R2 与 FACE_* 三组变量；`staging.cli.env` 只有 DB/session，
且 `staging.env` 的值含 `&` 与空格，不能直接 `source`。先生成 yxq 可读的
CLI env（root 执行）：

```bash
cd /opt/yixianqian-next
tmp=$(mktemp)
grep -v '^#' config/staging.env > "$tmp"
cat >> "$tmp" <<'EOF'
FACE_PROBE_CMD=/opt/yixianqian-next/venv/bin/python /opt/yixianqian-next/tools/face_probe.py
FACE_MODEL_PATH=/opt/yixianqian-next/tools/face_detection_yunet.onnx
FACE_PROBE_TIMEOUT=8s
EOF
install -m 600 -o yxq -g yxq "$tmp" config/facebackfill.cli.env
rm -f "$tmp"
```

执行时逐行 export（不要用 `.`/`source`）：

```bash
runuser -u yxq -- bash -c '
  while IFS= read -r line; do
    case "$line" in ""|\#*) continue;; esac
    export "${line%%=*}=${line#*=}"
  done < /opt/yixianqian-next/config/facebackfill.cli.env
  cd /opt/yixianqian-next
  exec bin/facebackfill -dry-run          # 先跑：应用 migration + 看数量
'
# 小批量验证：-limit 20
# 全量（1028 张，2 workers 约 25-30 分钟）：-workers 2
# 切 binary 后的增量补齐（只扫 face_checked=false）：不带参数
# 单用户补测：-user U-0006
# 强制重扫：-all -user U-0006
```

检测失败的照片保持 `face_checked=false`，命令以非零退出，可直接重跑；
日志只含 media_id，不输出对象键或图片路径。

### 门禁与已知限制

- 账号本身为 observer 时沿用 v1 的 `has_face` 放行；普通用户不豁免。
  `yxq_view_role=observer` 只是观察员 UI 视角，**不会**让 `has_face` 变 true。
- 观察员是否继续豁免尚未由 PM 最终裁定；当前保留现状。
- 卡片/资料照片的 `faces` 只回传 `{c,box,tb:[0,0],lr:[0,0]}`。v1 的黑边
  裁切（识别上下黑边并改写缓存图）**未移植**，黑边照片按原图构图显示。
- 腾讯云照片内容合规（宠物/卡通判定、活体）**未实现**，本轮不做；
  `has_face` 仅代表本地 YuNet/Haar 检出人脸。

## 用户端 smoke

```bash
curl -sS -o /dev/null -w '%{http_code}\n' https://next.nantou.love/          # 未登入 303 /login
curl -sS -o /dev/null -w '%{http_code}\n' https://next.nantou.love/login   # 200
curl -sS -o /dev/null -w '%{http_code}\n' -b /tmp/yxq_cookies https://next.nantou.love/ # 已登入 200 v1 SPA
```

- 已登录 `/` 必须返回 v1 Vue SPA；`/legacy/api/home` 200。
- v1 SPA 四 tab 用 hash 切换；不会整页 reload。
- `/admin` 仍为 Go SSR。
- `/cards`、`/likes`、`/activities`、`/profile` 等旧用户页返回 404。
- `/cards/photo/{id}`、`/profile/photo/{id}`、`/activities/{id}/poster` 等媒体路由仍可用。
