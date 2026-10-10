#!/usr/bin/env bash
set -Eeuo pipefail

staged_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
temporary_root=$(mktemp -d /tmp/loom-acceptance-cleanup-test.XXXXXX)
trap 'rm -rf "$temporary_root"' EXIT

make_harness() {
  local name=$1
  local harness="$temporary_root/$name"
  mkdir -p "$harness/scripts" "$harness/bin"
  cp "$staged_root/scripts/acceptance-real.sh" "$harness/scripts/acceptance-real.sh"
  cat >"$harness/scripts/demo-up.sh" <<'EOF'
#!/usr/bin/env bash
exit 42
EOF
  cat >"$harness/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '%s\n' "$*" >>"$DOCKER_CALLS"
if [[ "$1" == image && "$2" == inspect ]]; then
  [[ "${TEST_API_IMAGE_PRESENT:-false}" == true ]]
  exit
fi
if [[ "$1" == compose && "$*" == *" run "* ]]; then
  if [[ "${TEST_API_IMAGE_PRESENT:-false}" == true ]]; then
    python3 -c 'import sys,tarfile; archive=tarfile.open(fileobj=sys.stdout.buffer,mode="w|"); archive.close()'
  fi
fi
EOF
  chmod +x "$harness/scripts/demo-up.sh" "$harness/bin/docker"
}

run_case() {
  local name=$1 id=$2 image_present=$3
  local harness="$temporary_root/$name"
  local artifacts="$temporary_root/$name-artifacts"
  local cache="$temporary_root/$name-cache"
  local compose_project="loom-acceptance-cleanup-$id"
  local calls="$temporary_root/$name-docker-calls.log"
  local status=0
  mkdir -p "$artifacts" "$cache"

  if env \
    PATH="$harness/bin:$PATH" \
    DOCKER_CALLS="$calls" \
    TEST_API_IMAGE_PRESENT="$image_present" \
    LOOM_ACCEPTANCE_ISOLATED=true \
    LOOM_ACCEPTANCE_FIXTURE_PREPARED=true \
    LOOM_ACCEPTANCE_ID="$id" \
    LOOM_ACCEPTANCE_RUN_ID="$id" \
    LOOM_ACCEPTANCE_ARTIFACTS="$artifacts" \
    LOOM_ACCEPTANCE_FIXTURE_CACHE="$cache" \
    LOOM_DEMO_COMPOSE_PROJECT="$compose_project" \
    LOOM_API_IMAGE="loom-acceptance-api:$id" \
    bash "$harness/scripts/acceptance-real.sh" >"$temporary_root/$name.log" 2>&1; then
    status=0
  else
    status=$?
  fi
  if (( status != 42 )); then
    cat "$temporary_root/$name.log" >&2
    echo "$name: acceptance failure status = $status, want original status 42" >&2
    return 1
  fi
  if [[ "$image_present" == true ]]; then
    rg -q '^compose --project-name .* run ' "$calls" || {
      echo "$name: expected artifact export when API image exists" >&2
      return 1
    }
  else
    ! rg -q '^compose --project-name .* run ' "$calls" || {
      echo "$name: attempted artifact export without the API image" >&2
      return 1
    }
    rg -q 'skipped artifact export: API image loom-acceptance-api:' "$artifacts/artifact-export.log" || {
      echo "$name: missing explicit skipped-export evidence" >&2
      return 1
    }
  fi
  jq -e '.status == "42" and .cleanup_status == "0"' "$artifacts/cleanup.json" >/dev/null
}

make_harness missing-image
make_harness existing-image
run_case missing-image 0123456789abcdef false
run_case existing-image fedcba9876543210 true
echo "acceptance-real cleanup guard passed"
