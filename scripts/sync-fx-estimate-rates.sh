#!/usr/bin/env bash
# Keep the wallet page's currency estimates near what Stripe will actually
# quote. Run on the console instance (needs the postgres container), e.g. daily
# from cron after the ECB publishes (~15:30 UTC).
#
#   estimate rate = ECB reference (USD -> currency) x (1 + MARGIN)
#
# These rates NEVER price a charge. Every checkout is created in the account's
# settlement currency and Stripe Adaptive Pricing localises it at Stripe's own
# guaranteed rate; StripeCurrencies only decides what the wallet page quotes
# before the user gets there. So this carries no FX risk — the worst outcome of
# a wrong rate is a misleading estimate, not an underpriced sale.
#
# MARGIN is the TOP of Stripe's documented 2-4% conversion band, deliberately.
# The buyer pays that markup, so an estimate at bare mid-market reads cheaper
# than the checkout page and looks like a bait; quoting at the top means the
# amount at Stripe is the same or lower than promised. Observed 2026-08-30:
# Stripe presented CNY at 6.9000 against an ECB mid of 6.7209, i.e. +2.67%.
#
# Writes the option row; model.SyncOptions picks it up within SYNC_FREQUENCY
# (60s), so no restart and no release. Deliberately conservative — every
# failure mode leaves the last known-good rate in place rather than guessing.
set -uo pipefail

# Not readonly: these are passed to the helper below as command-prefix
# environment assignments, which bash refuses to do for a readonly name.
MARGIN=0.04
BUFFER_NOTE="top of Stripe's documented 2-4% conversion band"
# Only rewrite when the target moves more than this, so a normal day's FX noise
# does not churn the displayed price.
MIN_CHANGE=0.01
# Refuse a move larger than this in one run. A feed glitch or a genuine currency
# event should stop and wait for a human, not silently reprice the product.
MAX_CHANGE=0.05
# Hard sanity band per currency: rate must land inside it or the run aborts.
declare -A BANDS=( [MYR]="3.0 5.5" )
CURRENCIES=(MYR)

readonly PSQL=(docker exec -i unifyapi-postgres-1 psql -U unifyapi -d unifyapi -At -c)
readonly SOURCE="https://api.frankfurter.dev/v1/latest?base=USD"

log() { printf '%s sync-fx-estimate-rates: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }

fail() { log "ABORT: $*"; exit 1; }

# --- fetch the reference rates -------------------------------------------
symbols=$(IFS=,; echo "${CURRENCIES[*]}")
payload=$(curl -fsS --max-time 20 "${SOURCE}&symbols=${symbols}") \
  || fail "reference feed unreachable; leaving rates unchanged"

ref_date=$(printf '%s' "$payload" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("date",""))') \
  || fail "reference feed returned unparseable JSON"
[ -n "$ref_date" ] || fail "reference feed returned no date"

# ECB publishes on business days only; a weekend run legitimately sees Friday.
age_days=$(( ( $(date -u +%s) - $(date -u -d "$ref_date" +%s) ) / 86400 ))
if [ "$age_days" -gt 5 ]; then
  fail "reference rate is ${age_days} days old (${ref_date}); feed looks stale"
fi

# --- read what is live now ------------------------------------------------
current_json=$("${PSQL[@]}" "select value from options where key='StripeCurrencies'" 2>/dev/null)
[ -n "$current_json" ] || current_json='[]'

# --- compute, guard, and emit the new list --------------------------------
new_json=$(
  REF_PAYLOAD="$payload" CURRENT_JSON="$current_json" \
  MARGIN="$MARGIN" MIN_CHANGE="$MIN_CHANGE" MAX_CHANGE="$MAX_CHANGE" \
  CURRENCIES="${CURRENCIES[*]}" BANDS_RAW="$(declare -p BANDS)" \
  python3 - <<'PY'
import json, os, sys, re

ref = json.loads(os.environ["REF_PAYLOAD"])["rates"]
try:
    current = {c["code"].upper(): float(c["rate"])
               for c in json.loads(os.environ["CURRENT_JSON"])
               if isinstance(c, dict) and "code" in c and "rate" in c}
except Exception:
    current = {}

margin = float(os.environ["MARGIN"])
min_change = float(os.environ["MIN_CHANGE"])
max_change = float(os.environ["MAX_CHANGE"])
bands = dict(re.findall(r'\[(\w+)\]="([\d. ]+)"', os.environ["BANDS_RAW"]))

out, notes, blocked = [], [], False
for code in os.environ["CURRENCIES"].split():
    if code not in ref:
        print(f"NOTE reference feed has no {code}; keeping current", file=sys.stderr)
        if code in current:
            out.append({"code": code, "rate": current[code]})
        continue

    target = round(ref[code] * (1 + margin), 2)

    lo, hi = (float(x) for x in bands[code].split())
    if not (lo <= target <= hi):
        print(f"BLOCK {code} target {target} outside sanity band {lo}-{hi}", file=sys.stderr)
        blocked = True
        if code in current:
            out.append({"code": code, "rate": current[code]})
        continue

    live = current.get(code)
    if live is None:
        notes.append(f"{code} new -> {target}")
        out.append({"code": code, "rate": target})
        continue

    move = abs(target - live) / live
    if move > max_change:
        print(f"BLOCK {code} {live} -> {target} is {move:.1%}, over the {max_change:.0%} "
              f"single-run limit; needs a human", file=sys.stderr)
        blocked = True
        out.append({"code": code, "rate": live})
    elif move < min_change:
        notes.append(f"{code} held at {live} (target {target}, {move:.2%} move)")
        out.append({"code": code, "rate": live})
    else:
        notes.append(f"{code} {live} -> {target} ({move:.2%})")
        out.append({"code": code, "rate": target})

for n in notes:
    print("NOTE " + n, file=sys.stderr)
print("BLOCKED" if blocked else "", file=sys.stderr)
print(json.dumps(out, separators=(",", ":"), sort_keys=True))
PY
) 2> >(while read -r l; do log "$l"; done)

[ -n "$new_json" ] || fail "rate computation produced nothing; leaving rates unchanged"

normalize() { python3 -c 'import json,sys;print(json.dumps(json.loads(sys.stdin.read() or "[]"),separators=(",",":"),sort_keys=True))'; }
if [ "$(printf '%s' "$current_json" | normalize)" = "$(printf '%s' "$new_json" | normalize)" ]; then
  log "no change (ref ${ref_date}, margin ${MARGIN}: ${BUFFER_NOTE}); live: ${new_json}"
  exit 0
fi

escaped=${new_json//\'/\'\'}
"${PSQL[@]}" "insert into options (key,value) values ('StripeCurrencies','${escaped}')
  on conflict (key) do update set value = excluded.value" >/dev/null \
  || fail "database write failed; rates unchanged"

log "updated from ref ${ref_date} (margin ${MARGIN}: ${BUFFER_NOTE}): ${new_json}"
log "takes effect within one SyncOptions interval (60s); no restart needed"
