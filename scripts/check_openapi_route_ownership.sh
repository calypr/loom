#!/usr/bin/env bash
set -euo pipefail

search_production_go() {
  local pattern="$1"
  if command -v rg >/dev/null 2>&1; then
    rg -n "${pattern}" internal --glob '*.go' --glob '!**/*_test.go'
  else
    grep -R -nE --include='*.go' --exclude='*_test.go' "${pattern}" internal
  fi
}

if matches="$(search_production_go '(^|[^[:alnum:]_])(app|router|[[:alnum:]_]+\.app|server\.App\(\))\.(Get|Post|Put|Delete|Patch|Head|Options|All)\(' | grep -v '/generated/' || true)" && [[ -n "${matches}" ]]; then
  printf '%s\n' 'production HTTP routes must be registered through generated/loomapi:'
  printf '%s\n' "${matches}"
  exit 1
fi

registrars="$(search_production_go 'RegisterHandlers(WithOptions)?\(' || true)"
expected='internal/server/routes.go:'
if [[ "${registrars}" != "${expected}"* ]] || [[ "$(printf '%s\n' "${registrars}" | wc -l | tr -d ' ')" != '1' ]]; then
  printf '%s\n' 'expected exactly one production generated route registrar in internal/server/routes.go:'
  printf '%s\n' "${registrars}"
  exit 1
fi
