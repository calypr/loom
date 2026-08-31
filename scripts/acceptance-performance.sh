#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

base_ref=${LOOM_ACCEPTANCE_BASE_REF:-}
if [[ -z "$base_ref" && -n "${GITHUB_BASE_SHA:-}" ]]; then base_ref=$GITHUB_BASE_SHA; fi
if [[ -z "$base_ref" && -n "${GITHUB_EVENT_BEFORE:-}" && ! "$GITHUB_EVENT_BEFORE" =~ ^0+$ ]]; then base_ref=$GITHUB_EVENT_BEFORE; fi
if [[ -z "$base_ref" ]]; then base_ref=HEAD; fi

comparison_id=${LOOM_ACCEPTANCE_COMPARISON_ID:-$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')}
[[ "$comparison_id" =~ ^[a-f0-9]{16}$ ]] || { echo "invalid comparison ID" >&2; exit 2; }
artifacts=${LOOM_ACCEPTANCE_ARTIFACTS:-"$repo_root/.artifacts/acceptance/performance-$comparison_id"}
cache=${LOOM_ACCEPTANCE_FIXTURE_CACHE:-"$repo_root/.cache/acceptance/fixture"}
mkdir -p "$artifacts" "$cache"

temporary_root=$(mktemp -d "${TMPDIR:-/tmp}/loom-acceptance-base.XXXXXX")
cleanup() { rm -rf "$temporary_root"; }
trap cleanup EXIT
base_source="$temporary_root/base"
mkdir -p "$base_source"

base_unavailable() {
  local reason=$1
  jq -n --arg status BASE_UNAVAILABLE --arg base_ref "$base_ref" --arg reason "$reason" \
    '{status:$status,base_ref:$base_ref,reason:$reason,observations:[]}' >"$artifacts/performance.json"
  if [[ "${LOOM_ACCEPTANCE_ALLOW_BASE_UNAVAILABLE:-false}" == "true" ]]; then
    echo "acceptance performance BASE_UNAVAILABLE: $reason" >&2
    return 0
  fi
  echo "acceptance performance base unavailable: $reason" >&2
  return 1
}

if ! git cat-file -e "${base_ref}^{commit}" 2>/dev/null; then
  base_unavailable "base commit is not available in the checkout"
  exit $?
fi
if ! git archive "$base_ref" | tar -x -C "$base_source"; then
  base_unavailable "base source archive could not be created"
  exit $?
fi

if [[ -n "${LOOM_ACCEPTANCE_GOCACHE:-}" ]]; then
  GOCACHE="$LOOM_ACCEPTANCE_GOCACHE" go run ./cmd/loom-acceptance --fixture-only --fixture-cache "$cache"
else
  go run ./cmd/loom-acceptance --fixture-only --fixture-cache "$cache"
fi

run_variant() {
  local name=$1 source_root=$2
  LOOM_ACCEPTANCE_ARTIFACTS="$artifacts/$name" \
  LOOM_ACCEPTANCE_FIXTURE_CACHE="$cache" \
  LOOM_ACCEPTANCE_SERVER_SOURCE_ROOT="$source_root" \
  "$repo_root/scripts/acceptance-real.sh"
}

if [[ ! -f "$base_source/cmd/loom-acceptance/main.go" ]]; then
  run_variant current "$repo_root"
  LOOM_ACCEPTANCE_ALLOW_BASE_UNAVAILABLE=true base_unavailable "base commit predates the acceptance protocol"
  exit 0
fi

head_key=$(git rev-parse HEAD 2>/dev/null || printf '0')
last_hex=${head_key: -1}
if (( 16#$last_hex % 2 == 0 )); then
  first_order=base-current
  if ! run_variant base "$base_source"; then base_unavailable "base server did not complete the current acceptance protocol"; exit $?; fi
  run_variant current "$repo_root"
else
  first_order=current-base
  run_variant current "$repo_root"
  if ! run_variant base "$base_source"; then base_unavailable "base server did not complete the current acceptance protocol"; exit $?; fi
fi

compare=(go run ./cmd/loom-acceptance
  --performance-base-report "$artifacts/base/report.json"
  --performance-current-report "$artifacts/current/report.json"
  --performance-output "$artifacts/performance.json")
if [[ -n "${LOOM_ACCEPTANCE_GOCACHE:-}" ]]; then compare=(env GOCACHE="$LOOM_ACCEPTANCE_GOCACHE" "${compare[@]}"); fi
set +e
"${compare[@]}"
compare_status=$?
set -e
if (( compare_status != 3 )); then exit "$compare_status"; fi

if [[ "$first_order" == base-current ]]; then
  run_variant current-repeat "$repo_root"
  if ! run_variant base-repeat "$base_source"; then base_unavailable "repeat base run failed"; exit $?; fi
else
  if ! run_variant base-repeat "$base_source"; then base_unavailable "repeat base run failed"; exit $?; fi
  run_variant current-repeat "$repo_root"
fi

final_compare=(go run ./cmd/loom-acceptance
  --performance-base-report "$artifacts/base/report.json"
  --performance-current-report "$artifacts/current/report.json"
  --performance-repeat-base-report "$artifacts/base-repeat/report.json"
  --performance-repeat-current-report "$artifacts/current-repeat/report.json"
  --performance-output "$artifacts/performance.json")
if [[ -n "${LOOM_ACCEPTANCE_GOCACHE:-}" ]]; then final_compare=(env GOCACHE="$LOOM_ACCEPTANCE_GOCACHE" "${final_compare[@]}"); fi
"${final_compare[@]}"
