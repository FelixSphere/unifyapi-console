#!/usr/bin/env bash
# Keep the packages that decide what a customer is invoiced well tested. This
# deliberately gates the business-critical packages instead of pretending the
# inherited application already has 90% whole-repository coverage.
#
# Two packages, two floors:
#
#   pkg/billingexpr        90% -- the tiered/dynamic billing engine. Its
#                                 ceiling is ~92% as go test measures it:
#                                 nine of its statements are stub bodies in
#                                 the compile-time type environment
#                                 (func(string) string { return "" }) that by
#                                 design never execute.
#   setting/ratio_setting  95% -- the price catalogue and every accessor the
#                                 relay bills through. No structural ceiling;
#                                 a drop here means a pricing branch shipped
#                                 without a test.
#
# A floor is not a target. Raising one is fine; lowering one needs a reason in
# the commit message.
set -euo pipefail

cd "$(dirname "$0")/.."
export GOWORK=off

read_coverage() {
  printf '%s\n' "$1" | awk '
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
  '
}

failed=0

check() {
  local package="$1" threshold="$2" output coverage status

  # Capture the status rather than letting `set -e` abort here. A genuine test
  # failure must print the go test output and say which package failed: an
  # earlier version of this script died inside the command substitution, so CI
  # showed the previous package's success line and a bare exit 1, naming
  # neither the package nor the failing test.
  set +e
  output="$(go test "./${package}" -count=1 -cover 2>&1)"
  status=$?
  set -e

  printf '%s\n' "$output"

  if [[ "$status" -ne 0 ]]; then
    echo "${package}: tests failed (exit ${status}); coverage not evaluated" >&2
    failed=1
    return
  fi

  coverage="$(read_coverage "$output" || true)"
  if [[ -z "$coverage" ]]; then
    echo "could not read coverage for ${package} from go test output" >&2
    failed=1
    return
  fi

  if ! awk -v actual="$coverage" -v minimum="$threshold" 'BEGIN { exit !(actual >= minimum) }'; then
    echo "${package} coverage ${coverage}% is below the ${threshold}% floor" >&2
    failed=1
    return
  fi

  echo "${package} coverage ${coverage}% meets the ${threshold}% floor"
}

check pkg/billingexpr 90.0
check setting/ratio_setting 95.0

exit "$failed"
