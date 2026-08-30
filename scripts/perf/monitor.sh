#!/usr/bin/env bash
# monitor.sh -- one snapshot of UnifyAI's own gateway-side performance telemetry.
#
# The console already records TTFT / latency / success-rate / TPS per model in
# `perf_metrics` (pkg/perf_metrics). This pulls it, plus per-channel health, so a
# client-side bench.py run can be diffed against what the gateway itself saw:
#
#     client_ttft - gateway_ttft = the network tax an edge/CDN can remove
#     gateway_ttft               = gateway overhead + upstream provider
#
# Usage: ./monitor.sh [days]     (default 7)
set -euo pipefail
DAYS="${1:-7}"
INSTANCE="${UNIFYAI_INSTANCE:-i-0469afe3c6b3bec23}"
PSQL="docker exec unifyapi-postgres-1 psql -U unifyapi -d unifyapi -P pager=off"

run() {
  local cid
  cid=$(aws ssm send-command --instance-ids "$INSTANCE" \
        --document-name AWS-RunShellScript \
        --parameters "commands=[\"$1\"]" \
        --query 'Command.CommandId' --output text)
  for _ in $(seq 1 30); do
    sleep 2
    status=$(aws ssm get-command-invocation --command-id "$cid" --instance-id "$INSTANCE" \
             --query 'Status' --output text 2>/dev/null || echo Pending)
    case "$status" in
      Success|Failed) break ;;
    esac
  done
  aws ssm get-command-invocation --command-id "$cid" --instance-id "$INSTANCE" \
      --query 'StandardOutputContent' --output text
}

echo "═══ gateway-side per-model performance (last ${DAYS}d) ═══"
run "$PSQL -c \\\"select model_name, sum(request_count) reqs, round(100.0*sum(success_count)/nullif(sum(request_count),0),1) ok_pct, round(sum(ttft_sum_ms)::numeric/nullif(sum(ttft_count),0)) gw_ttft_ms, round(sum(total_latency_ms)::numeric/nullif(sum(request_count),0)) gw_lat_ms, round(sum(output_tokens)::numeric/nullif(sum(generation_ms),0)*1000,1) tps from perf_metrics where bucket_ts > extract(epoch from now())-${DAYS}*86400 group by model_name having sum(ttft_count)>0 order by gw_ttft_ms desc nulls last\\\""

echo
echo "═══ channel health (response_time = last health-check latency) ═══"
run "$PSQL -c \\\"select id,name,type,status,priority,weight,response_time,to_timestamp(test_time) tested from channels order by response_time desc\\\""

echo
echo "═══ host headroom ═══"
run "uptime; docker stats --no-stream --format '{{.Name}} cpu={{.CPUPerc}} mem={{.MemPerc}}'"
