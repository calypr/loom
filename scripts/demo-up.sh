#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }
docker compose version >/dev/null 2>&1 || { echo "docker compose v2 is required" >&2; exit 1; }

compose_project=${LOOM_DEMO_COMPOSE_PROJECT:-loom-demo}
api_host=${LOOM_DEMO_API_HOST:-127.0.0.1}
api_port=${LOOM_DEMO_API_PORT:-8080}
ui_host=${LOOM_DEMO_UI_HOST:-127.0.0.1}
ui_port=${LOOM_DEMO_UI_PORT:-3080}
run_id=${LOOM_DEMO_RUN_ID:-d000000000000001}
source_root=${LOOM_DEMO_SOURCE_ROOT:-$repo_root}
if [[ "$source_root" != /* ]]; then source_root="$repo_root/$source_root"; fi
api_build_context=${LOOM_API_BUILD_CONTEXT:-$source_root}
ui_build_context=${LOOM_UI_BUILD_CONTEXT:-$source_root/ui}
api_image=${LOOM_API_IMAGE:-loom-demo-api:local}
ui_image=${LOOM_UI_IMAGE:-loom-demo-ui:local}
seed=${LOOM_DEMO_SEED:-true}

[[ $compose_project =~ ^[a-z0-9][a-z0-9_-]*$ ]] || { echo "invalid LOOM_DEMO_COMPOSE_PROJECT: $compose_project" >&2; exit 2; }
[[ $api_port =~ ^[0-9]+$ ]] && ((api_port >= 1 && api_port <= 65535)) || { echo "invalid LOOM_DEMO_API_PORT: $api_port" >&2; exit 2; }
[[ $ui_port =~ ^[0-9]+$ ]] && ((ui_port >= 1 && ui_port <= 65535)) || { echo "invalid LOOM_DEMO_UI_PORT: $ui_port" >&2; exit 2; }
[[ $run_id =~ ^[a-f0-9]{16}$ ]] || { echo "invalid LOOM_DEMO_RUN_ID: $run_id" >&2; exit 2; }
[[ -d "$api_build_context" ]] || { echo "API build context does not exist: $api_build_context" >&2; exit 2; }
[[ -f "$api_build_context/Dockerfile" ]] || { echo "API build context has no Dockerfile: $api_build_context" >&2; exit 2; }
[[ -d "$ui_build_context" ]] || { echo "UI build context does not exist: $ui_build_context" >&2; exit 2; }
[[ -f "$ui_build_context/apps/demo/Dockerfile" ]] || { echo "UI build context has no apps/demo/Dockerfile: $ui_build_context" >&2; exit 2; }
case "$seed" in true|false) ;; *) echo "invalid LOOM_DEMO_SEED: $seed" >&2; exit 2 ;; esac

export LOOM_COMPOSE_PROJECT_NAME=$compose_project
export LOOM_API_HOST=$api_host
export LOOM_API_PORT=$api_port
export LOOM_UI_HOST=$ui_host
export LOOM_UI_PORT=$ui_port
export LOOM_DEMO_RUN_ID=$run_id
export LOOM_API_BUILD_CONTEXT=$api_build_context
export LOOM_UI_BUILD_CONTEXT=$ui_build_context
export LOOM_API_IMAGE=$api_image
export LOOM_UI_IMAGE=$ui_image

api_url_host=$api_host
ui_url_host=$ui_host
[[ $api_url_host == *:* ]] && api_url_host="[$api_url_host]"
[[ $ui_url_host == *:* ]] && ui_url_host="[$ui_url_host]"
api_url=${LOOM_DEMO_API_URL:-http://$api_url_host:$api_port}
ui_url=${LOOM_DEMO_UI_URL:-http://$ui_url_host:$ui_port}
compose=(docker compose --project-name "$compose_project")

"${compose[@]}" up --build -d arangodb clickhouse loom-api

for _ in $(seq 1 180); do
  if curl -fsS "$api_url/readyz" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl -fsS "$api_url/readyz" >/dev/null

if [[ "$seed" == true ]]; then
  "${compose[@]}" run --rm --no-deps demo-seed
fi
"${compose[@]}" up --build -d loom-ui

for _ in $(seq 1 120); do
  if curl -fsS "$ui_url/" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl -fsS "$ui_url/" >/dev/null

echo "Loom demo is ready at $ui_url"
