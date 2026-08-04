#!/usr/bin/env bash
# Blocking pre-handoff check. Exits non-zero on anything that must not regress.
# copyright:check is deliberately excluded: it is already red at v1.0.0-rc.23,
# so it cannot distinguish our breakage from upstream's. See AGENT-WORKFLOW.md.
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH="$HOME/.bun/bin:$PATH"
export GOWORK=off

echo "--> brand invariants (licence-critical)"
node web/scripts/check-brand-invariants.mjs

echo "--> go vet"
go vet ./...

echo "--> go test"
go test ./model/ -count=1

echo "--> web typecheck"
(cd web && bun run typecheck)

echo "--> web build (also regenerates the TanStack route tree)"
(cd web && bun run build >/dev/null)

echo "--> go build (needs web/dist; //go:embed)"
CGO_ENABLED=0 go build -o /dev/null .

echo "OK"
