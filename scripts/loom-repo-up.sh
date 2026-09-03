#!/usr/bin/env bash
set -Eeuo pipefail

loom_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
repository=$PWD

arguments=()
while (($#)); do
  case "$1" in
    --repository)
      (($# >= 2)) || { echo "--repository requires a path" >&2; exit 2; }
      repository=$2
      shift 2
      ;;
    *)
      arguments+=("$1")
      shift
      ;;
  esac
done

cd "$loom_root"
set +u
GOTOOLCHAIN=auto go run ./cmd/loom-repository launch \
  --loom-root "$loom_root" \
  --repository "$repository" \
  "${arguments[@]}"
