#!/usr/bin/env bash
# rig.sh -- stand up a local load-test rig: console + mock upstream, no network deps.
#
# UNIFYAPI-FORK: fork-owned test infrastructure.
#
# Why a rig at all. Ramping against production has three problems, and this
# removes all of them:
#   - every relay forwards upstream, so loading our gateway loads Flatkey, a
#     reseller already rate-limiting us
#   - the box is a single instance with no ASG, so a ramp risks real downtime
#   - provider latency dominates, which is the opposite of a capacity test
#
# Everything here is local: SQLite, no Redis, a mock upstream that answers in
# 50ms. The only thing under load is our own relay path.
#
#   ./rig.sh up      build and start (console :3009, mock :8899)
#   ./rig.sh test    one end-to-end request
#   ./rig.sh load    ramp against the rig
#   ./rig.sh down    stop everything
#   ./rig.sh status  what is running
#
# Ports are 3009/8899 rather than 3000/3001 so this cannot collide with another
# agent's dev server -- see AGENT-WORKFLOW.md.
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
RIG="${RIG_DIR:-${TMPDIR:-/tmp}/unifyapi-loadtest-rig}"
PORT="${RIG_PORT:-3009}"
MOCK_PORT="${RIG_MOCK_PORT:-8899}"
TOKEN="loadtestkey000000000000000000000"
DB="$RIG/lt.db"

log() { printf '  %s\n' "$*"; }

build() {
  mkdir -p "$RIG"
  # main.go embeds web/dist; a stub satisfies the embed without a frontend build,
  # which we do not need since nothing here touches the UI. web/dist is gitignored.
  if [ ! -f "$REPO/web/dist/index.html" ]; then
    mkdir -p "$REPO/web/dist"
    printf '<!doctype html><title>loadtest rig stub</title>\n' > "$REPO/web/dist/index.html"
  fi
  log "building console (~30s)"
  (cd "$REPO" && go build -o "$RIG/console" .)
  log "building mock upstream"
  (cd "$REPO" && go build -o "$RIG/mock" ./scripts/perf/mockupstream/)
}

seed() {
  # Seeded directly rather than through the admin API: the fork's tenancy model
  # does not accept a plain session cookie on admin routes, and this rig is a
  # throwaway SQLite file.
  #
  # channel_info is CAST to BLOB deliberately. ChannelInfo.Scan does
  # `value.([]byte)` and discards the failure; glebarez/sqlite hands back a
  # string for TEXT, so the assertion yields nil and every channel load fails
  # with "unexpected end of JSON input". Storing a BLOB makes the assertion
  # hold. This is an upstream bug that only bites on SQLite -- Postgres returns
  # []byte -- but SQLite is what you get when SQL_DSN is unset.
  local now; now=$(date +%s)
  sqlite3 "$DB" "
    DELETE FROM channels;  DELETE FROM abilities;  DELETE FROM tokens;
    INSERT INTO channels (id,type,\`key\`,status,name,weight,created_time,base_url,
                          models,\`group\`,priority,auto_ban,channel_info,setting,
                          settings,model_mapping,status_code_mapping,param_override,
                          header_override,other)
    VALUES (1,1,'sk-mock',1,'mock-upstream',0,$now,'http://127.0.0.1:$MOCK_PORT',
            'mock-model','default',0,0,
            CAST('{\"is_multi_key\":false,\"multi_key_size\":0,\"multi_key_polling_index\":0,\"multi_key_mode\":\"\"}' AS BLOB),
            '{}','{}','','','','','');
    INSERT INTO abilities (\`group\`,model,channel_id,enabled,priority,weight)
    VALUES ('default','mock-model',1,1,0,0);
    INSERT INTO tokens (id,user_id,\`key\`,status,name,created_time,accessed_time,
                        expired_time,remain_quota,unlimited_quota,model_limits_enabled,\`group\`)
    VALUES (1,1,'$TOKEN',1,'loadtest',$now,$now,-1,0,1,0,'default');
  "
}

start_console() {
  (cd "$RIG" && PORT="$PORT" SQLITE_PATH="$DB" GIN_MODE=release \
     SESSION_SECRET=loadtest-rig-not-a-secret "$RIG/console" > "$RIG/console.log" 2>&1 &)
  for _ in $(seq 1 20); do
    sleep 2
    curl -sf "http://127.0.0.1:$PORT/api/status" >/dev/null 2>&1 && return 0
  done
  log "console did not come up; tail of $RIG/console.log:"; tail -15 "$RIG/console.log"; return 1
}

case "${1:-up}" in
  up)
    "$0" down >/dev/null 2>&1 || true
    build
    log "starting mock upstream on :$MOCK_PORT"
    ("$RIG/mock" -addr "127.0.0.1:$MOCK_PORT" -latency "${MOCK_LATENCY:-50ms}" \
        -tokens 16 > "$RIG/mock.log" 2>&1 &)
    sleep 1
    log "first console start (creates the schema)"
    start_console
    # The fork requires explicit initialisation before a root user exists.
    curl -sf -X POST "http://127.0.0.1:$PORT/api/setup" -H 'Content-Type: application/json' \
      -d '{"username":"root","password":"LoadTest!2026","confirmPassword":"LoadTest!2026","SelfUseModeEnabled":true,"DemoSiteEnabled":false}' \
      >/dev/null || true
    log "seeding channel + token"
    seed
    # Restart so the in-memory channel cache picks up the seed.
    pkill -9 -f "$RIG/console" 2>/dev/null || true
    wait 2>/dev/null || true; sleep 2
    start_console
    log "rig up:  console http://127.0.0.1:$PORT   mock http://127.0.0.1:$MOCK_PORT"
    log "token:   $TOKEN"
    ;;
  test)
    curl -s -X POST "http://127.0.0.1:$PORT/v1/chat/completions" \
      -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
      -d '{"model":"mock-model","messages":[{"role":"user","content":"hi"}],"max_tokens":8}'
    echo; log "mock: $(curl -s "http://127.0.0.1:$MOCK_PORT/stats")"
    ;;
  load)
    shift
    UNIFYAI_API_KEY="$TOKEN" python3 "$REPO/scripts/perf/loadtest.py" \
      --url "http://127.0.0.1:$PORT/v1/chat/completions" --model mock-model \
      --processes "${RIG_PROCS:-6}" "$@"
    ;;
  down)
    pkill -9 -f "$RIG/console" 2>/dev/null || true
    pkill -9 -f "$RIG/mock" 2>/dev/null || true
    log "stopped"
    ;;
  status)
    c=$(lsof -nP -iTCP:"$PORT" -sTCP:LISTEN 2>/dev/null | tail -n +2 | wc -l | tr -d ' ')
    m=$(lsof -nP -iTCP:"$MOCK_PORT" -sTCP:LISTEN 2>/dev/null | tail -n +2 | wc -l | tr -d ' ')
    log "console :$PORT      $([ "$c" -gt 0 ] && echo up || echo down)"
    log "mock    :$MOCK_PORT      $([ "$m" -gt 0 ] && echo up || echo down)"
    [ "$c" -gt 0 ] && log "health: $(curl -sf "http://127.0.0.1:$PORT/api/status" >/dev/null 2>&1 && echo ok || echo unreachable)"
    ;;
  *)
    echo "usage: $0 {up|test|load|down|status}" >&2; exit 2 ;;
esac
