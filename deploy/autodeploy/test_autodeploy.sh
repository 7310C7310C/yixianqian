#!/usr/bin/env bash
# autodeploy.sh 沙箱单测：用临时 git 远端 + 假 gh/go/gofmt/curl/df/systemctl 覆盖全部分支。
# 不接触生产 /opt、不重启真实服务、不访问网络。
#
# 运行：bash deploy/autodeploy/test_autodeploy.sh [-v]
set -uo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# 仓库内是 deploy/autodeploy/；在 server 上安装为 /opt/yixianqian-next/tests/（脚本在上层）。
if [[ -x $HERE/autodeploy.sh ]]; then
  AUTODEPLOY=$HERE/autodeploy.sh
else
  AUTODEPLOY=$(cd "$HERE/.." && pwd)/autodeploy.sh
fi
VERBOSE=${VERBOSE:-0}
[[ ${1:-} == -v ]] && VERBOSE=1

PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); printf 'PASS  %s\n' "$1"; }
bad() {
  FAIL=$((FAIL + 1))
  printf 'FAIL  %s\n' "$1"
  [[ -n ${2:-} ]] && printf '      %s\n' "$2"
}
assert_eq() { [[ $1 == "$2" ]] && ok "$3" || bad "$3" "期望 '$1'，实际 '$2'"; }
assert_contains() { grep -qF -- "$1" "$2" 2>/dev/null && ok "$3" || bad "$3" "在 $2 未找到 '$1'"; }
assert_no_file() { [[ ! -e $1 ]] && ok "$2" || bad "$2" "不应存在：$1"; }
assert_empty_or_absent() { [[ ! -s $1 ]] && ok "$2" || bad "$2" "$1 非空：$(cat "$1")"; }

SB=''
setup() {
  SB=$(mktemp -d /tmp/yxq-autodeploy-test.XXXXXX)
  mkdir -p "$SB/root/bin" "$SB/fakebin" "$SB/home"
  printf 'OLD-BINARY\n' >"$SB/root/bin/server"
  printf 'OLD-SEED\n' >"$SB/root/bin/seed"
  : >"$SB/log"

  # 假远端 + 工作副本
  git init -q --bare -b main "$SB/origin.git"
  git init -q -b main "$SB/work"
  git -C "$SB/work" config user.email test@example.com
  git -C "$SB/work" config user.name test
  printf 'package main\n' >"$SB/work/main.go"
  mkdir -p "$SB/work/docs" "$SB/work/cmd/server"
  printf '# 文档\n' >"$SB/work/docs/a.md"
  git -C "$SB/work" add -A
  git -C "$SB/work" commit -qm '初始提交'
  git -C "$SB/work" remote add origin "$SB/origin.git"
  git -C "$SB/work" push -q origin main
  C1=$(git -C "$SB/work" rev-parse HEAD)

  git clone -q "$SB/origin.git" "$SB/root/src"
  printf '%s\n' "$C1" >"$SB/root/deployed.sha"
  write_health "staging-${C1:0:7}"

  cat >"$SB/fakebin/go" <<'EOF'
#!/usr/bin/env bash
SB=$(cd "$(dirname "$0")/.." && pwd)
printf 'go %s\n' "$*" >>"$SB/go.log"
out=''; prev=''
for a in "$@"; do [[ $prev == -o ]] && out=$a; prev=$a; done
if [[ -n $out ]]; then printf '#!/bin/sh\necho fake-binary\n' >"$out"; chmod 755 "$out"; fi
exit 0
EOF
  cat >"$SB/fakebin/gofmt" <<'EOF'
#!/usr/bin/env bash
[[ ${GOFMT_DIRTY:-0} == 1 ]] && echo main.go
exit 0
EOF
  cat >"$SB/fakebin/systemctl" <<'EOF'
#!/usr/bin/env bash
SB=$(cd "$(dirname "$0")/.." && pwd)
printf 'systemctl %s\n' "$*" >>"$SB/systemctl.log"
exit 0
EOF
  cat >"$SB/fakebin/curl" <<'EOF'
#!/usr/bin/env bash
SB=$(cd "$(dirname "$0")/.." && pwd)
[[ ${CURL_FAIL:-0} == 1 ]] && exit 7
cat "$SB/health.json"
EOF
  cat >"$SB/fakebin/df" <<'EOF'
#!/usr/bin/env bash
printf 'Filesystem 1M-blocks Used Available Capacity Mounted on\n/dev/vda2 58000 50000 %s 95%% /\n' "${DF_FREE_MB:-5000}"
EOF
  cat >"$SB/fakebin/gh" <<'EOF'
#!/usr/bin/env bash
SB=$(cd "$(dirname "$0")/.." && pwd)
printf 'gh %s\n' "$*" >>"$SB/gh.log"
sha=''; prev=''
for a in "$@"; do [[ $prev == --commit ]] && sha=$a; prev=$a; done
if [[ ${1:-} == run && ${2:-} == list ]]; then
  out='['
  first=1
  ci_status=${GH_CI_STATUS:-completed}
  ci_concl=${GH_CI_CONCL:-success}
  if [[ ${GH_CI:-ok} != missing ]]; then
    [[ $first == 0 ]] && out+=','
    out+=$(printf '{"workflowName":"CI","status":"%s","conclusion":"%s","headSha":"%s","databaseId":101}' \
      "$ci_status" "$ci_concl" "$sha")
    first=0
  fi
  ds_status=${GH_DOCS_STATUS:-completed}
  ds_concl=${GH_DOCS_CONCL:-success}
  if [[ ${GH_DOCS:-ok} != missing ]]; then
    [[ $first == 0 ]] && out+=','
    out+=$(printf '{"workflowName":"Docs","status":"%s","conclusion":"%s","headSha":"%s","databaseId":202}' \
      "$ds_status" "$ds_concl" "$sha")
  fi
  out+=']'
  printf '%s\n' "$out"
  exit 0
fi
if [[ ${1:-} == run && ${2:-} == view ]]; then
  if [[ ${GH_JOBS:-ok} == fail ]]; then
    printf '%s\n' '{"jobs":[{"name":"lint-build","conclusion":"success"},{"name":"unit","conclusion":"failure"},{"name":"integration","conclusion":"success"}]}'
  else
    printf '%s\n' '{"jobs":[{"name":"lint-build","conclusion":"success"},{"name":"unit","conclusion":"success"},{"name":"integration","conclusion":"success"}]}'
  fi
  exit 0
fi
printf '[]\n'
EOF
  chmod 755 "$SB/fakebin/"*
}

write_health() {
  printf '{"db":"ok","status":"ok","time":"2026-10-24T00:00:00Z","version":"%s"}\n' "$1" >"$SB/health.json"
}

reset_run_logs() {
  : >"$SB/log"
  : >"$SB/go.log"
  : >"$SB/systemctl.log"
  : >"$SB/gh.log"
}

run_ad() {
  env -i \
    PATH="$SB/fakebin:/usr/local/bin:/usr/bin:/bin" \
    HOME="$SB/home" \
    YXQ_DEPLOY_ROOT="$SB/root" \
    YXQ_SRC_DIR="$SB/root/src" \
    YXQ_LOG_FILE="$SB/log" \
    YXQ_LOCK_FILE="$SB/lock" \
    YXQ_HEALTH_URL="http://127.0.0.1:9/healthz" \
    YXQ_SERVICE="yxq-test" \
    YXQ_SERVICE_USER="$(id -un)" \
    YXQ_MIN_FREE_MB=1 \
    YXQ_HEALTH_TIMEOUT=4 \
    YXQ_FAIL_BACKOFF=900 \
    YXQ_GH_BIN="$SB/fakebin/gh" \
    YXQ_GO_BIN="$SB/fakebin/go" \
    YXQ_GOFMT_BIN="$SB/fakebin/gofmt" \
    GH_CI="${GH_CI:-ok}" \
    GH_CI_STATUS="${GH_CI_STATUS:-completed}" \
    GH_CI_CONCL="${GH_CI_CONCL:-success}" \
    GH_DOCS="${GH_DOCS:-ok}" \
    GH_DOCS_STATUS="${GH_DOCS_STATUS:-completed}" \
    GH_DOCS_CONCL="${GH_DOCS_CONCL:-success}" \
    GH_JOBS="${GH_JOBS:-ok}" \
    GOFMT_DIRTY="${GOFMT_DIRTY:-0}" \
    CURL_FAIL="${CURL_FAIL:-0}" \
    DF_FREE_MB="${DF_FREE_MB:-5000}" \
    ${YXQ_FAKE_REMOTE:+YXQ_FAKE_REMOTE="$YXQ_FAKE_REMOTE"} \
    ${YXQ_SIMULATE_CI:+YXQ_SIMULATE_CI="$YXQ_SIMULATE_CI"} \
    bash "$AUTODEPLOY" "$@"
}

commit_new() { # $1=type(code|docs) $2=message [$3=noreview]
  local file
  if [[ $1 == docs ]]; then
    file="docs/doc-$(date +%s%N).md"
  else
    file="main.go"
  fi
  printf '%s %s\n' "$1" "$(date +%s%N)" >>"$SB/work/$file"
  git -C "$SB/work" add -A
  if [[ $1 == code && ${3:-} != noreview ]]; then
    git -C "$SB/work" commit -qm "$2" -m 'Reviewed-By: reviewer'
  else
    git -C "$SB/work" commit -qm "$2"
  fi
  git -C "$SB/work" push -q origin main
  git -C "$SB/work" rev-parse HEAD
}

echo "== autodeploy 沙箱单测 =="
setup

# 1) 已部署 == origin：不动作
reset_run_logs
run_ad --dry-run
rc=$?
assert_eq 0 "$rc" "S1 已最新时退出码 0"
assert_contains "已是最新" "$SB/log" "S1 日志说明已最新"
assert_empty_or_absent "$SB/go.log" "S1 未调用 go"
assert_empty_or_absent "$SB/systemctl.log" "S1 未重启服务"

# 2) 落后一个提交 + dry-run：只打印计划，不执行
C2=$(commit_new code '第二次提交')
reset_run_logs
run_ad --dry-run
rc=$?
assert_eq 0 "$rc" "S2 dry-run 退出码 0"
assert_contains "DRY-RUN 计划" "$SB/log" "S2 打印部署计划"
assert_contains "staging-${C2:0:7}" "$SB/log" "S2 计划包含目标版本"
assert_eq "$C1" "$(cat "$SB/root/deployed.sha")" "S2 未更新 deployed.sha"
assert_eq "OLD-BINARY" "$(cat "$SB/root/bin/server")" "S2 未替换二进制"
assert_empty_or_absent "$SB/go.log" "S2 未构建"
assert_empty_or_absent "$SB/systemctl.log" "S2 未重启"

# 3) docs-only：只更新标记，不构建不重启（先把 C2 标记为已部署，模拟上一轮已部署）
printf '%s\n' "$C2" >"$SB/root/deployed.sha"
printf 'yaml\n' >"$SB/work/docs/only.md"
git -C "$SB/work" add -A
git -C "$SB/work" commit -qm '只改文档'
git -C "$SB/work" push -q origin main
C3=$(git -C "$SB/work" rev-parse HEAD)
reset_run_logs
run_ad
rc=$?
assert_eq 0 "$rc" "S3 docs-only 退出码 0"
assert_eq "$C3" "$(cat "$SB/root/deployed.sha")" "S3 已更新 deployed.sha"
assert_contains "只更新 deployed.sha" "$SB/log" "S3 日志说明只更新标记"
assert_empty_or_absent "$SB/go.log" "S3 docs-only 不构建"
assert_empty_or_absent "$SB/systemctl.log" "S3 docs-only 不重启"

# 4) CI 红：不部署，退出码 1
C4=$(commit_new code '会红的提交')
reset_run_logs
GH_CI_CONCL=failure run_ad
rc=$?
assert_eq 1 "$rc" "S4 CI 红退出码 1"
assert_contains "CI 闸门失败" "$SB/log" "S4 记录 CI 闸门失败"
assert_eq "$C3" "$(cat "$SB/root/deployed.sha")" "S4 未更新 deployed.sha"
assert_eq "OLD-BINARY" "$(cat "$SB/root/bin/server")" "S4 未替换二进制"
assert_empty_or_absent "$SB/go.log" "S4 CI 红不构建"
assert_empty_or_absent "$SB/systemctl.log" "S4 CI 红不重启"

# 5) CI 未完成：等待下一轮，退出码 0
reset_run_logs
GH_CI_STATUS=in_progress run_ad --force
rc=$?
assert_eq 0 "$rc" "S5 CI 未完成退出码 0"
assert_contains "等待下一轮" "$SB/log" "S5 日志说明等待"
assert_empty_or_absent "$SB/go.log" "S5 未构建"
assert_empty_or_absent "$SB/systemctl.log" "S5 未重启"

# 6) run 级 success 但 unit job 失败：不部署
reset_run_logs
GH_JOBS=fail run_ad --force
rc=$?
assert_eq 1 "$rc" "S6 job 失败退出码 1"
assert_contains "required job 未 success" "$SB/log" "S6 报出失败 job"
assert_eq "$C3" "$(cat "$SB/root/deployed.sha")" "S6 未更新 deployed.sha"
assert_empty_or_absent "$SB/systemctl.log" "S6 未重启"

# 7) 健康检查失败：自动回滚，标记不变
C7=$(commit_new code '会导致健康失败')
reset_run_logs
write_health "staging-${C3:0:7}" # 新二进制永远不会匹配
run_ad --force
rc=$?
assert_eq 1 "$rc" "S7 健康失败退出码 1"
assert_contains "健康检查失败" "$SB/log" "S7 记录健康失败"
assert_contains "回滚完成" "$SB/log" "S7 回滚完成"
assert_eq "OLD-BINARY" "$(cat "$SB/root/bin/server")" "S7 server 已回滚"
assert_eq "$C3" "$(cat "$SB/root/deployed.sha")" "S7 deployed.sha 未变"
assert_eq 2 "$(grep -c 'systemctl restart' "$SB/systemctl.log")" "S7 重启两次（部署+回滚）"

# 8) 正常部署成功
C8=$(commit_new code '正常部署')
reset_run_logs
write_health "staging-${C8:0:7}"
run_ad
rc=$?
assert_eq 0 "$rc" "S8 部署成功退出码 0"
assert_eq "$C8" "$(cat "$SB/root/deployed.sha")" "S8 deployed.sha 已更新"
assert_contains "部署成功" "$SB/log" "S8 日志记录成功"
assert_eq 1 "$(grep -c 'systemctl restart' "$SB/systemctl.log")" "S8 只重启一次"
[[ -x $SB/root/bin/seed && -x $SB/root/bin/token && -x $SB/root/bin/facebackfill && -x $SB/root/bin/thumbwarm ]] \
  && ok "S8 seed/token/facebackfill/thumbwarm 已安装" || bad "S8 CLI 未安装"
assert_contains "fake-binary" "$SB/root/bin/server" "S8 server 已替换（占位内容）"

# 9) 单实例：持锁时第二次调用直接跳过
(
  exec 9>"$SB/lock"
  flock 9
  sleep 3
) &
HOLDER=$!
sleep 1
reset_run_logs
run_ad --dry-run
rc=$?
assert_eq 0 "$rc" "S9 抢锁失败退出码 0"
assert_contains "已有 autodeploy 实例" "$SB/log" "S9 日志说明已有实例"
assert_empty_or_absent "$SB/gh.log" "S9 未访问 gh"
kill "$HOLDER" 2>/dev/null
wait "$HOLDER" 2>/dev/null

# 10) 源码脏：拒绝自动部署
C10=$(commit_new code '脏树测试')
printf 'local hotfix\n' >>"$SB/root/src/main.go"
reset_run_logs
write_health "staging-${C10:0:7}"
run_ad --force
rc=$?
assert_eq 1 "$rc" "S10 脏树退出码 1"
assert_contains "未提交改动" "$SB/log" "S10 日志说明脏树"
assert_eq "$C8" "$(cat "$SB/root/deployed.sha")" "S10 未更新 deployed.sha"
assert_empty_or_absent "$SB/systemctl.log" "S10 未重启"
git -C "$SB/root/src" checkout -- . 2>/dev/null

# 11) dry-run 模拟 CI 红：只报告不执行
reset_run_logs
YXQ_SIMULATE_CI=fail run_ad --dry-run --force
rc=$?
assert_eq 1 "$rc" "S11 模拟 CI 红退出码 1"
assert_contains "模拟" "$SB/log" "S11 日志标注模拟"
assert_empty_or_absent "$SB/go.log" "S11 未构建"
assert_empty_or_absent "$SB/systemctl.log" "S11 未重启"
assert_eq "$C8" "$(cat "$SB/root/deployed.sha")" "S11 未更新 deployed.sha"

# 12) 模拟参数不带 --dry-run：拒绝执行
reset_run_logs
YXQ_SIMULATE_CI=fail run_ad --force
rc=$?
assert_eq 2 "$rc" "S12 实跑禁止模拟退出码 2"
assert_empty_or_absent "$SB/systemctl.log" "S12 未重启"

# 13) 代码提交缺 Reviewed-By：拒绝部署
C13=$(commit_new code '缺少 Reviewed-By' noreview)
reset_run_logs
run_ad
rc=$?
assert_eq 1 "$rc" "S13 缺 Reviewed-By 退出码 1"
assert_contains "缺少 Reviewed-By trailer" "$SB/log" "S13 日志说明部署闸拒绝"
assert_eq "$C8" "$(cat "$SB/root/deployed.sha")" "S13 未更新 deployed.sha"
assert_empty_or_absent "$SB/go.log" "S13 未构建"
assert_empty_or_absent "$SB/systemctl.log" "S13 未重启"

# 14) --force 例外：缺 Reviewed-By 仍可 dry-run 计划（提示警告）
reset_run_logs
run_ad --dry-run --force
rc=$?
assert_eq 0 "$rc" "S14 --force 例外退出码 0"
assert_contains "缺少 Reviewed-By trailer；--force 例外" "$SB/log" "S14 记录 --force 例外"
assert_contains "DRY-RUN 计划" "$SB/log" "S14 仍打印部署计划"
assert_empty_or_absent "$SB/go.log" "S14 未构建"
assert_empty_or_absent "$SB/systemctl.log" "S14 未重启"

echo
printf '总计：%d PASS / %d FAIL\n' "$PASS" "$FAIL"
if ((FAIL > 0)); then
  echo "沙箱目录保留：$SB"
  exit 1
fi
rm -rf "$SB"
exit 0
