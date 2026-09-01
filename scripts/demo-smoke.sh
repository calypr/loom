#!/usr/bin/env bash
set -Eeuo pipefail

api_url=${LOOM_DEMO_API_URL:-http://127.0.0.1:8080}
ui_url=${LOOM_DEMO_UI_URL:-http://127.0.0.1:3080}

curl -fsS "$api_url/readyz" >/dev/null
curl -fsS "$api_url/api/v1/projects/NCPI_ACCEPTANCE/explorers/default" | grep -q 'tcga_brca_cohort'
curl -fsS "$ui_url/" | grep -qi '<!doctype html>'

echo "Loom API, seeded Explorer, and standalone UI are reachable"
