#!/usr/bin/env sh
set -eu

GOTOOLCHAIN=local go mod download
exec /go/bin/air -c /workspace/.air.dev.toml
