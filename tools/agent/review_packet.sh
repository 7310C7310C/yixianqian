#!/usr/bin/env bash
# Build a compact review packet for an independent Reviewer.
# Does not run tests; the Reviewer decides what to re-run.
set -uo pipefail

ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
cd "$ROOT" || exit 1
BASE=${1:-origin/main}
OUT=${2:-}

if ! git rev-parse --verify --quiet "$BASE" >/dev/null 2>&1; then
  BASE=HEAD~1
fi

tmp=$(mktemp)
{
  echo "# Review packet"
  echo
  echo "- repo: $ROOT"
  echo "- branch: $(git rev-parse --abbrev-ref HEAD)"
  echo "- HEAD: $(git rev-parse HEAD)"
  echo "- base: $BASE"
  echo "- generated: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo
  echo "## Diffstat"
  echo '```'
  git diff --stat "$BASE" 2>/dev/null || true
  echo '```'
  echo
  echo "## Changed files"
  echo '```'
  git diff --name-only "$BASE" 2>/dev/null || true
  echo '```'
  echo
  echo "## Risk flags"
  files=$(git diff --name-only "$BASE" 2>/dev/null || true)
  flag() { printf '%s\n' "$files" | grep -qE "$1" && echo "- $2"; }
  flag '^(internal/auth/|internal/httpserver/.*auth|internal/db/.*(user|session|token))' "auth/session/permission"
  flag '^(internal/db/|migrations/)' "data/schema"
  flag '^(deploy/|\.github/workflows/)' "deploy/CI gate"
  flag '^(internal/media/|internal/face/)' "media/face"
  flag '^(web/static/|web/templates/)' "frontend/user-visible"
  flag '(secret|password|token|cookie)' "secret-adjacent filename (manual check)"
  echo
  echo "## Suggested review commands"
  echo '```sh'
  echo "make fast"
  echo "make ci"
  echo "bash deploy/autodeploy/test_autodeploy.sh"
  echo "make smoke-fast   # only if user-visible; run on server1 when possible"
  echo '```'
  echo
  echo "## Evidence to attach"
  echo '- [ ] `make fast` output'
  echo '- [ ] `make ci` output'
  echo '- [ ] targeted smoke / staging healthz (if user-visible)'
  echo '- [ ] rollback note'
} >"$tmp"

if [[ -n $OUT ]]; then
  mkdir -p "$(dirname "$OUT")"
  cp "$tmp" "$OUT"
  echo "review packet: $OUT"
else
  cat "$tmp"
fi
rm -f "$tmp"
