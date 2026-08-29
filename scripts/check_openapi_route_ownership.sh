#!/usr/bin/env bash
set -euo pipefail

if matches="$(rg -n '(^|[^[:alnum:]_])(app|router|[[:alnum:]_]+\.app|server\.App\(\))\.(Get|Post|Put|Delete|Patch|Head|Options|All)\(' internal \
  --glob '*.go' \
  --glob '!**/*_test.go' \
  --glob '!**/generated/**' || true)" && [[ -n "${matches}" ]]; then
  printf '%s\n' 'production HTTP routes must be registered through generated/loomapi:'
  printf '%s\n' "${matches}"
  exit 1
fi

registrars="$(rg -n 'RegisterHandlers(WithOptions)?\(' internal --glob '*.go' --glob '!**/*_test.go' || true)"
expected='internal/server/routes.go:'
if [[ "${registrars}" != "${expected}"* ]] || [[ "$(printf '%s\n' "${registrars}" | wc -l | tr -d ' ')" != '1' ]]; then
  printf '%s\n' 'expected exactly one production generated route registrar in internal/server/routes.go:'
  printf '%s\n' "${registrars}"
  exit 1
fi
