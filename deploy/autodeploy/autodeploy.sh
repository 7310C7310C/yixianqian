#!/usr/bin/env bash
# 一线牵 v2 staging 自动部署守护。
#
# 模型：git push → GitHub CI/Docs 绿 → 本机定时检查 → 构建/切换/健康校验（失败自动回滚）。
# 只作用于 staging（/opt/yixianqian-next + systemd yixianqian-next-test），不涉及生产。
#
# 用法：
#   autodeploy.sh              # 实际部署（systemd timer 调用）
#   autodeploy.sh --dry-run    # 只判断并打印计划，不改动任何文件/服务
#   autodeploy.sh --force      # 忽略失败退避，立即重试同一 SHA
#
# 退出码：0 = 无动作/等待下一轮/部署成功；1 = 硬失败（CI 红、构建失败、健康失败已回滚）；2 = 用法或环境保护错误。
#
# 环境变量覆盖仅用于测试/排障（生产默认值即可）：
#   YXQ_DEPLOY_ROOT, YXQ_SRC_DIR, YXQ_LOG_FILE, YXQ_LOCK_FILE, YXQ_HEALTH_URL,
#   YXQ_REPO, YXQ_BRANCH, YXQ_SERVICE, YXQ_SERVICE_USER, YXQ_MIN_FREE_MB,
#   YXQ_HEALTH_TIMEOUT, YXQ_FAIL_BACKOFF, YXQ_MISSING_GRACE, YXQ_REQUIRED_JOBS,
#   YXQ_REQUIRE_DOCS, YXQ_SIMULATE_CI（仅 --dry-run）。
# 脚本不读取、不打印任何密钥；staging.env 由 systemd 注入 app，与本脚本无关。

set -Eeuo pipefail

readonly PROG=${0##*/}

ROOT=${YXQ_DEPLOY_ROOT:-/opt/yixianqian-next}
SRC=${YXQ_SRC_DIR:-$ROOT/src}
BIN=$ROOT/bin
LOG=${YXQ_LOG_FILE:-/var/log/yxq-autodeploy.log}
LOCK=${YXQ_LOCK_FILE:-/run/yxq-autodeploy.lock}
HEALTH_URL=${YXQ_HEALTH_URL:-http://127.0.0.1:8093/healthz}
REPO=${YXQ_REPO:-7310C7310C/yixianqian}
BRANCH=${YXQ_BRANCH:-main}
SERVICE=${YXQ_SERVICE:-yixianqian-next-test}
SVC_USER=${YXQ_SERVICE_USER:-yxq}
MIN_FREE_MB=${YXQ_MIN_FREE_MB:-1500}
HEALTH_TIMEOUT=${YXQ_HEALTH_TIMEOUT:-40}
FAIL_BACKOFF=${YXQ_FAIL_BACKOFF:-900}
MISSING_GRACE=${YXQ_MISSING_GRACE:-3600}
REQUIRED_JOBS=${YXQ_REQUIRED_JOBS:-lint-build unit integration}
REQUIRE_DOCS=${YXQ_REQUIRE_DOCS:-1}
GH_BIN=${YXQ_GH_BIN:-gh}
LOG_MAX_BYTES=${YXQ_LOG_MAX_BYTES:-20971520}
GO_BIN=${YXQ_GO_BIN:-go}
GOFMT_BIN=${YXQ_GOFMT_BIN:-gofmt}

DRY_RUN=0
FORCE=0
MODE=live

log() {
  local line
  line="$(date -Is) [$MODE] $*"
  printf '%s\n' "$line"
  printf '%s\n' "$line" >>"$LOG" 2>/dev/null || true
}

die() { log "错误：$*"; exit 2; }

usage() {
  sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
}

parse_args() {
  while (($# > 0)); do
    case "$1" in
      --dry-run) DRY_RUN=1; MODE=dry-run ;;
      --force) FORCE=1 ;;
      -h|--help) usage; exit 0 ;;
      *) die "未知参数：$1（可用 --dry-run/--force/--help）" ;;
    esac
    shift
  done
}

# 日志自截断（logrotate 之外的兜底）：超过上限时只保留最后 2000 行。
rotate_log() {
  [[ -f $LOG ]] || return 0
  local size
  size=$(stat -c %s "$LOG" 2>/dev/null || echo 0)
  ((size > LOG_MAX_BYTES)) || return 0
  local tmp
  tmp=$(mktemp "${LOG}.XXXXXX") || return 0
  tail -n 2000 "$LOG" >"$tmp" 2>/dev/null || true
  cat "$tmp" >"$LOG" 2>/dev/null || true
  rm -f "$tmp"
  log "日志超过 ${LOG_MAX_BYTES} 字节，已自截断为最后 2000 行"
}

# 读取 healthz，输出 "version status"；失败输出空。
health_probe() {
  local body
  body=$(curl -fsS --max-time 3 "$HEALTH_URL" 2>/dev/null) || return 1
  python3 -c 'import json,sys
try:
    d=json.loads(sys.stdin.read())
except Exception:
    raise SystemExit(1)
print(d.get("version",""), d.get("status",""))' <<<"$body"
}

# 等待 healthz 版本等于 $1；可选第二参数为期望 status（默认 ok）。
wait_health_version() {
  local want=$1 deadline now got
  deadline=$(($(date +%s) + HEALTH_TIMEOUT))
  while :; do
    got=$(health_probe || true)
    if [[ ${got%% *} == "$want" && ${got#* } == ok ]]; then
      return 0
    fi
    now=$(date +%s)
    ((now < deadline)) || return 1
    sleep 2
  done
}

wait_health_any_ok() {
  local deadline now got
  deadline=$(($(date +%s) + HEALTH_TIMEOUT))
  while :; do
    got=$(health_probe || true)
    if [[ -n $got && ${got#* } == ok ]]; then
      return 0
    fi
    now=$(date +%s)
    ((now < deadline)) || return 1
    sleep 2
  done
}

write_marker() {
  printf '%s\n' "$1" >"$ROOT/.deployed.sha.tmp" && mv -f "$ROOT/.deployed.sha.tmp" "$ROOT/deployed.sha"
}

record_failure() {
  printf '%s %s %s\n' "$REMOTE" "$(date +%s)" "$1" >"$ROOT/autodeploy.failed"
}

clear_failure() { rm -f "$ROOT/autodeploy.failed"; }

# 最近一次失败是否属于同一 SHA 且在退避窗口内。
failure_backoff_active() {
  [[ -f $ROOT/autodeploy.failed && $FORCE -eq 0 ]] || return 1
  local fsha fts freason now age
  read -r fsha fts freason <"$ROOT/autodeploy.failed" || return 1
  [[ $fsha == "$REMOTE" ]] || return 1
  now=$(date +%s)
  age=$((now - fts))
  if ((age < FAIL_BACKOFF)); then
    log "跳过：$SHORT 上次失败（${freason:-未知}）于 ${age}s 前，${FAIL_BACKOFF}s 内不重试；--force 可忽略"
    return 0
  fi
  return 1
}

gh_runs_json() {
  # NO_COLOR/GH_FORCE_TTY：gh 在 pty 下会给 --json 输出加 ANSI 颜色，影响解析。
  NO_COLOR=1 GH_FORCE_TTY=0 GH_PROMPT_DISABLED=1 \
    "$GH_BIN" run list --repo "$REPO" --commit "$REMOTE" --event push --limit 5 \
    --json workflowName,status,conclusion,headSha,databaseId 2>/dev/null
}

# 单个 workflow 的状态：ok/pending/fail/missing/error|原因|run id
workflow_state() {
  local name=$1 json out
  if ! json=$(gh_runs_json); then
    printf 'error|gh run list 调用失败|0\n'
    return 0
  fi
  out=$(python3 - "$json" "$name" <<'PY'
import json, re, sys

raw, name = re.sub(r"\x1b\[[0-9;]*m", "", sys.argv[1]), sys.argv[2]
try:
    runs = json.loads(raw)
except Exception:
    print("error|无法解析 gh run list 输出|0")
    raise SystemExit
mine = [r for r in runs if r.get("workflowName") == name]
if not mine:
    print("missing|无 %s run（可能 workflow 未触发）|0" % name)
    raise SystemExit
mine.sort(key=lambda r: r.get("databaseId") or 0)
run = mine[-1]
rid = run.get("databaseId") or 0
status, concl = run.get("status"), run.get("conclusion")
if status != "completed":
    print("pending|%s %s|%s" % (name, status, rid))
elif concl == "success":
    print("ok|%s success|%s" % (name, rid))
else:
    print("fail|%s %s|%s" % (name, concl, rid))
PY
)
  printf '%s\n' "$out"
}

# CI run 内 job 级校验：ok|原因
jobs_state() {
  local rid=$1 json out
  if ! json=$(NO_COLOR=1 GH_FORCE_TTY=0 "$GH_BIN" run view "$rid" --repo "$REPO" --json jobs 2>/dev/null); then
    printf 'error|gh run view 失败\n'
    return 0
  fi
  out=$(python3 - "$json" "$REQUIRED_JOBS" <<'PY'
import json, re, sys

raw, required = re.sub(r"\x1b\[[0-9;]*m", "", sys.argv[1]), sys.argv[2].split()
try:
    data = json.loads(raw)
except Exception:
    print("error|无法解析 jobs 输出")
    raise SystemExit
jobs = {j.get("name"): (j.get("conclusion") or j.get("status")) for j in data.get("jobs", [])}
bad = [n for n in required if jobs.get(n) != "success"]
if bad:
    print("fail|required job 未 success：%s" % ", ".join("%s=%s" % (n, jobs.get(n, "missing")) for n in bad))
else:
    print("ok|required jobs success：%s" % ", ".join(required))
PY
)
  printf '%s\n' "$out"
}

commit_age() {
  local ct
  ct=$(git -C "$SRC" -c safe.directory="$SRC" show -s --format=%ct "$1" 2>/dev/null) || { echo 999999999; return; }
  echo $(($(date +%s) - ct))
}

# 汇总 CI + Docs 闸门；结果写入全局 GATE/GATE_REASON。
check_gate() {
  GATE=wait
  GATE_REASON=""

  if [[ -n ${YXQ_SIMULATE_CI:-} ]]; then
    if ((DRY_RUN == 0)); then
      log "错误：YXQ_SIMULATE_CI 只允许与 --dry-run 一起使用，拒绝在实跑模式模拟"
      exit 2
    fi
    case "$YXQ_SIMULATE_CI" in
      ok) GATE=ok; GATE_REASON="（dry-run 模拟）CI/Docs 均 success" ;;
      fail) GATE=fail; GATE_REASON="（dry-run 模拟）CI 失败" ;;
      pending) GATE=wait; GATE_REASON="（dry-run 模拟）CI 仍在运行" ;;
      *) die "YXQ_SIMULATE_CI 取值只能是 ok|fail|pending" ;;
    esac
    return 0
  fi

  local ci docs jobs wf rid
  wf=$(workflow_state CI)
  IFS='|' read -r ci GATE_REASON rid <<<"$wf"
  case "$ci" in
    ok) ;;
    pending)
      GATE=wait
      return 0
      ;;
    fail)
      GATE=fail
      return 0
      ;;
    missing)
      if (($(commit_age "$REMOTE") > MISSING_GRACE)); then
        GATE=fail
        GATE_REASON="无 CI run 且提交已超过 ${MISSING_GRACE}s（workflow 未触发？）"
      else
        GATE=wait
        GATE_REASON="等待 CI run 出现"
      fi
      return 0
      ;;
    *)
      GATE=wait
      GATE_REASON="CI 状态未知：$GATE_REASON"
      return 0
      ;;
  esac

  local jobs
  IFS='|' read -r jobs GATE_REASON <<<"$(jobs_state "$rid")"
  if [[ $jobs != ok ]]; then
    if [[ $jobs == fail ]]; then GATE=fail; else GATE=wait; fi
    return 0
  fi

  if ((DOCS_CHANGED == 1 || REQUIRE_DOCS == 1)); then
    local docs_state docs_reason
    IFS='|' read -r docs_state docs_reason _ <<<"$(workflow_state Docs)"
    case "$docs_state" in
      ok)
        GATE=ok
        GATE_REASON="CI + Docs 均 success"
        ;;
      pending)
        GATE=wait
        GATE_REASON="Docs 仍在运行"
        ;;
      fail)
        GATE=fail
        GATE_REASON="Docs 失败：$docs_reason"
        ;;
      missing)
        if (($(commit_age "$REMOTE") > MISSING_GRACE)); then
          GATE=fail
          GATE_REASON="无 Docs run 且提交已超过 ${MISSING_GRACE}s"
        else
          GATE=wait
          GATE_REASON="等待 Docs run 出现"
        fi
        ;;
      *)
        GATE=wait
        GATE_REASON="Docs 状态未知：$docs_reason"
        ;;
    esac
    return 0
  fi

  GATE=ok
  GATE_REASON="CI success（docs 未变更，Docs 门禁按配置跳过）"
}

rollback() {
  log "开始回滚：用 server.prev 覆盖 server 并重启 $SERVICE"
  if [[ ! -s $BIN/server.prev ]]; then
    log "回滚失败：$BIN/server.prev 不存在或为空，需人工介入"
    return 1
  fi
  if ! cp -f "$BIN/server.prev" "$BIN/server"; then
    log "回滚失败：复制 server.prev 出错，需人工介入"
    return 1
  fi
  if ! systemctl restart "$SERVICE"; then
    log "回滚重启失败，需人工介入"
    return 1
  fi
  if wait_health_any_ok; then
    log "回滚完成：服务已恢复（healthz version=$(health_probe | awk '{print $1}')）"
    return 0
  fi
  log "回滚后健康检查仍失败，需人工介入"
  return 1
}

print_plan() {
  local count=$1
  log "DRY-RUN 计划（不执行任何改动）："
  log "  1) cd $SRC && git reset --hard $REMOTE"
  log "  2) $GOFMT_BIN -l .（必须为空）+ $GO_BIN vet ./..."
  log "  3) build server/seed/token/facebackfill → bin/*.next（version=staging-$SHORT）"
  log "  4) cp bin/server bin/server.prev；mv bin/server.next bin/server；chown $SVC_USER:$SVC_USER；chmod 755"
  log "  5) systemctl restart $SERVICE"
  log "  6) 轮询 $HEALTH_URL 等待 version=staging-$SHORT（最多 ${HEALTH_TIMEOUT}s）"
  log "  7) 成功写 deployed.sha；失败 cp server.prev → server 并重启回滚，不写 deployed.sha"
  log "DRY-RUN 结束：共 $count 个变更文件，无任何实际动作"
}

main() {
  parse_args "$@"
  rotate_log

  exec 9>"$LOCK" || die "无法创建锁文件 $LOCK"
  if ! flock -n 9; then
    log "已有 autodeploy 实例在运行，本次跳过"
    exit 0
  fi

  [[ -d $SRC/.git ]] || die "$SRC 不是 git 仓库"
  [[ -d $BIN ]] || die "$BIN 不存在"

  log "开始检查：repo=$REPO branch=$BRANCH（$MODE）"
  if ! git -C "$SRC" -c safe.directory="$SRC" fetch --quiet origin "$BRANCH"; then
    log "git fetch 失败，等待下一轮"
    exit 0
  fi
  REMOTE=$(git -C "$SRC" -c safe.directory="$SRC" rev-parse "origin/$BRANCH")
  if [[ -n ${YXQ_FAKE_REMOTE:-} ]]; then
    if ((DRY_RUN == 0)); then
      die "YXQ_FAKE_REMOTE 只允许与 --dry-run 一起使用，拒绝在实跑模式伪造提交"
    fi
    REMOTE=$YXQ_FAKE_REMOTE
    log "测试模式：把 origin/$BRANCH 当作 $REMOTE"
  fi
  SHORT=${REMOTE:0:7}

  local DEPLOYED=""
  if [[ -f $ROOT/deployed.sha ]]; then
    DEPLOYED=$(tr -d '[:space:]' <"$ROOT/deployed.sha")
  else
    # 首次启用：能从 healthz 推出当前版本就不重复部署。
    local hv
    hv=$(health_probe 2>/dev/null | awk '{print $1}' || true)
    if [[ $hv == staging-* ]]; then
      local cand=${hv#staging-}
      if git -C "$SRC" -c safe.directory="$SRC" rev-parse --verify --quiet "$cand^{commit}" >/dev/null; then
        DEPLOYED=$(git -C "$SRC" -c safe.directory="$SRC" rev-parse "$cand^{commit}")
        log "缺少 deployed.sha，按运行版本 $hv 推断为 $DEPLOYED"
        if ((DRY_RUN == 0)); then
          write_marker "$DEPLOYED"
        fi
      fi
    fi
  fi

  if [[ -n $DEPLOYED && $DEPLOYED == "$REMOTE" ]]; then
    log "已是最新（$SHORT），无动作"
    exit 0
  fi

  if failure_backoff_active; then
    exit 0
  fi

  local CHANGED=""
  if [[ -n $DEPLOYED ]]; then
    CHANGED=$(git -C "$SRC" -c safe.directory="$SRC" diff --name-only "$DEPLOYED..$REMOTE")
  else
    CHANGED=$(git -C "$SRC" -c safe.directory="$SRC" ls-tree -r --name-only "$REMOTE")
    log "无部署标记，按全量变更处理"
  fi

  local f count=0
  CODE_CHANGED=0
  DOCS_CHANGED=0
  while IFS= read -r f; do
    [[ -n $f ]] || continue
    count=$((count + 1))
    case "$f" in
      docs/*|*.md|.team/*) DOCS_CHANGED=1 ;;
      *) CODE_CHANGED=1 ;;
    esac
  done <<<"$CHANGED"

  log "待部署：$SHORT（已部署 ${DEPLOYED:0:7}${DEPLOYED:+…}），变更 $count 个文件：code=$CODE_CHANGED docs=$DOCS_CHANGED"

  if ((CODE_CHANGED == 0)); then
    log "变更仅含 docs/*、*.md、.team/*：只更新 deployed.sha，不构建不重启"
    if ((DRY_RUN == 1)); then
      log "DRY-RUN：将写入 deployed.sha=$REMOTE（docs-only）"
    else
      write_marker "$REMOTE"
      clear_failure
      log "已更新 deployed.sha=$REMOTE（未重启服务）"
    fi
    exit 0
  fi

  log "CI 闸门检查中（要求 $REQUIRED_JOBS；Docs=$REQUIRE_DOCS）"
  check_gate
  case "$GATE" in
    ok) log "CI 闸门通过：$GATE_REASON" ;;
    wait)
      log "CI 闸门未就绪：$GATE_REASON；等待下一轮"
      exit 0
      ;;
    *)
      log "CI 闸门失败：$GATE_REASON；不部署（已记录，${FAIL_BACKOFF}s 内不重试）"
      if ((DRY_RUN == 0)); then
        record_failure "CI: $GATE_REASON"
      fi
      exit 1
      ;;
  esac

  if ((DRY_RUN == 1)); then
    print_plan "$count"
    exit 0
  fi

  local free_mb
  free_mb=$(df -Pm "$ROOT" | awk 'NR==2{print $4}')
  if ((free_mb < MIN_FREE_MB)); then
    log "磁盘可用 ${free_mb}MB < ${MIN_FREE_MB}MB，拒绝部署（避免写满根分区）"
    record_failure "磁盘不足"
    exit 1
  fi

  local dirty
  dirty=$(git -C "$SRC" -c safe.directory="$SRC" status --porcelain --untracked-files=no)
  if [[ -n $dirty ]]; then
    log "源码目录有未提交改动，拒绝自动部署（请人工处理）："
    log "$dirty"
    record_failure "src 有未提交改动"
    exit 1
  fi

  if ! git -C "$SRC" -c safe.directory="$SRC" reset --hard --quiet "$REMOTE"; then
    log "git reset --hard $REMOTE 失败，不部署"
    record_failure "git reset 失败"
    exit 1
  fi
  log "源码已同步到 $REMOTE"

  local fmt
  fmt=$(cd "$SRC" && "$GOFMT_BIN" -l . 2>/dev/null || true)
  if [[ -n $fmt ]]; then
    log "gofmt 未通过，不部署："
    log "$fmt"
    record_failure "gofmt"
    exit 1
  fi
  if ! (cd "$SRC" && "$GO_BIN" vet ./... >/dev/null 2>&1); then
    log "go vet 失败，不部署"
    record_failure "go vet"
    exit 1
  fi

  local VERSION="staging-$SHORT"
  log "构建 $VERSION（server/seed/token/facebackfill）"
  local app
  if ! (cd "$SRC" && "$GO_BIN" build -buildvcs=false -trimpath \
        -ldflags "-X github.com/7310C7310C/yixianqian/internal/platform.Version=$VERSION" \
        -o "$BIN/server.next" ./cmd/server); then
    log "构建 server 失败，不切换"
    rm -f "$BIN"/server.next
    record_failure "build server"
    exit 1
  fi
  for app in seed token facebackfill; do
    if ! (cd "$SRC" && "$GO_BIN" build -buildvcs=false -trimpath -o "$BIN/$app.next" "./cmd/$app"); then
      log "构建 $app 失败，不切换"
      rm -f "$BIN"/server.next "$BIN"/*.next
      record_failure "build $app"
      exit 1
    fi
  done

  if ! chown "$SVC_USER:$SVC_USER" "$BIN"/*.next || ! chmod 755 "$BIN"/*.next; then
    log "设置 bin 权限失败，不切换"
    rm -f "$BIN"/*.next
    record_failure "chown/chmod"
    exit 1
  fi

  if [[ ! -s $BIN/server ]]; then
    log "当前 bin/server 不存在，无法备份 server.prev，拒绝部署"
    rm -f "$BIN"/*.next
    record_failure "缺少当前 server"
    exit 1
  fi
  cp -f "$BIN/server" "$BIN/server.prev"

  log "切换二进制并重启 $SERVICE"
  mv -f "$BIN/server.next" "$BIN/server"
  for app in seed token facebackfill; do
    mv -f "$BIN/$app.next" "$BIN/$app"
  done

  if ! systemctl restart "$SERVICE"; then
    log "systemctl restart 失败，回滚"
    rollback || true
    record_failure "restart 失败"
    exit 1
  fi

  if ! wait_health_version "$VERSION"; then
    local got
    got=$(health_probe 2>/dev/null || true)
    log "健康检查失败：期望 version=$VERSION status=ok，实际 '${got:-无响应}'"
    rollback || true
    record_failure "healthz 未匹配"
    exit 1
  fi

  write_marker "$REMOTE"
  clear_failure
  log "部署成功：$SHORT（version=$VERSION，healthz ok）"
  exit 0
}

main "$@"
