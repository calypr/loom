#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

compose_project=${LOOM_DEMO_COMPOSE_PROJECT:-loom-demo}
[[ $compose_project =~ ^[a-z0-9][a-z0-9_-]*$ ]] || { echo "invalid LOOM_DEMO_COMPOSE_PROJECT: $compose_project" >&2; exit 2; }

docker compose --project-name "$compose_project" down "$@"
