#!/usr/bin/env bash
set -euo pipefail

script=$1
shift
pair=$(mktemp -d "tests/scripts/.loom-script-test.XXXXXX")
trap 'rm -rf "$pair"' EXIT

cp "scripts/$script.go" "tests/scripts/${script}_test.go" "$pair/"
"${GO:-go}" test "$@" "$pair/$script.go" "$pair/${script}_test.go"
