#!/usr/bin/env bash
# Agent fast path: run only checks affected by the working-tree change.
#
# This is deliberately NOT a replacement for `make ci`. It is the <30s loop
# used after each edit. `make ci` remains the pre-push / CI-equivalent gate.
set -uo pipefail

ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
cd "$ROOT" || exit 1

# The opencode host sometimes starts without /usr/local/go/bin in PATH.
if ! command -v go >/dev/null 2>&1 && [[ -x /usr/local/go/bin/go ]]; then
  export PATH=/usr/local/go/bin:$PATH
fi

BASE=${BASE:-origin/main}
if ! git rev-parse --verify --quiet "$BASE" >/dev/null 2>&1; then
  BASE=HEAD
fi

changed=$(
  {
    git diff --name-only "$BASE"...HEAD 2>/dev/null || true
    git diff --name-only 2>/dev/null || true
    git diff --cached --name-only 2>/dev/null || true
  } | sed '/^$/d' | sort -u
)

if [[ -z $changed ]]; then
  echo "fast: no changes vs $BASE (still checking gofmt)"
fi

fail=0
step() { printf '\n== %s ==\n' "$1"; }
run() {
  step "$1"
  shift
  if "$@"; then
    return 0
  fi
  fail=1
}

# 1) gofmt on changed Go files (full-repo gofmt is still cheap).
mapfile -t go_files < <(printf '%s\n' "$changed" | grep -E '\.go$' || true)
existing_go=()
for f in "${go_files[@]}"; do
  [[ -f $f ]] && existing_go+=("$f")
done
if ((${#existing_go[@]} > 0)); then
  step "gofmt (changed)"
  out=$(gofmt -l "${existing_go[@]}")
  if [[ -n $out ]]; then
    printf 'gofmt needed:\n%s\n' "$out"
    fail=1
  fi
else
  step "gofmt (all)"
  out=$(gofmt -l .)
  if [[ -n $out ]]; then
    printf 'gofmt needed:\n%s\n' "$out"
    fail=1
  fi
fi

# 2) Derive changed Go packages once; skip packages whose directory is gone.
mapfile -t go_pkgs < <(
  printf '%s\n' "$changed" | grep -E '\.go$' | xargs -r -n1 dirname | sort -u |
    while IFS= read -r d; do [[ -d $d ]] && printf './%s\n' "$d"; done
)
if printf '%s\n' "$changed" | grep -qxE 'go\.mod|go\.sum'; then
  go_pkgs=(./...)
fi

if ((${#go_pkgs[@]} > 0)); then
  run "go vet (changed)" go vet "${go_pkgs[@]}"
  run "go test (changed, cached)" go test -p 4 "${go_pkgs[@]}"
  run "go build (cached)" go build ./...
fi

# 3) Static / generated assets.
if printf '%s\n' "$changed" | grep -qE '^(web/static/|internal/httpserver/templates\.go|tools/spa_|tools/css_split/)'; then
  run "static-gzip check" go run ./tools/static_gzip -check
  run "spa-check" make spa-check
  run "asset-version" python3 tools/check_asset_version.py
fi

# 4) Docs metadata + stale mapping (only if docs/config/route changed).
if printf '%s\n' "$changed" | grep -qE '^(docs/|AGENTS\.md|README|Makefile|internal/httpserver/routes\.go|internal/config/|migrations/|deploy/)'; then
  run "docs-check" python3 tools/check_docs.py --strict
  if git rev-parse --verify --quiet origin/main >/dev/null 2>&1; then
    run "docs-check-stale" python3 tools/check_docs.py --strict --base=origin/main
  fi
fi

# 5) Perf guardrails only when their sources change.
if printf '%s\n' "$changed" | grep -qE '^(deploy/nginx/|tools/perf/)'; then
  run "perf-budget" make perf-budget
fi

if ((fail != 0)); then
  printf '\nFAST FAIL\n'
  exit 1
fi
printf '\nFAST PASS\n'
