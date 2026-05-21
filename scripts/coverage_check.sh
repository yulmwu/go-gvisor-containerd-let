#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

mkdir -p .artifacts
RAW=.artifacts/coverage.raw.out
OUT=.artifacts/coverage.out

go test -covermode=atomic -coverprofile="$RAW" ./...

{
  head -n1 "$RAW"
  tail -n +2 "$RAW" | grep -Ev \
    '(^sandboxd-o/cmd/|^sandboxd-o/scripts/|^sandboxd-o/docs/|^sandboxd-o/assets/|^sandboxd-o/.*/router\.go|^sandboxd-o/.*/server\.go|^sandboxd-o/.*/response\.go|^sandboxd-o/.*/types\.go|^sandboxd-o/.*/dto\.go|^sandboxd-o/sandboxd-let/network/|^sandboxd-o/sandboxd-let/sandbox/|^sandboxd-o/sandboxd-let/http/handlers\.go|^sandboxd-o/sandboxd-orch/docs/|^sandboxd-o/sandboxd-let/docs/|^sandboxd-o/sandboxd-ctl/)'
} > "$OUT"

go tool cover -func="$OUT"
TOTAL=$(go tool cover -func="$OUT" | awk '/^total:/ {gsub("%","",$3); print $3}')
MIN=${COVERAGE_MIN:-70}

awk -v t="$TOTAL" -v m="$MIN" 'BEGIN { if (t+0 < m+0) { printf("coverage %.2f%% is below minimum %.2f%%\n", t, m); exit 1 } else { printf("coverage %.2f%% >= %.2f%%\n", t, m) } }'
