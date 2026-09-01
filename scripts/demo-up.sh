#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }
docker compose version >/dev/null 2>&1 || { echo "docker compose v2 is required" >&2; exit 1; }

docker compose up --build -d arangodb clickhouse loom-api

for _ in $(seq 1 180); do
  if curl -fsS http://127.0.0.1:8080/readyz >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl -fsS http://127.0.0.1:8080/readyz >/dev/null

docker compose run --rm --no-deps demo-seed
docker compose up --build -d loom-ui

for _ in $(seq 1 120); do
  if curl -fsS http://127.0.0.1:3080/ >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl -fsS http://127.0.0.1:3080/ >/dev/null

echo "Loom demo is ready at http://127.0.0.1:3080"
