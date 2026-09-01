#!/usr/bin/env bash
set -Eeuo pipefail

ui_url=${LOOM_DEMO_UI_URL:-http://127.0.0.1:3080}

find_chrome() {
  if [[ -n ${CHROME_BIN:-} ]]; then
    printf '%s\n' "$CHROME_BIN"
    return
  fi

  local candidate
  for candidate in \
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
    google-chrome \
    chromium \
    chromium-browser; do
    if [[ -x $candidate ]] || command -v "$candidate" >/dev/null 2>&1; then
      printf '%s\n' "$candidate"
      return
    fi
  done

  echo "Chrome or Chromium is required. Set CHROME_BIN to its executable." >&2
  return 1
}

chrome=$(find_chrome)
if [[ -n ${LOOM_BROWSER_ARTIFACT_DIR:-} ]]; then
  work_dir=$LOOM_BROWSER_ARTIFACT_DIR
  mkdir -p "$work_dir"
else
  work_dir=$(mktemp -d "${TMPDIR:-/tmp}/loom-browser-smoke.XXXXXX")
  trap 'rm -rf -- "$work_dir"' EXIT
fi

dump_page() {
  local mode=$1
  local output=$2
  local ready_text=$3
  local profile="$work_dir/profile-$mode"
  local browser_pid

  "$chrome" \
    --headless=new \
    --no-sandbox \
    --disable-gpu \
    --disable-background-networking \
    --disable-component-update \
    --no-first-run \
    --no-default-browser-check \
    --user-data-dir="$profile" \
    --virtual-time-budget=5000 \
    --dump-dom \
    "$ui_url/?mode=$mode" >"$output" 2>"$work_dir/$mode.stderr" &
  browser_pid=$!

  for _ in $(seq 1 60); do
    if grep -Fq "$ready_text" "$output" || \
      grep -Fq 'class="loom-error"' "$output" || \
      grep -Fq 'role="alert"' "$output"; then
      break
    fi
    if ! kill -0 "$browser_pid" 2>/dev/null; then
      wait "$browser_pid"
      return
    fi
    sleep 0.5
  done

  if kill -0 "$browser_pid" 2>/dev/null; then
    kill "$browser_pid"
    wait "$browser_pid" 2>/dev/null || true
  fi
}

assert_contains() {
  local file=$1
  local text=$2
  if ! grep -Fq "$text" "$file"; then
    echo "Expected $text in $(basename "$file")" >&2
    return 1
  fi
}

assert_no_ui_error() {
  local file=$1
  if grep -Fq 'class="loom-error"' "$file" || grep -Fq 'role="alert"' "$file"; then
    echo "Loom rendered an error in $(basename "$file")" >&2
    return 1
  fi
}

viewer_dom="$work_dir/viewer.html"
builder_dom="$work_dir/builder.html"
dump_page viewer "$viewer_dom" "TCGA-"
assert_no_ui_error "$viewer_dom"
assert_contains "$viewer_dom" "TCGA-BRCA patient cohort"
assert_contains "$viewer_dom" "Patient ID"
assert_contains "$viewer_dom" "TCGA-"

dump_page builder "$builder_dom" "Publish"
assert_no_ui_error "$builder_dom"
assert_contains "$builder_dom" "FHIR Explorer Studio"
assert_contains "$builder_dom" "Preview"
assert_contains "$builder_dom" "Publish"

echo "Standalone Viewer and Builder rendered the seeded Explorer in headless Chrome"
