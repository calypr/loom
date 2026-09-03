#!/usr/bin/env bash
set -Eeuo pipefail

api_host=${LOOM_DEMO_API_HOST:-127.0.0.1}
ui_host=${LOOM_DEMO_UI_HOST:-127.0.0.1}
[[ $api_host == *:* ]] && api_host="[$api_host]"
[[ $ui_host == *:* ]] && ui_host="[$ui_host]"
api_url=${LOOM_DEMO_API_URL:-http://$api_host:${LOOM_DEMO_API_PORT:-8080}}
ui_url=${LOOM_DEMO_UI_URL:-http://$ui_host:${LOOM_DEMO_UI_PORT:-3080}}
compose_project=${LOOM_DEMO_COMPOSE_PROJECT:-loom-demo}
project=${LOOM_DEMO_PROJECT:-NCPI_ACCEPTANCE}
generation=${LOOM_DEMO_GENERATION:-tcga-brca-locked}
management=${LOOM_DEMO_MANAGEMENT:-REPOSITORY}
output_id=${LOOM_DEMO_OUTPUT_ID:-tcga_brca_cohort}
output_title=${LOOM_DEMO_OUTPUT_TITLE:-TCGA-BRCA patient cohort}

[[ $compose_project =~ ^[a-z0-9][a-z0-9_-]*$ ]] || { echo "invalid LOOM_DEMO_COMPOSE_PROJECT: $compose_project" >&2; exit 2; }

curl -fsS "$api_url/readyz" >/dev/null
docker compose --project-name "$compose_project" exec -T loom-api \
  /app/loom-acceptance \
  --smoke-only \
  --project "$project" \
  --smoke-management "$management" \
  --generation "$generation" \
  --smoke-output-id "$output_id" \
  --smoke-output-title "$output_title" \
  --oracle /app/testdata/oracle.json
curl -fsS "$ui_url/" | grep -qi '<!doctype html>'

echo "Loom API, expected seeded Explorer, and standalone UI are healthy"
