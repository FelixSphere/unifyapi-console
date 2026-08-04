#!/usr/bin/env bash
# Snapshot which gates are red RIGHT NOW, so an agent can tell inherited
# breakage from breakage it caused. Never fails; it reports.
set -uo pipefail
cd "$(dirname "$0")/.."
export PATH="$HOME/.bun/bin:$PATH"
export GOWORK=off

echo "=== brand-invariants ==="
node web/scripts/check-brand-invariants.mjs 2>&1 || true
echo "=== go vet ==="
go vet ./... 2>&1 | grep -v '^#' || true
echo "=== go test ./model/ ==="
go test ./model/ -count=1 2>&1 | tail -1 || true
echo "=== web typecheck ==="
(cd web && bun run typecheck 2>&1 | grep -E 'error|^\$' | head -20) || true
echo "=== web copyright:check (RED AT BASELINE on 2 upstream files) ==="
(cd web && node scripts/add-copyright.mjs --check 2>&1 | tail -5) || true
