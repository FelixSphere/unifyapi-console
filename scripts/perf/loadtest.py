#!/usr/bin/env python3
"""
loadtest.py -- bounded concurrency ramp against OUR gateway, with a circuit breaker.

Answers the one question the capacity arithmetic cannot: where is the actual
knee? Every RPS figure we have so far is derived from resource limits
(128 Mbps network baseline / 105.6 KB per request = ~148 RPS). This measures it.

  SCOPE, deliberately narrow
  --------------------------
  Targets app.unifyapi.ai ONLY. It will refuse any other host.

  Do not point this at OpenRouter or any other third party. Consuming someone
  else's production capacity is not ours to spend, it is very likely a ToS
  violation, and the bill lands on whoever owns the key. Low-concurrency
  comparison (bench.py) is normal API use; a ramp is not.

  Note that loading our gateway also loads Flatkey, since every relay forwards
  upstream. That is why the ramp is small, capped, and uses the healthiest,
  cheapest channel. Measuring OUR ceiling properly needs a mock upstream and a
  non-production target -- see README. This tool finds the floor of the knee,
  not the true maximum.

  SAFETY
  ------
  The breaker aborts the whole run on the first step that shows real distress,
  so a ramp cannot walk a production box into the ground:

    - error rate over --max-error-rate (default 20%)
    - p95 latency over --max-p95 (default 30s)
    - any 5xx at all (upstream 429/503 included -- that is the signal to stop)

  Total requests are bounded by the ramp, printed before anything is sent, and
  --dry-run shows the plan without issuing a request.

Usage:
  export UNIFYAI_API_KEY=sk-...
  python3 loadtest.py --dry-run
  python3 loadtest.py --model gemini-flash-lite-latest --steps 1,2,5,10 -n 20
"""
import argparse
import json
import os
import ssl
import statistics
import sys
import threading
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone

ALLOWED_HOSTS = {"app.unifyapi.ai"}
DEFAULT_URL = "https://app.unifyapi.ai/v1/chat/completions"


def pct(values, p):
    if not values:
        return None
    v = sorted(values)
    if len(v) == 1:
        return v[0]
    k = (len(v) - 1) * p
    f = int(k)
    return v[f] + (v[min(f + 1, len(v) - 1)] - v[f]) * (k - f)


def one_request(url, key, model, timeout):
    """Single non-streaming request. Returns (ok, seconds, status, note)."""
    payload = json.dumps({
        "model": model,
        "messages": [{"role": "user", "content": "Say the single word: ready"}],
        "max_tokens": 16,
        "temperature": 0,
        "stream": False,
    }).encode()
    req = urllib.request.Request(url, data=payload, method="POST", headers={
        "Authorization": f"Bearer {key}",
        "Content-Type": "application/json",
        "User-Agent": "unifyai-loadtest/1.0",
    })
    t0 = time.perf_counter()
    try:
        with urllib.request.urlopen(req, timeout=timeout, context=ssl.create_default_context()) as r:
            r.read()
            return True, time.perf_counter() - t0, r.status, ""
    except urllib.error.HTTPError as e:
        body = ""
        try:
            body = e.read()[:120].decode("utf-8", "replace")
        except Exception:
            pass
        return False, time.perf_counter() - t0, e.code, body
    except Exception as e:
        return False, time.perf_counter() - t0, 0, f"{type(e).__name__}: {e}"


def run_step(url, key, model, concurrency, per_worker, timeout):
    """One rung of the ramp. Returns aggregate stats."""
    results, lock = [], threading.Lock()

    def worker():
        local = []
        for _ in range(per_worker):
            local.append(one_request(url, key, model, timeout))
        with lock:
            results.extend(local)

    threads = [threading.Thread(target=worker, daemon=True) for _ in range(concurrency)]
    t0 = time.perf_counter()
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    wall = time.perf_counter() - t0

    lat = [r[1] for r in results if r[0]]
    errs = [r for r in results if not r[0]]
    server_errs = [r for r in errs if r[2] >= 500 or r[2] == 429]
    return {
        "concurrency": concurrency,
        "requests": len(results),
        "wall_s": wall,
        "rps": len(results) / wall if wall else 0,
        "ok": len(lat),
        "errors": len(errs),
        "error_rate": len(errs) / len(results) if results else 0,
        "server_errors": len(server_errs),
        "p50": pct(lat, 0.50),
        "p95": pct(lat, 0.95),
        "mean": statistics.fmean(lat) if lat else None,
        "samples": errs[:3],
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--url", default=DEFAULT_URL)
    ap.add_argument("--model", default="gemini-flash-lite-latest",
                    help="use the healthiest, cheapest channel; this measures the "
                         "gateway, not the model")
    ap.add_argument("--steps", default="1,2,5,10",
                    help="concurrency rungs, comma separated")
    ap.add_argument("-n", "--per-worker", type=int, default=10,
                    help="requests per worker per step")
    ap.add_argument("--timeout", type=float, default=60.0)
    ap.add_argument("--settle", type=float, default=3.0,
                    help="seconds to idle between steps")
    ap.add_argument("--max-error-rate", type=float, default=0.20)
    ap.add_argument("--max-p95", type=float, default=30.0)
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--json", default=None)
    args = ap.parse_args()

    host = args.url.split("/")[2].split(":")[0]
    if host not in ALLOWED_HOSTS:
        ap.error(f"refusing to load test {host!r}. This tool targets our own "
                 f"gateway only ({', '.join(sorted(ALLOWED_HOSTS))}); pointing a "
                 f"ramp at a third party spends capacity that is not ours.")

    steps = [int(s) for s in args.steps.split(",") if s.strip()]
    total = sum(c * args.per_worker for c in steps)

    print(f"target      {args.url}")
    print(f"model       {args.model}")
    print(f"ramp        {steps} concurrent x {args.per_worker} req/worker")
    print(f"TOTAL       {total} requests (all forwarded upstream to Flatkey)")
    print(f"breaker     abort if error rate > {args.max_error_rate:.0%}, "
          f"p95 > {args.max_p95:.0f}s, or any 5xx/429\n")

    if args.dry_run:
        print("dry run -- nothing sent")
        return 0

    key = os.environ.get("UNIFYAI_API_KEY", "")
    if not key:
        print("UNIFYAI_API_KEY is not set", file=sys.stderr)
        return 2

    out = {"utc": datetime.now(timezone.utc).isoformat(timespec="seconds"),
           "url": args.url, "model": args.model, "steps": []}
    hdr = f"{'conc':>5} {'req':>5} {'RPS':>7} {'p50':>8} {'p95':>8} {'err':>6} {'5xx/429':>8}"
    print(hdr)
    print("-" * len(hdr))

    aborted = None
    for c in steps:
        s = run_step(args.url, key, args.model, c, args.per_worker, args.timeout)
        out["steps"].append(s)
        f = lambda v: f"{v:>8.2f}" if v is not None else f"{'-':>8}"
        print(f"{c:>5} {s['requests']:>5} {s['rps']:>7.1f} {f(s['p50'])} {f(s['p95'])} "
              f"{s['error_rate']:>5.0%} {s['server_errors']:>8}")

        if s["server_errors"] > 0:
            aborted = f"{s['server_errors']} server errors (5xx/429) at concurrency {c}"
        elif s["error_rate"] > args.max_error_rate:
            aborted = f"error rate {s['error_rate']:.0%} at concurrency {c}"
        elif s["p95"] is not None and s["p95"] > args.max_p95:
            aborted = f"p95 {s['p95']:.1f}s at concurrency {c}"
        if aborted:
            for e in s["samples"]:
                print(f"      !! HTTP {e[2]}: {e[3][:100]}")
            print(f"\nBREAKER TRIPPED: {aborted}")
            print("Stopping the ramp. The knee is at or below this rung.")
            out["aborted"] = aborted
            break
        time.sleep(args.settle)

    if not aborted:
        best = max(out["steps"], key=lambda s: s["rps"])
        print(f"\nRamp completed with no distress. Highest observed "
              f"{best['rps']:.1f} RPS at concurrency {best['concurrency']}.")
        print("This is a floor, not the ceiling -- the ramp ended before the knee.")

    if args.json:
        os.makedirs(os.path.dirname(args.json) or ".", exist_ok=True)
        json.dump(out, open(args.json, "w"), indent=2)
        print(f"raw -> {args.json}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
