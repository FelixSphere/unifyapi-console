# UnifyAI performance benchmark vs OpenRouter

Dependency-free (stdlib + `aws` CLI). Runs unchanged on the dev Mac or on an EC2
box in any region, which matters — most of the measurable gap is a function of
*where the client is*.

Lives outside `web/` and `console/` on purpose: both are shared checkouts that
other agents merge from, and benchmark scratch should not land in either.

## The three tools

| tool | needs a key? | answers |
|---|---|---|
| `netpath.py` | no | What does the *network path* cost? DNS/TCP/TLS/TTFB, cold vs warm. |
| `bench.py` | yes | Client-observed TTFT + round-trip per model, with phase breakdown. |
| `monitor.sh` | no (needs AWS SSM) | What did the *gateway itself* see? Pulls `perf_metrics` + channel health. |

## The decomposition that makes results actionable

```
client TTFT  =  [ DNS + TCP + TLS ]  +  [ gateway ]  +  [ upstream provider ]
                └── netpath.py ────┘   └── monitor.sh (gw_ttft_ms) ────────┘
                └────────────────── bench.py (ttft_total_ms) ─────────────┘
```

`bench.py` ttft − `monitor.sh` gw_ttft = **the network tax**. That is the number
an edge/CDN terminator removes, and nothing else does. Everything left over is
gateway overhead or the upstream provider, and no amount of CDN touches it.

## Usage

```bash
export UNIFYAI_API_KEY=sk-...
export OPENROUTER_API_KEY=sk-or-...
export PERF_VANTAGE="hk-office"        # label the measurement location

python3 netpath.py -n 20 --json results/netpath-$(date +%F).json
python3 bench.py --all -n 5 --json results/bench-$(date +%F).json
./monitor.sh 7
```

Scenarios (`--scenario`): `ttft` (default, short streaming prompt),
`roundtrip` (non-streaming), `long_prompt` (~2k tokens, surfaces prefill),
`sustained` (400 tokens, measures TPS and inter-token latency).

`--warm` reuses one connection across repeats. **Always run both**: cold is what a
benchmark script measures, warm is what a long-lived agent client actually gets.
The cold/warm delta *is* the edge opportunity.

## Fairness rules

- Both sides must resolve to the same underlying model. Check `model_map` in
  `targets.json` before trusting any comparison.
- HTTP/1.1 is forced on both sides so the protocol is not a confound.
- `temperature: 0` and a pinned `max_tokens`, so generation length cannot drift.
- Record `PERF_VANTAGE`. A number without a location is not a measurement.
- Report p50 **and** p95 with n. A single sample of a 30-request/week model is
  noise, not a result.
