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
go test ./...
go build -buildvcs=false -trimpath   -ldflags "-X github.com/7310C7310C/yixianqian/internal/platform.Version=staging-$SHA"   -o /opt/yixianqian-next/bin/server ./cmd/server
go build -buildvcs=false -trimpath -o /opt/yixianqian-next/bin/seed ./cmd/seed
go build -buildvcs=false -trimpath -o /opt/yixianqian-next/bin/token ./cmd/token
chown yxq:yxq /opt/yixianqian-next/bin/*
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
