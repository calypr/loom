#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

canonical_project=${LOOM_DEMO_COMPOSE_PROJECT:-loom-demo}
acceptance_id=${LOOM_ACCEPTANCE_ID:-$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')}
run_id=${LOOM_ACCEPTANCE_RUN_ID:-$acceptance_id}
artifacts=${LOOM_ACCEPTANCE_ARTIFACTS:-"$repo_root/.artifacts/acceptance/$acceptance_id"}
cache=${LOOM_ACCEPTANCE_FIXTURE_CACHE:-"$repo_root/.cache/acceptance/fixture"}
source_root=${LOOM_DEMO_SOURCE_ROOT:-$repo_root}
isolated=${LOOM_ACCEPTANCE_ISOLATED:-false}
fixture_prepared=${LOOM_ACCEPTANCE_FIXTURE_PREPARED:-false}

[[ "$acceptance_id" =~ ^[a-f0-9]{16}$ ]] || { echo "invalid acceptance ID" >&2; exit 2; }
[[ "$run_id" =~ ^[a-f0-9]{16}$ ]] || { echo "invalid acceptance run ID" >&2; exit 2; }
[[ "$canonical_project" =~ ^[a-z0-9][a-z0-9_-]*$ ]] || { echo "invalid LOOM_DEMO_COMPOSE_PROJECT: $canonical_project" >&2; exit 2; }
case "$isolated" in true|false) ;; *) echo "invalid LOOM_ACCEPTANCE_ISOLATED: $isolated" >&2; exit 2 ;; esac
case "$fixture_prepared" in true|false) ;; *) echo "invalid LOOM_ACCEPTANCE_FIXTURE_PREPARED: $fixture_prepared" >&2; exit 2 ;; esac
if [[ "$source_root" != /* ]]; then source_root="$repo_root/$source_root"; fi
mkdir -p "$artifacts" "$cache"
cache=$(cd "$cache" && pwd)

free_ports() {
  python3 -c 'import socket; sockets=[]; ports=[]
for _ in range(2):
 s=socket.socket(); s.bind(("127.0.0.1", 0)); sockets.append(s); ports.append(str(s.getsockname()[1]))
print(" ".join(ports)); [s.close() for s in sockets]'
}

deployment_project=$canonical_project
acceptance_project=$canonical_project
teardown_acceptance=false
if [[ "$isolated" == false ]]; then
  # Rebuild the user's canonical demo from the current source without changing
  # its repository data. The locked fixture runs separately below.
  LOOM_DEMO_SEED=false \
    LOOM_DEMO_SOURCE_ROOT="$source_root" \
    "$repo_root/scripts/demo-up.sh"
  docker compose --project-name "$deployment_project" ps -a >"$artifacts/deployment-compose-ps.txt" 2>&1 || true

  acceptance_project="loom-acceptance-$acceptance_id"
  read -r api_port ui_port <<<"$(free_ports)"
  export LOOM_DEMO_API_PORT=$api_port
  export LOOM_DEMO_UI_PORT=$ui_port
  export LOOM_API_IMAGE="loom-acceptance-api:$acceptance_id"
  export LOOM_UI_IMAGE="loom-acceptance-ui:$acceptance_id"
  teardown_acceptance=true
fi

compose=(docker compose --project-name "$acceptance_project")
report_exported=false

capture_compose_evidence() {
  "${compose[@]}" ps -a >"$artifacts/compose-ps.txt" 2>&1 || true
  "${compose[@]}" images >"$artifacts/compose-images.txt" 2>&1 || true
  "${compose[@]}" config --services >"$artifacts/compose-services.txt" 2>&1 || true
}

export_demo_artifacts() {
  mkdir -p "$artifacts"
  "${compose[@]}" run --rm --no-deps -T --entrypoint /bin/sh demo-seed \
    -c 'tar -C /var/lib/loom/artifacts -cf - .' \
    | tar -C "$artifacts" -xf -
  report_exported=true
}

cleanup() {
  local command_status=$? teardown_status=0
  set +e
  if [[ "$report_exported" != true ]]; then
    export_demo_artifacts >/dev/null 2>&1 || true
  fi
  capture_compose_evidence
  if [[ "$teardown_acceptance" == true ]]; then
    LOOM_DEMO_COMPOSE_PROJECT="$acceptance_project" \
      "$repo_root/scripts/demo-down.sh" --volumes --remove-orphans \
      >"$artifacts/compose-down.log" 2>&1 || teardown_status=$?
  fi
  jq -n \
    --arg status "$command_status" \
    --arg cleanup_status "$teardown_status" \
    --arg deployment_project "$deployment_project" \
    --arg acceptance_project "$acceptance_project" \
    --argjson acceptance_volumes_removed "$teardown_acceptance" \
    '{status:$status, cleanup_status:$cleanup_status, deployment_project:$deployment_project,
      acceptance_project:$acceptance_project, acceptance_volumes_removed:$acceptance_volumes_removed}' \
    >"$artifacts/cleanup.json" 2>/dev/null || true
  if (( command_status == 0 && teardown_status != 0 )); then command_status=$teardown_status; fi
  exit "$command_status"
}
trap cleanup EXIT

if [[ "$fixture_prepared" == false ]]; then
  if [[ -n "${LOOM_ACCEPTANCE_GOCACHE:-}" ]]; then
    GOCACHE="$LOOM_ACCEPTANCE_GOCACHE" go run ./cmd/loom-acceptance --fixture-only --fixture-cache "$cache"
  else
    go run ./cmd/loom-acceptance --fixture-only --fixture-cache "$cache"
  fi
fi

export LOOM_DEMO_COMPOSE_PROJECT="$acceptance_project"
export LOOM_DEMO_RUN_ID="$run_id"
export LOOM_DEMO_PROJECT="${LOOM_DEMO_PROJECT:-${LOOM_ACCEPTANCE_PROJECT:-NCPI_ACCEPTANCE}}"
export LOOM_DEMO_SOURCE_ROOT="$source_root"
export LOOM_DEMO_FIXTURE_CACHE_DIR="$cache"

"$repo_root/scripts/demo-up.sh"
export_demo_artifacts

jq -e '.status == "PASSED"' "$artifacts/report.json" >/dev/null || {
  echo "demo-seed acceptance report is not PASSED: $artifacts/report.json" >&2
  exit 1
}

"$repo_root/scripts/demo-smoke.sh"

browser_smoke=${LOOM_ACCEPTANCE_BROWSER_SMOKE:-true}
case "$browser_smoke" in
  true|1|yes|on) "$repo_root/scripts/demo-browser-smoke.sh" ;;
  false|0|no|off) ;;
  *) echo "invalid LOOM_ACCEPTANCE_BROWSER_SMOKE: $browser_smoke" >&2; exit 2 ;;
esac

echo "Docker Compose acceptance PASSED (deployment=$deployment_project acceptance=$acceptance_project run=$run_id)"
