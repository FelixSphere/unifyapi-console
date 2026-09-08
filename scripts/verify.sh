#!/usr/bin/env bash
# Blocking pre-handoff check. Every listed gate must be green.
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH="$HOME/.bun/bin:$PATH"
export GOWORK=off

echo "--> brand invariants (licence-critical)"
node web/scripts/check-brand-invariants.mjs

echo "--> Bun test-runner imports"
node web/scripts/check-test-runner.mjs

echo "--> go vet"
go vet ./...

echo "--> go test"
go test ./model/ -count=1

echo "--> billing coverage"
scripts/check-billing-coverage.sh

echo "--> web typecheck"
(cd web && bun run typecheck)

echo "--> web copyright headers"
(cd web && bun run copyright:check)

echo "--> web formatting"
(cd web && bun run format:check)

echo "--> web tests"
(cd web && bun test)

echo "--> web build (also regenerates the TanStack route tree)"
(cd web && bun run build >/dev/null)

echo "--> go build (needs web/dist; //go:embed)"
CGO_ENABLED=0 go build -o /dev/null .

echo "OK"
