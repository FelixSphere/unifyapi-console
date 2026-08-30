# Baseline notes — read before comparing against anything here

`results/baseline-*.json` are the only measurement files in git, and they are
kept so a later run has something to compare against. This file records what
each one is worth, because **a benchmark number is meaningless without knowing
which version of the harness produced it.**

## Which committed baseline is which

| file | what it is |
|---|---|
| `baseline-20260830-proxy.json` | **The canonical head-to-head.** 15 model pairs, n=5, UnifyAI and OpenRouter both through the same proxy exit, taken after all four harness fixes below |
| `baseline-20260830-rig-ramp.json` | Load-test ramp against the local rig — where the CPU knee is |
| `baseline-20260830-loadtest-low.json` / `-high.json` | The two ends of that ramp |

## Four harness bugs, and why earlier runs were discarded

Four bugs were found and fixed in the harness on 2026-08-30. Runs made before
each fix measure the bug as much as they measure the system, which is why only
the post-fix run above is in git. Recorded here so nobody re-derives them:

1. **TTFT counted only content tokens.** Most reasoning models stream
   `reasoning` deltas before any `content`. At `max_tokens 16` they spend the
   whole budget thinking and never emit a content token, so TTFT was really
   measuring *time until the model stopped reasoning* — unbounded. Counting the
   first token of any kind moved `qwen3.7-max` from 22.35 s to 2.11 s.
2. **ALPN mismatch in `netpath.py`.** It negotiated h2 then spoke HTTP/1.1, so
   TTFB and warm figures were HTTP/2 frames misread as text. The DNS, TCP and
   TLS phases were unaffected.
3. **Keepalive never actually reused.** An unconsumed chunked trailer broke
   reuse, so every "warm" sample was in fact cold.
4. **Throughput reported burst artifacts** — 34,000 tok/s, which is a buffer
   flush, not a generation rate.

## What these files do *not* establish

- **No clean warm-vs-cold delta.** Keepalive only started working at the last
  fix, so the +102 ms connection tax has never been confirmed from the warm
  direction.
- **The network ceiling is arithmetic, not measurement.** The load-test rig used
  16-token payloads, far smaller than a real request, so it exercised the CPU
  ceiling and never approached the network path.

## Environment caveats that apply to every file here

- Taken from a dev Mac behind a proxy at `127.0.0.1:7897`. Raw sockets ignore
  the proxy; `curl` does not. OpenRouter geo-blocks Gemini, Claude and GPT on
  the direct path from that vantage, so any comparison covering those models
  must pass `--proxy` to **both** sides or it is not like-for-like.
- Production is arm64 (`t4g.small`); that Mac is x86_64. Absolute numbers do not
  transfer — only the A/B relationship does.
