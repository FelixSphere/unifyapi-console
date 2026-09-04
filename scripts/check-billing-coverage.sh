#!/usr/bin/env bash
# Keep the pure billing-expression engine well tested. This deliberately gates
# one business-critical package instead of pretending the inherited application
# already has 90% whole-repository coverage.
set -euo pipefail

cd "$(dirname "$0")/.."
export GOWORK=off

readonly threshold=90.0
output="$(go test ./pkg/billingexpr -count=1 -cover)"
printf '%s\n' "$output"

coverage="$({ printf '%s\n' "$output" | awk '
  /coverage:/ {
    for (i = 1; i <= NF; i++) {
      if ($i == "coverage:") {
        value = $(i + 1)
        sub(/%$/, "", value)
        print value
        exit
      }
    }
  }
'; } || true)"

if [[ -z "$coverage" ]]; then
  echo "could not read billing coverage from go test output" >&2
  exit 1
fi

if ! awk -v actual="$coverage" -v minimum="$threshold" 'BEGIN { exit !(actual >= minimum) }'; then
  echo "billingexpr coverage ${coverage}% is below the ${threshold}% floor" >&2
  exit 1
fi

echo "billingexpr coverage ${coverage}% meets the ${threshold}% floor"
