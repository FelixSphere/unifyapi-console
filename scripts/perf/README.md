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

Both `netpath.py` and `bench.py` take `--proxy HOST:PORT`. **Pass the same value
to both, or they measure different network paths and their numbers cannot be
compared.** Raw sockets ignore `HTTP_PROXY`/`HTTPS_PROXY`, so the exit is never
implicit — each run records its exit in the JSON as `"exit"`.

Scenarios (`--scenario`): `ttft` (default, short streaming prompt),
`roundtrip` (non-streaming), `long_prompt` (~2k tokens, surfaces prefill),
`sustained` (400 tokens, measures TPS and inter-token latency).

`--warm` reuses one connection across repeats. **Always run both**: cold is what a
benchmark script measures, warm is what a long-lived agent client actually gets.
The cold/warm delta *is* the edge opportunity.

## Load testing

```bash
python3 loadtest.py --dry-run              # always preview the plan first
python3 loadtest.py --steps 20,40,80 -n 5
```

Targets `app.unifyapi.ai` and **refuses any other host**. A ramp against a third
party spends capacity that is not ours and is very likely a ToS violation;
`bench.py`'s low-concurrency comparison is normal API use, a ramp is not. Note
that loading our gateway also loads Flatkey, since every relay forwards
upstream -- hence small, capped ramps on the cheapest healthy channel.

The breaker aborts the run on the first sign of distress: error rate over 20%,
p95 over 30s, or any 5xx/429.

**What the committed baseline does and does not show.** At 31 RPS the console
container peaked at 12.46% CPU and 2.42% memory, load 0.35 on two cores --
enormous headroom, and CPU will not be the binding constraint. But two limits
were not reached: the ramp saturated the *load generator* (80 CPython threads,
GIL-bound: 31 RPS observed against 47 implied by p50), and the requests were
`max_tokens=16` toys, roughly 500x smaller than the 105.6 KB production
average, so the 128 Mbps network baseline was never approached. Finding the
real ceiling needs a mock upstream, a non-production target, and a
multi-process generator.

## The local rig

```bash
./rig.sh up            # console :3009 + mock upstream :8899, ~40s
./rig.sh test          # one end-to-end request
./rig.sh load --steps 10,25,50,100,150 -n 20
./rig.sh down
```

Everything local: SQLite, no Redis, a mock upstream answering in 50ms. Removes
the three things that make a production ramp useless or unsafe -- it does not
load Flatkey, it cannot take the single production instance down, and provider
latency no longer dominates the measurement.

**Measured knee.** Throughput plateaus at **~100-127 RPS** from concurrency 25
onward while p50 grows roughly linearly (0.13s to 1.09s at concurrency 150) --
a saturation plateau, not a cliff. At the knee the console process consumed
**110-133% CPU (~1.2 cores)** while the mock sat at 3.6-5.4%, so the gateway is
genuinely the bottleneck rather than the rig around it.

That works out to **~85-106 RPS per core**. On the production t4g.small (2 vCPU)
that extrapolates to **~170-210 RPS CPU-bound**, against **148 RPS network-bound**
from the 128 Mbps baseline. **Network binds first**, so the ~8,900 RPM / ~41M TPM
figure stands -- now with a measured cross-check that CPU is not the limit.

Caveats worth keeping: the rig runs generator, gateway and mock on one 8-core
Intel Mac, so they compete; production is arm64 Graviton; the mock returns
16-token payloads rather than the 105.6 KB production average; and SQLite is
cheaper than Postgres-over-network. Treat the per-core number as indicative.

## Tests

```bash
python3 test_bench.py
```

Stdlib `unittest`, no deps, no network. Pins every path through the body reader,
which is the one place a bug is invisible: a truncated read still produces a 200
with plausible timings, so the harness reports a clean success for exactly the
stalled upstream it was built to detect.

Enforced, not advisory: the `perf-tools` job in `.github/workflows/fork-ci.yml`
runs this on every PR and every push, and a failure blocks the merge. Anything
weaker would be worse than nothing here -- a benchmark you cannot trust is one
you will act on anyway.

## Fairness rules

- Both sides must resolve to the same underlying model. Check `model_map` in
  `targets.json` before trusting any comparison.
- HTTP/1.1 is forced on both sides so the protocol is not a confound.
- `temperature: 0` and a pinned `max_tokens`, so generation length cannot drift.
- Record `PERF_VANTAGE`. A number without a location is not a measurement.
- Report p50 **and** p95 with n. A single sample of a 30-request/week model is
  noise, not a result.
