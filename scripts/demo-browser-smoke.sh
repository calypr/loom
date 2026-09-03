#!/usr/bin/env bash
set -Eeuo pipefail

ui_host=${LOOM_DEMO_UI_HOST:-127.0.0.1}
[[ $ui_host == *:* ]] && ui_host="[$ui_host]"
ui_url=${LOOM_DEMO_UI_URL:-http://$ui_host:${LOOM_DEMO_UI_PORT:-3080}}
project=${LOOM_DEMO_PROJECT:-NCPI_ACCEPTANCE}
output_title=${LOOM_DEMO_OUTPUT_TITLE:-TCGA-BRCA patient cohort}
expected_column=${LOOM_DEMO_EXPECTED_COLUMN_LABEL:-Patient ID}
expected_cell=${LOOM_DEMO_EXPECTED_CELL:-TCGA-}
expected_resources=${LOOM_DEMO_EXPECTED_RESOURCES:-Patient Condition Specimen Observation ResearchStudy}
browser_url=${LOOM_DEMO_BROWSER_URL:-$ui_url/?project=$project&explorer=default}

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
    "$browser_url&mode=$mode" >"$output" 2>"$work_dir/$mode.stderr" &
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

assert_served_graph_styles() {
  local index css_href css_url css
  index=$(curl --fail --silent --show-error --location "$ui_url/")
  css_href=$(printf '%s' "$index" | grep -oE 'href="[^"]+\.css"' | head -n 1 | sed -E 's/^href="//; s/"$//')
  if [[ -z $css_href ]]; then
    echo "Standalone UI did not serve a stylesheet" >&2
    return 1
  fi
  if [[ $css_href == /* ]]; then
    css_url="${ui_url%/}$css_href"
  else
    css_url="${ui_url%/}/$css_href"
  fi
  css=$(curl --fail --silent --show-error --location "$css_url")
  for selector in '.react-flow{' '.react-flow__renderer{' '.react-flow__viewport{' '.react-flow__node{' ; do
    if ! grep -Fq "$selector" <<<"$css"; then
      echo "Expected React Flow layout selector $selector in served stylesheet $css_href" >&2
      return 1
    fi
  done
}

viewer_dom="$work_dir/viewer.html"
builder_dom="$work_dir/builder.html"
assert_served_graph_styles
dump_page viewer "$viewer_dom" "$expected_cell"
assert_no_ui_error "$viewer_dom"
assert_contains "$viewer_dom" "$output_title"
assert_contains "$viewer_dom" "$expected_column"
assert_contains "$viewer_dom" "$expected_cell"

dump_page builder "$builder_dom" "Publish"
assert_no_ui_error "$builder_dom"
assert_contains "$builder_dom" "FHIR Explorer Studio"
assert_contains "$builder_dom" "Preview"
assert_contains "$builder_dom" "Publish"
for resource in $expected_resources; do
  assert_contains "$builder_dom" "$resource"
done

echo "Standalone Viewer and Builder rendered the seeded Explorer in headless Chrome"
