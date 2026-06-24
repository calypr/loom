#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

find . -name .DS_Store -type f -delete

git add -A -- .gitignore
git add -u -- '**/.DS_Store' 2>/dev/null || true
git add -u
