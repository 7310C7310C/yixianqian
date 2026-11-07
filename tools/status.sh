#!/usr/bin/env sh
# 当前运行状态：healthz / deployed.sha / src HEAD / 自动部署 / DB 计数。
# 本文件是 STATUS 文档的查询入口；不要把输出写死回 docs。
set -u

BASE_URL=${BASE_URL:-https://next.nantou.love}
ROOT=${YXQ_ROOT:-/opt/yixianqian-next}
DB=${YXQ_DB:-yixianqian_staging}

line() { printf '%-12s %s\n' "$1" "$2"; }

# Public /healthz no longer exposes the build version (fingerprint). On
# server1, read the direct loopback endpoint so `make status` can still align
# healthz.version with deployed.sha.
HEALTH_URL=${YXQ_HEALTH_URL:-}
if [ -z "$HEALTH_URL" ]; then
  if [ -f "$ROOT/deployed.sha" ] || [ -d "$ROOT/src/.git" ]; then
    HEALTH_URL=http://127.0.0.1:8093/healthz
  else
    HEALTH_URL=$BASE_URL/healthz
  fi
fi
if health=$(curl -fsS --max-time 5 "$HEALTH_URL" 2>/dev/null); then
  line healthz "$health"
else
  line healthz "unreachable ($HEALTH_URL)"
fi

if [ ! -f "$ROOT/deployed.sha" ] && [ ! -d "$ROOT/src/.git" ]; then
  line deployed "n/a (not server1: $ROOT)"
  line "src HEAD" "n/a"
  line autodeploy "n/a"
  line db "n/a"
  exit 0
fi

if [ -f "$ROOT/deployed.sha" ]; then
  line deployed "$(tr -d '[:space:]' <"$ROOT/deployed.sha")"
else
  line deployed "n/a ($ROOT/deployed.sha)"
fi

if [ -d "$ROOT/src/.git" ]; then
  line "src HEAD" "$(git -C "$ROOT/src" rev-parse --short HEAD 2>/dev/null || echo unknown)"
else
  line "src HEAD" "n/a ($ROOT/src)"
fi

if command -v systemctl >/dev/null 2>&1; then
  line autodeploy "$(systemctl is-active yxq-autodeploy.timer 2>/dev/null || true)"
else
  line autodeploy "n/a"
fi

if command -v psql >/dev/null 2>&1 && [ "$(id -u)" = "0" ]; then
  migrations=$(runuser -u yxq -- psql -d "$DB" -Atc "select max(version) from schema_migrations" 2>/dev/null || true)
  users=$(runuser -u yxq -- psql -d "$DB" -Atc "select count(*) from users" 2>/dev/null || true)
  photos=$(runuser -u yxq -- psql -d "$DB" -Atc "select count(*) from profile_photos" 2>/dev/null || true)
  if [ -n "$migrations" ] && [ -n "$users" ] && [ -n "$photos" ]; then
    line migrations "$migrations"
    line users "$users"
    line photos "$photos"
  else
    line db "n/a (psql $DB)"
  fi
else
  line db "n/a (needs root + psql)"
fi
