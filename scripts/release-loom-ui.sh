#!/usr/bin/env bash
set -Eeuo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(cd -- "$script_dir/.." && pwd -P)
ui_root="$repo_root/ui"
loom_ui_dir="$ui_root/packages/loom-ui"
demo_dir="$ui_root/apps/demo"
package_name='@calypr/loom-ui'
demo_package_name='@calypr/loom-demo'
registry='https://registry.npmjs.org/'
default_consumer_root='/Users/peterkor/Desktop/FFNEW/IDP-Frontend'
release_npm_cache=''
upstream_remote=''
upstream_branch=''
upstream_ref=''

# These are the only paths the automated release commit may ever include.
# Keep this as a literal array: the allowlist is part of the release contract.
owned_paths=(
  Makefile
  scripts/release-loom-ui.sh
  scripts/tests/test_release_loom_ui.py
  ui/packages/loom-ui/package.json
  ui/apps/demo/package.json
  ui/package-lock.json
)

cleanup() {
  if [[ -n "$release_npm_cache" && -d "$release_npm_cache" ]]; then
    rm -rf -- "$release_npm_cache"
  fi
}
trap cleanup EXIT

usage() {
  cat <<'EOF'
Usage:
  scripts/release-loom-ui.sh release VERSION
  scripts/release-loom-ui.sh prepare VERSION
  scripts/release-loom-ui.sh publish VERSION
  scripts/release-loom-ui.sh upgrade-consumer VERSION [IDP-FRONTEND-PATH]

Stages:
  release           Run preflight, authenticate, prepare when needed, commit
                    only release-owned files, push, and publish or verify.
  prepare          Synchronize versions, install, test, build, pack, inspect,
                   and run npm publish --dry-run. It never publishes.
  publish          Require a clean committed release, repeat all checks, then
                   publish the exact inspected tarball as public/latest and
                   verify the registry result.
  upgrade-consumer Install the published exact version in @gen3/frontend,
                   run its focused ExplorerBuilder test, and build frontend
                   and sampleCommons when those workspaces exist.

The npm registry is fixed to https://registry.npmjs.org/ and every stage uses
a private temporary npm cache. npm credentials are read by npm itself and are
never printed by this command. VERSION must be a stable X.Y.Z release.
EOF
}

die() {
  printf 'release-loom-ui: %s\n' "$*" >&2
  exit 1
}

command_exists() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

require_layout() {
  [[ -d "$ui_root" ]] || die "UI workspace not found: $ui_root"
  [[ -f "$ui_root/package.json" ]] || die "UI workspace package.json not found"
  [[ -f "$ui_root/package-lock.json" ]] || die "UI workspace package-lock.json not found"
  [[ -f "$loom_ui_dir/package.json" ]] || die "Loom UI package.json not found"
  [[ -f "$demo_dir/package.json" ]] || die "demo package.json not found"
}

validate_version() {
  local version=$1
  if ! node --input-type=module - "$version" <<'NODE'
const version = process.argv[2] ?? '';
if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version)) process.exit(1);
NODE
  then
    return 1
  fi
  [[ -n "$version" ]] || die 'VERSION is required'
}

version_compare() {
  local left=$1 right=$2
  node --input-type=module - "$left" "$right" <<'NODE'
const [left, right] = [process.argv[2], process.argv[3]].map((value) => value.split('.').map(BigInt));
for (let index = 0; index < 3; index += 1) {
  if (left[index] > right[index]) process.stdout.write('1');
  else if (left[index] < right[index]) process.stdout.write('-1');
  else continue;
  process.exit(0);
}
process.stdout.write('0');
NODE
}

json_value() {
  local path=$1 field=$2
  node --input-type=module - "$path" "$field" <<'NODE'
import { readFileSync } from 'node:fs';

const path = process.argv[2];
const field = process.argv[3];
const value = field.split('.').reduce((current, key) => current?.[key], JSON.parse(readFileSync(path, 'utf8')));
if (typeof value !== 'string') process.exit(1);
process.stdout.write(value);
NODE
}

check_node_and_npm() {
  local node_version npm_version node_major npm_major
  node_version=$(node --version)
  npm_version=$(npm --version)
  node_major=${node_version#v}; node_major=${node_major%%.*}
  npm_major=${npm_version%%.*}
  [[ "$node_major" =~ ^[0-9]+$ && "$node_major" -ge 20 ]] || die "Node >=20 is required (found $node_version)"
  [[ "$npm_major" =~ ^[0-9]+$ && "$npm_major" -ge 10 ]] || die "npm >=10 is required (found $npm_version)"
}

check_registry() {
  local configured
  configured=$(npm config get registry 2>/dev/null | tr -d '\r\n')
  [[ "$configured" == "$registry" ]] || die "npm registry must be $registry (configured: $configured)"
}

check_npm_auth() {
  if ! npm whoami --registry "$registry" >/dev/null 2>&1; then
    die 'npm authentication check failed; run npm login for https://registry.npmjs.org/ and retry'
  fi
}

authenticate_release() {
  if npm whoami --registry "$registry" >/dev/null 2>&1; then
    return 0
  fi

  printf 'npm authentication required; complete the browser login when prompted.\n'
  npm login --auth-type=web --registry "$registry"
  check_npm_auth
}

bootstrap() {
  local require_auth=${1:-yes}
  require_layout
  for command in node npm git tar mktemp chmod mkdir rm tr grep; do command_exists "$command"; done

  # Never inherit ~/.npm. npm_config_cache is intentionally exported only for
  # this process and its children. The directory is an exact mktemp target.
  if [[ -z "$release_npm_cache" || ! -d "$release_npm_cache" ]]; then
    release_npm_cache=$(mktemp -d "${TMPDIR:-/tmp}/loom-release-npm-cache.XXXXXX")
    chmod 700 "$release_npm_cache"
  fi
  export npm_config_cache="$release_npm_cache"

  check_node_and_npm
  check_registry
  if [[ "$require_auth" == yes ]]; then check_npm_auth; fi
}

require_clean_ui_scope() {
  local path
  while IFS= read -r -d '' path; do
    case "$path" in
      ui/packages/loom-ui/package.json|ui/apps/demo/package.json|ui/package-lock.json)
        ;;
      *)
        die "dirty UI path is outside the release metadata allowlist: $path"
        ;;
    esac
  done < <(
    git -C "$repo_root" diff --name-only -z -- ui
    git -C "$repo_root" diff --cached --name-only -z -- ui
    git -C "$repo_root" ls-files --others --exclude-standard -z -- ui
  )
}

require_release_git_state() {
  local branch merge counts local_only remote_only
  branch=$(git -C "$repo_root" symbolic-ref --quiet --short HEAD) || die 'release requires a non-detached branch'

  [[ ! -e "$(git -C "$repo_root" rev-parse --git-path MERGE_HEAD)" ]] || die 'release cannot run during a merge'
  [[ ! -d "$(git -C "$repo_root" rev-parse --git-path rebase-merge)" ]] || die 'release cannot run during a rebase'
  [[ ! -d "$(git -C "$repo_root" rev-parse --git-path rebase-apply)" ]] || die 'release cannot run during a rebase'

  require_clean_ui_scope

  upstream_remote=$(git -C "$repo_root" config --get "branch.$branch.remote") || die 'release requires a configured upstream'
  merge=$(git -C "$repo_root" config --get "branch.$branch.merge") || die 'release requires a configured upstream'
  [[ "$upstream_remote" != '.' && "$merge" == refs/heads/* ]] || die 'release requires a remote upstream branch'
  upstream_branch=${merge#refs/heads/}
  upstream_ref=$(git -C "$repo_root" rev-parse --symbolic-full-name "${upstream_remote}/${upstream_branch}" 2>/dev/null) || die 'release requires an existing upstream branch'

  git -C "$repo_root" fetch --quiet "$upstream_remote" "$upstream_branch" || die 'release could not refresh the configured upstream'
  upstream_ref=$(git -C "$repo_root" rev-parse --symbolic-full-name "${upstream_remote}/${upstream_branch}")
  counts=$(git -C "$repo_root" rev-list --left-right --count "HEAD...$upstream_ref")
  read -r local_only remote_only <<<"$counts"
  if [[ "$remote_only" != 0 && "$local_only" != 0 ]]; then
    die 'release cannot push a diverged branch; reconcile the configured upstream first'
  fi
  [[ "$remote_only" == 0 ]] || die 'release cannot push while the configured upstream is ahead'
}

commit_owned_changes() {
  local version=$1 status
  status=$(git -C "$repo_root" status --porcelain=v1 --untracked-files=all -- "${owned_paths[@]}")
  [[ -n "$status" ]] || return 0

  # Stage only the literal allowlist, then --only protects unrelated staged
  # entries from being included in this release commit.
  git -C "$repo_root" add -- "${owned_paths[@]}"
  git -C "$repo_root" commit --only "${owned_paths[@]}" -m "release(ui): @calypr/loom-ui $version"
}

push_release_head() {
  local head remote_head
  git -C "$repo_root" push "$upstream_remote" "HEAD:refs/heads/$upstream_branch"
  git -C "$repo_root" fetch --quiet "$upstream_remote" "$upstream_branch" || die 'release could not verify the pushed upstream'
  head=$(git -C "$repo_root" rev-parse HEAD)
  remote_head=$(git -C "$repo_root" rev-parse "$upstream_ref")
  [[ "$head" == "$remote_head" ]] || die 'release push completed but upstream does not equal HEAD'
}

check_metadata() {
  local expected=$1 actual_loom actual_demo lock_demo lock_loom
  actual_loom=$(json_value "$loom_ui_dir/package.json" version) || die "cannot read Loom UI package version"
  actual_demo=$(json_value "$demo_dir/package.json" dependencies.@calypr/loom-ui) || die "demo does not declare $package_name"
  lock_demo=$(json_value "$ui_root/package-lock.json" packages.apps/demo.dependencies.@calypr/loom-ui) || die "package-lock does not declare demo dependency $package_name"
  lock_loom=$(json_value "$ui_root/package-lock.json" packages.packages/loom-ui.version) || die 'package-lock does not contain the Loom UI package version'

  [[ "$(json_value "$loom_ui_dir/package.json" name)" == "$package_name" ]] || die "Loom UI package name is not $package_name"
  [[ "$(json_value "$demo_dir/package.json" name)" == "$demo_package_name" ]] || die "demo package name is not $demo_package_name"
  [[ "$actual_loom" == "$expected" ]] || die "Loom UI package version is $actual_loom, expected $expected"
  [[ "$actual_demo" == "$expected" ]] || die "demo dependency is $actual_demo, expected $expected"
  [[ "$lock_demo" == "$expected" ]] || die "package-lock demo dependency is $lock_demo, expected $expected"
  [[ "$lock_loom" == "$expected" ]] || die "package-lock Loom UI version is $lock_loom, expected $expected"
}

registry_version_exists() {
  local version=$1 output
  output=$(mktemp "${TMPDIR:-/tmp}/loom-release-registry.XXXXXX")
  if npm view "$package_name@$version" version --json --registry "$registry" >"$output" 2>&1; then
    rm -f -- "$output"
    return 0
  fi
  if ! grep -Eq 'E404|404 Not Found|code E404' "$output"; then
    rm -f -- "$output"
    die "could not prove $package_name@$version is absent from the npm registry"
  fi
  rm -f -- "$output"
  return 1
}

registry_latest_version() {
  local output error_file
  error_file=$(mktemp "${TMPDIR:-/tmp}/loom-release-latest.XXXXXX")
  if ! output=$(npm view "$package_name" dist-tags.latest --json --registry "$registry" 2>"$error_file"); then
    if grep -Eq 'E404|404 Not Found|code E404' "$error_file"; then
      rm -f -- "$error_file"
      printf ''
      return 0
    fi
    rm -f -- "$error_file"
    die 'could not read the npm latest dist-tag'
  fi
  rm -f -- "$error_file"
  node --input-type=module - "$output" <<'NODE'
const actual = JSON.parse(process.argv[2]);
if (typeof actual !== 'string') process.exit(1);
process.stdout.write(actual);
NODE
}

require_target_after_latest() {
  local target=$1 latest
  latest=$(registry_latest_version)
  if [[ -n "$latest" && "$(version_compare "$target" "$latest")" -le 0 ]]; then
    die "$target must be newer than the npm latest version $latest"
  fi
}

synchronize_versions() {
  local version=$1 current_loom current_demo
  current_loom=$(json_value "$loom_ui_dir/package.json" version) || die 'cannot read current Loom UI version'
  if [[ "$current_loom" != "$version" ]]; then
    (cd "$ui_root" && npm version "$version" --workspace "$package_name" --no-git-tag-version --ignore-scripts)
  fi

  current_demo=$(json_value "$demo_dir/package.json" dependencies.@calypr/loom-ui) || true
  if [[ "$current_demo" != "$version" ]]; then
    (cd "$ui_root" && npm pkg set "dependencies.@calypr/loom-ui=$version" --workspace "$demo_package_name")
  fi

  (cd "$ui_root" && npm install --package-lock-only --ignore-scripts --no-audit --no-fund)
  check_metadata "$version"
}

run_ui_checks() {
  (cd "$ui_root" && npm ci --ignore-scripts --no-audit --no-fund)
  (cd "$ui_root" && npm test)
  (cd "$ui_root" && npm run build)
}

package_slug=${package_name#@}
package_slug=${package_slug//\//-}

pack_and_inspect() {
  local version=$1 artifact_dir=$2 tarball contents metadata
  mkdir -p "$artifact_dir"
  tarball="$artifact_dir/$package_slug-$version.tgz"
  (cd "$ui_root" && npm pack "$loom_ui_dir" --pack-destination "$artifact_dir" --json --ignore-scripts >"$artifact_dir/pack.json")
  [[ -f "$tarball" ]] || die "npm pack did not create expected tarball: $tarball"

  contents="$artifact_dir/contents.txt"
  tar -tzf "$tarball" >"$contents"
  grep -Fxq 'package/package.json' "$contents" || die 'packed tarball has no package/package.json'
  grep -Fxq 'package/dist/loom-ui.js' "$contents" || die 'packed tarball has no dist/loom-ui.js'
  grep -Fxq 'package/dist/styles.css' "$contents" || die 'packed tarball has no dist/styles.css'

  metadata="$artifact_dir/package.json"
  tar -xOf "$tarball" package/package.json >"$metadata"
  [[ "$(json_value "$metadata" name)" == "$package_name" ]] || die 'packed package name is incorrect'
  [[ "$(json_value "$metadata" version)" == "$version" ]] || die 'packed package version is incorrect'
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$tarball" >"$artifact_dir/sha256.txt"
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$tarball" >"$artifact_dir/sha256.txt"
  fi
  printf '%s\n' "$tarball"
}

dry_run_publish() {
  local tarball=$1
  npm publish "$tarball" --dry-run --access public --tag latest --registry "$registry" --ignore-scripts
}

require_clean_release() {
  local status
  status=$(git -C "$repo_root" status --porcelain -- ui scripts/release-loom-ui.sh)
  [[ -z "$status" ]] || {
    printf '%s\n' "$status" >&2
    die 'publish requires committed UI release files and a committed release script'
  }
}

published_version() {
  local version=$1 output latest
  output=$(npm view "$package_name@$version" version --json --registry "$registry" 2>/dev/null) || die "$package_name@$version is not published"
  if ! node --input-type=module - "$output" "$version" <<'NODE'
const actual = JSON.parse(process.argv[2]);
if (actual !== process.argv[3]) process.exit(1);
NODE
  then
    die "registry returned an unexpected version for $package_name@$version"
  fi
  latest=$(npm view "$package_name" dist-tags.latest --json --registry "$registry" 2>/dev/null) || die 'could not read the npm latest dist-tag'
  if ! node --input-type=module - "$latest" "$version" <<'NODE'
const actual = JSON.parse(process.argv[2]);
if (actual !== process.argv[3]) process.exit(1);
NODE
  then
    die "$package_name latest dist-tag is not $version"
  fi
}

tarball_integrity() {
  local tarball=$1
  node --input-type=module - "$tarball" <<'NODE'
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';

process.stdout.write(`sha512-${createHash('sha512').update(readFileSync(process.argv[2])).digest('base64')}`);
NODE
}

registry_integrity() {
  local version=$1 output
  output=$(npm view "$package_name@$version" dist.integrity --json --registry "$registry" 2>/dev/null) || die "could not read registry integrity for $package_name@$version"
  node --input-type=module - "$output" <<'NODE'
const actual = JSON.parse(process.argv[2]);
if (typeof actual !== 'string') process.exit(1);
process.stdout.write(actual);
NODE
}

repair_or_verify_existing_release() {
  local version=$1 tarball=$2 local_integrity remote_integrity latest comparison
  local_integrity=$(tarball_integrity "$tarball")
  remote_integrity=$(registry_integrity "$version")
  [[ "$local_integrity" == "$remote_integrity" ]] || die "registry integrity for $package_name@$version does not match the local tarball"

  latest=$(registry_latest_version)
  if [[ -z "$latest" ]]; then
    comparison=1
  else
    comparison=$(version_compare "$version" "$latest")
  fi
  case "$comparison" in
    1)
      # The explicit publish stage authorizes repairing a missing/stale latest
      # tag, but never permits moving latest backward.
      npm dist-tag add "$package_name@$version" latest --registry "$registry"
      ;;
    0)
      ;;
    -1)
      die "refusing to move npm latest backward from $latest to $version"
      ;;
    *)
      die "could not compare $package_name@$version with npm latest $latest"
      ;;
  esac
  published_version "$version"
  printf 'Existing %s matches the local tarball and latest is verified\n' "$package_name@$version"
}

prepare() {
  local version=$1 artifact_dir tarball current
  bootstrap yes
  current=$(json_value "$loom_ui_dir/package.json" version) || die 'cannot read current Loom UI version'
  [[ "$(version_compare "$version" "$current")" -ge 0 ]] || die "cannot prepare $version below the checked-in Loom UI version $current"
  if registry_version_exists "$version"; then
    die "$package_name@$version already exists on the npm registry"
  fi
  require_target_after_latest "$version"
  synchronize_versions "$version"
  run_ui_checks
  artifact_dir="$repo_root/.artifacts/releases/loom-ui/$version"
  tarball=$(pack_and_inspect "$version" "$artifact_dir")
  dry_run_publish "$tarball"
  printf 'Prepared %s\n' "$package_name@$version"
  printf 'Commit the synchronized UI manifests, lockfile, and release script, then run:\n'
  printf '  %s publish %s\n' "$script_dir/release-loom-ui.sh" "$version"
}

publish() {
  local version=$1 artifact_dir tarball current
  bootstrap yes
  require_clean_release
  current=$(json_value "$loom_ui_dir/package.json" version) || die 'cannot read current Loom UI version'
  [[ "$(version_compare "$version" "$current")" -ge 0 ]] || die "cannot publish $version below the checked-in Loom UI version $current"
  check_metadata "$version"
  run_ui_checks
  artifact_dir="$repo_root/.artifacts/releases/loom-ui/$version"
  tarball=$(pack_and_inspect "$version" "$artifact_dir")
  if registry_version_exists "$version"; then
    repair_or_verify_existing_release "$version" "$tarball"
    return 0
  fi
  require_target_after_latest "$version"
  dry_run_publish "$tarball"
  printf 'Publishing %s (this is the explicit irreversible registry step)\n' "$package_name@$version"
  npm publish "$tarball" --access public --tag latest --registry "$registry" --ignore-scripts
  published_version "$version"
  printf 'Published and verified %s\n' "$package_name@$version"
}

release() {
  local version=$1

  # Keep this sequence explicit. A registry mutation is reachable only after
  # the branch is checked, authentication succeeds, owned changes are pushed,
  # and publish performs its normal verification.
  require_release_git_state
  bootstrap no
  authenticate_release

  if registry_version_exists "$version"; then
    printf '%s already exists; skipping prepare mutations\n' "$package_name@$version"
    check_metadata "$version"
  else
    prepare "$version"
  fi

  # Preparation is allowed to mutate only the three UI metadata files. Check
  # again before staging so a toolchain-generated source or workspace change
  # cannot slip past the release boundary.
  require_clean_ui_scope
  commit_owned_changes "$version"
  push_release_head
  publish "$version"
}

upgrade_consumer() {
  local version=$1 consumer_arg=${2:-$default_consumer_root} consumer_root frontend_name
  bootstrap no
  published_version "$version"
  [[ -d "$consumer_arg" ]] || die "consumer repository not found: $consumer_arg"
  consumer_root=$(cd -- "$consumer_arg" && pwd -P)
  [[ -f "$consumer_root/package.json" ]] || die "consumer package.json not found: $consumer_root"
  [[ -f "$consumer_root/packages/frontend/package.json" ]] || die "consumer frontend workspace not found: $consumer_root/packages/frontend"
  frontend_name=$(json_value "$consumer_root/packages/frontend/package.json" name) || die 'could not read consumer frontend workspace name'
  [[ "$frontend_name" == '@gen3/frontend' ]] || die "unexpected consumer frontend workspace: $frontend_name"

  (cd "$consumer_root" && npm install "$package_name@$version" --workspace '@gen3/frontend' --save-exact --ignore-scripts --no-audit --no-fund)
  [[ "$(json_value "$consumer_root/packages/frontend/package.json" dependencies.@calypr/loom-ui)" == "$version" ]] || die 'consumer dependency was not synchronized exactly'
  (cd "$consumer_root" && npm ls "$package_name" --workspace '@gen3/frontend' --depth=0)
  (cd "$consumer_root" && npm run test:all --workspace '@gen3/frontend' -- --runInBand src/features/ExplorerBuilder/ExplorerBuilderPage.unit.test.tsx)

  if [[ -f "$consumer_root/packages/frontend/package.json" ]]; then
    (cd "$consumer_root" && npm run build --workspace '@gen3/frontend')
  fi
  if [[ -f "$consumer_root/packages/sampleCommons/package.json" ]]; then
    (cd "$consumer_root" && npm run build --workspace '@gen3/samplecommons')
  fi
  printf 'Upgraded and verified @gen3/frontend against %s\n' "$package_name@$version"
}

main() {
  local command=${1:-}
  case "$command" in
    --help|-h|'') usage; [[ -n "$command" ]] || exit 2; return 0 ;;
    release|prepare|publish|upgrade-consumer) ;;
    *) usage >&2; die "unknown command: $command" ;;
  esac

  local version=${2:-}
  validate_version "$version" || die "invalid npm version: ${version:-<missing>}"
  case "$command" in
    release) [[ $# -eq 2 ]] || die 'release accepts exactly VERSION'; release "$version" ;;
    prepare) [[ $# -eq 2 ]] || die 'prepare accepts exactly VERSION'; prepare "$version" ;;
    publish) [[ $# -eq 2 ]] || die 'publish accepts exactly VERSION'; publish "$version" ;;
    upgrade-consumer) [[ $# -le 3 ]] || die 'upgrade-consumer accepts VERSION and optional consumer path'; upgrade_consumer "$version" "${3:-$default_consumer_root}" ;;
  esac
}

main "$@"
