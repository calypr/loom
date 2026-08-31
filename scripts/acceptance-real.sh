#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

run_id=${LOOM_ACCEPTANCE_RUN_ID:-$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')}
if [[ ! "$run_id" =~ ^[a-f0-9]{16}$ ]]; then echo "invalid run ID" >&2; exit 2; fi
arango_database="loom_acceptance_${run_id}"
clickhouse_database="loom_acceptance_${run_id}"
if [[ ! "$arango_database" =~ ^loom_acceptance_[a-f0-9]{16}$ || ! "$clickhouse_database" =~ ^loom_acceptance_[a-f0-9]{16}$ ]]; then echo "unsafe generated database name" >&2; exit 2; fi

artifacts=${LOOM_ACCEPTANCE_ARTIFACTS:-"$repo_root/.artifacts/acceptance/$run_id"}
cache=${LOOM_ACCEPTANCE_FIXTURE_CACHE:-"$repo_root/.cache/acceptance/fixture"}
server_source_root=${LOOM_ACCEPTANCE_SERVER_SOURCE_ROOT:-$repo_root}
[[ -f "$server_source_root/go.mod" ]] || { echo "server source root has no go.mod: $server_source_root" >&2; exit 2; }
mkdir -p "$artifacts" "$cache"
processes=()
server_pid=""
status=0
arango_url=""
clickhouse_http_url=""
clickhouse_native_url=""
clickhouse_username=""
clickhouse_password=""
forward_port=""
build_dir=""
server_config=""
clickhouse_curl_config=""

cleanup() {
  local previous=$? cleanup_status=0; status=$previous
  set +e
  if [[ -n "$server_pid" ]]; then kill "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true; fi
  if [[ "$arango_database" =~ ^loom_acceptance_[a-f0-9]{16}$ ]]; then
    [[ -n "$arango_url" ]] && curl -fsS -X DELETE "${arango_url}/_api/database/${arango_database}" -o "$artifacts/arango-drop.json" 2>"$artifacts/arango-drop.err" || cleanup_status=1
    if [[ -n "$arango_url" ]]; then
      curl -fsS "${arango_url}/_api/database" -o "$artifacts/arango-databases-after-drop.json" 2>/dev/null || cleanup_status=1
      jq -e --arg database "$arango_database" '.result | index($database) == null' "$artifacts/arango-databases-after-drop.json" >/dev/null 2>&1 || cleanup_status=1
    fi
  else cleanup_status=1; fi
  if [[ "$clickhouse_database" =~ ^loom_acceptance_[a-f0-9]{16}$ ]]; then
    if [[ -n "$clickhouse_http_url" ]]; then
      clickhouse_http_request "DROP DATABASE IF EXISTS \`$clickhouse_database\`" >"$artifacts/clickhouse-drop.txt" 2>"$artifacts/clickhouse-drop.err" || cleanup_status=1
      clickhouse_http_request "SELECT name FROM system.databases WHERE name = '$clickhouse_database'" >"$artifacts/clickhouse-check.txt" 2>/dev/null || cleanup_status=1
      [[ ! -s "$artifacts/clickhouse-check.txt" ]] || cleanup_status=1
    else
      cleanup_status=1
    fi
  else cleanup_status=1; fi
  # Keep the database forwards alive until both drops and absence probes finish.
  for pid in "${processes[@]+${processes[@]}}"; do kill "$pid" 2>/dev/null || true; done
  for pid in "${processes[@]+${processes[@]}}"; do wait "$pid" 2>/dev/null || true; done
  if [[ -n "$server_config" && -f "$server_config" ]]; then rm -f -- "$server_config"; fi
  if [[ -n "$clickhouse_curl_config" && -f "$clickhouse_curl_config" ]]; then rm -f -- "$clickhouse_curl_config"; fi
  if [[ -n "$build_dir" && -d "$build_dir" ]]; then rm -rf -- "$build_dir"; fi
  printf '{"status":"%s","cleanup_status":%d}\n' "$status" "$cleanup_status" >"$artifacts/cleanup.json"
  if (( status == 0 && cleanup_status != 0 )); then status=$cleanup_status; fi
  exit "$status"
}
trap cleanup EXIT

kubectl_bin=${KUBECTL:-kubectl}
namespace_args=()
if [[ -n "${KUBE_NAMESPACE:-}" ]]; then namespace_args=(-n "$KUBE_NAMESPACE"); fi

resolve_service() {
  local fallback=$1 selector=${2:-} value
  value=""
  if [[ -n "$selector" ]]; then value=$($kubectl_bin "${namespace_args[@]+${namespace_args[@]}}" get service -l "$selector" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true); fi
  if [[ -z "$value" ]]; then value=$($kubectl_bin "${namespace_args[@]+${namespace_args[@]}}" get service "$fallback" -o jsonpath='{.metadata.name}' 2>/dev/null || true); fi
  [[ -n "$value" ]] || { echo "could not resolve Kubernetes service $fallback" >&2; return 1; }
  printf '%s' "$value"
}

start_forward() {
  local name=$1 remote=$2 label=${3:-$1}
  local log="$artifacts/$label-port-forward.log" line
  "$kubectl_bin" "${namespace_args[@]+${namespace_args[@]}}" port-forward --address 127.0.0.1 "service/$name" "0:$remote" >"$log" 2>&1 &
  local pid=$!; processes+=("$pid")
  for _ in $(seq 1 100); do
    line=$(grep -m1 'Forwarding from 127.0.0.1:' "$log" 2>/dev/null || true)
    if [[ -n "$line" ]]; then forward_port=$(sed -E 's/.*127\.0\.0\.1:([0-9]+).*/\1/' <<<"$line"); return 0; fi
    kill -0 "$pid" 2>/dev/null || { echo "port-forward $name failed; see $log" >&2; return 1; }
    sleep 0.1
  done
  echo "timed out starting port-forward $name; see $log" >&2; return 1
}

clickhouse_http_request() {
  local query=$1
  curl -fsS --config "$clickhouse_curl_config" --data-binary "$query" "${clickhouse_http_url}/"
}

load_local_clickhouse_credentials() {
  if [[ -n "$clickhouse_username" && -n "$clickhouse_password" ]]; then return 0; fi
  command -v yq >/dev/null 2>&1 || {
    echo "local acceptance needs yq to discover missing ClickHouse credentials from Kubernetes secret loom-config (config.yaml); install yq or set LOOM_ACCEPTANCE_CLICKHOUSE_USERNAME and LOOM_ACCEPTANCE_CLICKHOUSE_PASSWORD" >&2
    return 1
  }

  local secret_name=${LOOM_ACCEPTANCE_KUBE_CONFIG_SECRET:-loom-config}
  local secret_data secret_config discovered_username discovered_password
  secret_data=$($kubectl_bin "${namespace_args[@]+${namespace_args[@]}}" get secret "$secret_name" -o jsonpath='{.data.config\.yaml}' 2>/dev/null) || {
    echo "could not read Kubernetes secret $secret_name key config.yaml for ClickHouse credentials" >&2
    return 1
  }
  [[ -n "$secret_data" ]] || {
    echo "Kubernetes secret $secret_name has no config.yaml key for ClickHouse credentials" >&2
    return 1
  }
  if base64 --decode </dev/null >/dev/null 2>&1; then
    secret_config=$(printf '%s' "$secret_data" | base64 --decode)
  elif base64 -d </dev/null >/dev/null 2>&1; then
    secret_config=$(printf '%s' "$secret_data" | base64 -d)
  elif base64 -D </dev/null >/dev/null 2>&1; then
    secret_config=$(printf '%s' "$secret_data" | base64 -D)
  else
    echo "could not find a base64 decoder for Kubernetes secret $secret_name key config.yaml" >&2
    return 1
  fi
  discovered_username=$(printf '%s\n' "$secret_config" | yq -r '.server.clickhouse.username // ""')
  discovered_password=$(printf '%s\n' "$secret_config" | yq -r '.server.clickhouse.password // ""')
  [[ -n "$clickhouse_username" ]] || clickhouse_username=$discovered_username
  [[ -n "$clickhouse_password" ]] || clickhouse_password=$discovered_password
  if [[ -z "$clickhouse_username" || -z "$clickhouse_password" ]]; then
    echo "Kubernetes secret $secret_name config.yaml did not provide both server.clickhouse.username and server.clickhouse.password; set explicit acceptance credential overrides" >&2
    return 1
  fi
}

if [[ "${LOOM_ACCEPTANCE_MODE:-local}" == "github" ]]; then
  arango_url=${LOOM_ACCEPTANCE_ARANGO_URL:-http://127.0.0.1:8529}
  clickhouse_http_url=${LOOM_ACCEPTANCE_CLICKHOUSE_HTTP_URL:-http://127.0.0.1:8123}
  clickhouse_native_url=${LOOM_ACCEPTANCE_CLICKHOUSE_NATIVE_URL:-clickhouse://127.0.0.1:9000}
  clickhouse_username=${LOOM_ACCEPTANCE_CLICKHOUSE_USERNAME:-loom_ci}
  clickhouse_password=${LOOM_ACCEPTANCE_CLICKHOUSE_PASSWORD:-loom_ci_password}
else
  arango_service=${LOOM_ACCEPTANCE_ARANGO_SERVICE:-$(resolve_service arangodb 'app=arangodb')}
  clickhouse_service=${LOOM_ACCEPTANCE_CLICKHOUSE_SERVICE:-$(resolve_service clickhouse 'app=clickhouse')}
  start_forward "$arango_service" 8529
  arango_port=$forward_port
  start_forward "$clickhouse_service" 8123 clickhouse-http
  clickhouse_http_port=$forward_port
  start_forward "$clickhouse_service" 9000 clickhouse-native
  clickhouse_native_port=$forward_port
  arango_url=${LOOM_ACCEPTANCE_ARANGO_URL:-"http://127.0.0.1:$arango_port"}
  clickhouse_http_url=${LOOM_ACCEPTANCE_CLICKHOUSE_HTTP_URL:-"http://127.0.0.1:$clickhouse_http_port"}
  clickhouse_native_url=${LOOM_ACCEPTANCE_CLICKHOUSE_NATIVE_URL:-"clickhouse://127.0.0.1:$clickhouse_native_port"}
  clickhouse_username=${LOOM_ACCEPTANCE_CLICKHOUSE_USERNAME:-}
  clickhouse_password=${LOOM_ACCEPTANCE_CLICKHOUSE_PASSWORD:-}
  load_local_clickhouse_credentials || exit 1
fi

clickhouse_curl_config=$(mktemp "${TMPDIR:-/tmp}/loom-acceptance-curl.XXXXXX.conf")
chmod 600 "$clickhouse_curl_config"
LOOM_ACCEPTANCE_CURL_USER="$clickhouse_username:$clickhouse_password" \
  jq -nr '"user = " + ($ENV.LOOM_ACCEPTANCE_CURL_USER | @json)' >"$clickhouse_curl_config"

listen_port=${LOOM_ACCEPTANCE_LOOM_PORT:-$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')}
loom_url="http://127.0.0.1:$listen_port"
build_dir=$(mktemp -d "${TMPDIR:-/tmp}/loom-acceptance-bin.XXXXXX")
go_build() {
  if [[ -n "${LOOM_ACCEPTANCE_GOCACHE:-}" ]]; then
    GOCACHE="$LOOM_ACCEPTANCE_GOCACHE" go build "$@"
  else
    go build "$@"
  fi
}
if [[ -n "${LOOM_ACCEPTANCE_GOCACHE:-}" ]]; then
  GOCACHE="$LOOM_ACCEPTANCE_GOCACHE" go -C "$server_source_root" build -o "$build_dir/arango-fhir-server" ./cmd/arango-fhir-server
else
  go -C "$server_source_root" build -o "$build_dir/arango-fhir-server" ./cmd/arango-fhir-server
fi
go_build -o "$build_dir/loom-acceptance" ./cmd/loom-acceptance

server_config=$(mktemp "${TMPDIR:-/tmp}/loom-acceptance-server-config.XXXXXX.json")
chmod 600 "$server_config"
LOOM_ACCEPTANCE_CONFIG_CLICKHOUSE_PASSWORD="$clickhouse_password" jq -n \
  --arg listen "127.0.0.1:$listen_port" \
  --arg arango_url "$arango_url" \
  --arg arango_database "$arango_database" \
  --arg clickhouse_url "$clickhouse_native_url" \
  --arg clickhouse_database "$clickhouse_database" \
  --arg clickhouse_username "$clickhouse_username" \
  --arg schema "$server_source_root/schemas/graph-fhir.json" \
  --arg recipe "$repo_root/testdata/acceptance/ncpi-tcga-brca/recipe.json" \
  '{server: {listen: $listen, url: $arango_url, database: $arango_database, schema: $schema, clickhouse: {enabled: true, url: $clickhouse_url, database: $clickhouse_database, username: $clickhouse_username, password: $ENV.LOOM_ACCEPTANCE_CONFIG_CLICKHOUSE_PASSWORD}, dataframer: {recipe: $recipe}}, auth: {allow_unauthenticated: true}}' \
  >"$server_config"
"$build_dir/arango-fhir-server" --config "$server_config" --no-auth >"$artifacts/loom.log" 2>&1 &
server_pid=$!
for _ in $(seq 1 180); do curl -fsS "$loom_url/readyz" >"$artifacts/readyz.json" 2>/dev/null && break; kill -0 "$server_pid" 2>/dev/null || { echo "Loom stopped; see $artifacts/loom.log" >&2; exit 1; }; sleep 1; done
curl -fsS "$loom_url/readyz" >"$artifacts/readyz-final.json"
rm -f -- "$server_config"
server_config=""

LOOM_ACCEPTANCE_CLICKHOUSE_PASSWORD="$clickhouse_password" "$build_dir/loom-acceptance" --run-id "$run_id" --loom-url "$loom_url" --arango-url "$arango_url" --clickhouse-url "$clickhouse_native_url" --clickhouse-username "$clickhouse_username" --fixture-lock testdata/acceptance/ncpi-tcga-brca/fixture.lock.json --fixture-cache "$cache" --workspace testdata/acceptance/ncpi-tcga-brca/workspace.json --oracle testdata/acceptance/ncpi-tcga-brca/oracle.json --artifacts "$artifacts"
