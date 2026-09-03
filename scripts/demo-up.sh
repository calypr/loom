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

[[ $compose_project =~ ^[a-z0-9][a-z0-9_-]*$ ]] || { echo "invalid LOOM_DEMO_COMPOSE_PROJECT: $compose_project" >&2; exit 2; }
[[ $api_port =~ ^[0-9]+$ ]] && ((api_port >= 1 && api_port <= 65535)) || { echo "invalid LOOM_DEMO_API_PORT: $api_port" >&2; exit 2; }
[[ $ui_port =~ ^[0-9]+$ ]] && ((ui_port >= 1 && ui_port <= 65535)) || { echo "invalid LOOM_DEMO_UI_PORT: $ui_port" >&2; exit 2; }

export LOOM_COMPOSE_PROJECT_NAME=$compose_project
export LOOM_API_HOST=$api_host
export LOOM_API_PORT=$api_port
export LOOM_UI_HOST=$ui_host
export LOOM_UI_PORT=$ui_port

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

"${compose[@]}" run --rm --no-deps demo-seed
"${compose[@]}" up --build -d loom-ui

for _ in $(seq 1 120); do
  if curl -fsS "$ui_url/" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl -fsS "$ui_url/" >/dev/null

echo "Loom demo is ready at $ui_url"
