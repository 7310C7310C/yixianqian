#!/usr/bin/env bash
# Wait for CI + staging autodeploy in one blocking command.
#
# Replaces the model-side "poll gh run list / curl healthz every 10s" loop.
# Handles the docs-only tip case: CI does not run for docs-only pushes, so the
# expected health version is the push tip (autodeploy builds REMOTE, including docs/test-only tips).
set -uo pipefail

REPO=${REPO:-7310C7310C/yixianqian}
HEALTH_URL=${HEALTH_URL:-https://next.nantou.love/healthz}
CI_TIMEOUT=${CI_TIMEOUT:-1200}
HEALTH_TIMEOUT=${HEALTH_TIMEOUT:-900}
POLL=${POLL:-10}
SHA=${1:-$(git rev-parse HEAD 2>/dev/null || true)}

die() { echo "deploy-wait: $*" >&2; exit 1; }
log() { printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*"; }

[[ -n $SHA ]] || die "usage: tools/agent/deploy_wait.sh [sha]"
# gh run list --commit 需要完整 SHA；短 SHA 会返回空。
SHA=$(git rev-parse "$SHA^{commit}" 2>/dev/null) || die "unknown sha: $SHA"

# 非 docs 提交？
has_code() {
  local sha=$1 f
  while IFS= read -r f; do
    [[ -n $f ]] || continue
    case "$f" in
      docs/*|*.md|.team/*) ;;
      *) return 0 ;;
    esac
  done < <(git diff-tree --no-commit-id --name-only -r -m "$sha" 2>/dev/null)
  return 1
}

# 从 tip 往前找最近的 code commit（最多 50 个）。
ci_sha=$SHA
if ! has_code "$ci_sha"; then
  found=""
  while IFS= read -r cand; do
    [[ -n $cand ]] || continue
    if has_code "$cand"; then
      found=$cand
      break
    fi
  done < <(git rev-list -n 50 "$SHA" 2>/dev/null)
  [[ -n $found ]] || die "找不到 $SHA 之前的 code commit"
  ci_sha=$found
  log "tip $SHA 是 docs-only；用 code commit ${ci_sha:0:7} 等 CI"
fi

if ! command -v gh >/dev/null 2>&1 || ! gh auth status >/dev/null 2>&1; then
  log "gh 不可用/未登录；跳过 CI，直接等 healthz=${SHA:0:7}"
else
  log "等待 CI：${ci_sha:0:7}（最多 ${CI_TIMEOUT}s）"
  deadline=$((SECONDS + CI_TIMEOUT))
  while ((SECONDS < deadline)); do
    state=$(
      NO_COLOR=1 GH_FORCE_TTY=0 gh run list --repo "$REPO" --commit "$ci_sha" \
        --event push --limit 10 --json workflowName,status,conclusion,databaseId \
      | python3 -c '
import json, sys
try:
    runs = json.load(sys.stdin)
except Exception:
    print("error|bad-json")
    raise SystemExit
runs = [r for r in runs if r.get("workflowName") == "CI"]
if not runs:
    print("missing|")
    raise SystemExit
r = sorted(runs, key=lambda x: x.get("databaseId") or 0)[-1]
print("%s|%s" % (r.get("status"), r.get("conclusion") or ""))
' 2>/dev/null || echo "error|"
    )
    case "$state" in
      "completed|success")
        log "CI success"
        break
        ;;
      "completed|failure"|"completed|cancelled"|"completed|timed_out"|"completed|action_required")
        die "CI $state"
        ;;
      "missing|")
        log "CI run 尚未出现"
        ;;
      in_progress\|*|queued\|*|requested\|*|waiting\|*)
        log "CI $state"
        ;;
      *)
        log "CI 状态未知：$state"
        ;;
    esac
    sleep "$POLL"
  done
  ((SECONDS < deadline)) || die "CI 超时（${CI_TIMEOUT}s）"
fi

# autodeploy 的 CODE_CHANGED 口径：docs/*、*.md、.team/* 以外的任何文件都算代码，
# 会触发重建+重启，healthz 版本 = tip（$SHA）。只有 docs-only 才不重启。
is_docs_path() {
  case "$1" in
    docs/*|*.md|.team/*) return 0 ;;
    *) return 1 ;;
  esac
}

range_has_code() {
  local a=$1 b=$2 f
  while IFS= read -r f; do
    [[ -n $f ]] || continue
    is_docs_path "$f" || return 0
  done < <(git diff --name-only "$a..$b" 2>/dev/null || true)
  return 1
}

body=$(curl -fsS --max-time 5 "$HEALTH_URL" 2>/dev/null || true)
current_sha=$(printf '%s' "$body" | grep -o 'staging-[0-9a-f]\{7,\}' | head -1 | sed 's/^staging-//')
needs_wait=0
if [[ -n $current_sha && $current_sha != "$SHA" ]] && git rev-parse --verify --quiet "$current_sha^{commit}" >/dev/null 2>&1; then
  if range_has_code "$current_sha" "$SHA"; then
    needs_wait=1
  fi
elif [[ $current_sha != "$SHA" ]]; then
  # healthz 不可达/版本不在本地 repo：无法证明无需部署，保守等待。
  needs_wait=1
fi

if ((needs_wait == 0)); then
  log "无需等新版本（${current_sha:-unknown}..${SHA:0:7} 无 code 变更，或已是最新）；healthz=${body:-<no response>}"
  exit 0
fi

log "等待 healthz=${SHA:0:7}（最多 ${HEALTH_TIMEOUT}s）"
deadline=$((SECONDS + HEALTH_TIMEOUT))
while ((SECONDS < deadline)); do
  body=$(curl -fsS --max-time 5 "$HEALTH_URL" 2>/dev/null || true)
  if [[ $body == *"staging-${SHA:0:7}"* ]]; then
    log "PASS healthz=$body"
    exit 0
  fi
  log "healthz=${body:-<no response>}"
  sleep "$POLL"
done

die "healthz 超时，期望 staging-${SHA:0:7}"
