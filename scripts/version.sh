#!/usr/bin/env bash
# Écrit version.env (VERSION, COMMIT, SOURCE_DATE_EPOCH) depuis git : tag vX.Y.Z pour une release,
# sinon description git ; la date de build est celle du dernier commit (build reproductible).
set -euo pipefail
cd "$(dirname "$0")/.."
if git rev-parse --git-dir >/dev/null 2>&1; then
  ver=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
  commit=$(git rev-parse --short HEAD 2>/dev/null || echo none)
  epoch=$(git log -1 --format=%ct 2>/dev/null || date +%s)
else
  ver=dev; commit=none; epoch=$(date +%s)
fi
printf 'VERSION=%s\nCOMMIT=%s\nSOURCE_DATE_EPOCH=%s\n' "$ver" "$commit" "$epoch" > version.env
echo "$ver"
