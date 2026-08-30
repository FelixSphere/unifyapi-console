#!/usr/bin/env python3
"""
bench.py -- client-observed TTFT and round-trip benchmark, UnifyAI vs OpenRouter.

Measures the same request against both gateways and decomposes where the time
goes, so a regression can be attributed instead of just observed:

    DNS -> TCP -> TLS -> request sent -> first byte -> FIRST TOKEN -> last token

  ttft_ms       t0 .. first non-empty content delta   <- the headline number
  roundtrip_ms  t0 .. stream complete
  connect_ms    DNS+TCP+TLS (the cold-start tax; what an edge/CDN removes)
  server_ms     request-sent .. first byte (gateway + upstream inference)
  tps           output tokens / (last_token - first_token)

Cross-check: UnifyAI's own /api/perf-metrics reports TTFT measured AT THE
GATEWAY. client_ttft - gateway_ttft = the network tax we can attack with an
edge. Anything left is gateway or upstream routing.

Dependency-free (stdlib only) so it runs unchanged on an EC2 box in any region.

Usage:
  export UNIFYAI_API_KEY=sk-...  OPENROUTER_API_KEY=sk-or-...
  python3 bench.py --case gpt-4o-mini -n 5
  python3 bench.py --all -n 5 --json results/$(date +%Y%m%d-%H%M).json
  python3 bench.py --case gpt-4o-mini --warm      # reuse the connection
"""
import argparse, json, os, socket, ssl, statistics, sys, time
from datetime import datetime, timezone

HERE = os.path.dirname(os.path.abspath(__file__))

# ---------------------------------------------------------------- scenarios
# Kept deliberately small and fixed. TTFT must not be polluted by prompt size
# or by how much the model decides to say; both are pinned.
SCENARIOS = {
    "ttft": {
        "desc": "Short prompt, streaming. Isolates time-to-first-token.",
        "messages": [{"role": "user", "content": "Say the single word: ready"}],
        "max_tokens": 16, "stream": True,
    },
    "roundtrip": {
        "desc": "Short prompt, non-streaming. Full request->complete response.",
        "messages": [{"role": "user", "content": "Say the single word: ready"}],
        "max_tokens": 16, "stream": False,
    },
    "long_prompt": {
        "desc": "~2k-token prompt, streaming. Surfaces upload + prefill cost.",
        "messages": [{"role": "user", "content": ("Summarize this in one word. " + "lorem ipsum dolor sit amet " * 300)}],
        "max_tokens": 16, "stream": True,
    },
    "sustained": {
        "desc": "Longer generation, streaming. Measures tokens/sec, not just TTFT.",
        "messages": [{"role": "user", "content": "Count from 1 to 100, one number per line."}],
        "max_tokens": 400, "stream": True,
    },
}

def ms(a, b):
    return (b - a) * 1000.0

# ------------------------------------------------------------- HTTP/1.1 I/O
class Conn:
    """Minimal HTTP/1.1-over-TLS client with per-phase timing.

    stdlib only, and HTTP/1.1 on both sides so the comparison is protocol-fair.
    (OpenRouter would negotiate h2 with a real client; for a single
    non-multiplexed request the difference is header compression only.)"""

    def __init__(self, host, port=443, timeout=120.0, proxy=None):
        self.host, self.port, self.timeout = host, port, timeout
        self.proxy = proxy          # ("127.0.0.1", 7897) or None
        self.sock = None
        self.buf = b""
        self.t = {}

    def connect(self):
        # Raw sockets ignore HTTP(S)_PROXY, so the exit point is explicit here.
        # It has to be: this client's proxy exits in a different country, and
        # OpenRouter geo-gates several providers -- measuring one side through
        # the tunnel and the other direct makes the comparison meaningless.
        t0 = time.perf_counter()
        dial_host, dial_port = self.proxy if self.proxy else (self.host, self.port)
        infos = socket.getaddrinfo(dial_host, dial_port, socket.AF_INET, socket.SOCK_STREAM)
        t_dns = time.perf_counter()
        fam, styp, proto, _, addr = infos[0]
        self.ip = addr[0]
        s = socket.socket(fam, styp, proto)
        s.settimeout(self.timeout)
        s.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
        s.connect(addr)
        if self.proxy:
            s.sendall(f"CONNECT {self.host}:{self.port} HTTP/1.1\r\n"
                      f"Host: {self.host}:{self.port}\r\n\r\n".encode())
            resp = b""
            while b"\r\n\r\n" not in resp:
                c = s.recv(4096)
                if not c:
                    raise ConnectionError("proxy closed during CONNECT")
                resp += c
            if b" 200 " not in resp.split(b"\r\n", 1)[0]:
                raise ConnectionError(f"proxy CONNECT failed: {resp.split(chr(13).encode())[0][:120]!r}")
        t_tcp = time.perf_counter()
        ctx = ssl.create_default_context()
        ctx.set_alpn_protocols(["http/1.1"])
        self.sock = ctx.wrap_socket(s, server_hostname=self.host)
        t_tls = time.perf_counter()
        self.tls_version = self.sock.version()
        self.t = {"dns_ms": ms(t0, t_dns), "tcp_ms": ms(t_dns, t_tcp),
                  "tls_ms": ms(t_tcp, t_tls), "connect_ms": ms(t0, t_tls)}
        return self

    def close(self):
        try:
            if self.sock:
                self.sock.close()
        except Exception:
            pass
        self.sock = None

    def _fill(self):
        c = self.sock.recv(65536)
        if not c:
            raise ConnectionError("connection closed by peer")
        self.buf += c
        return c

    def read_headers(self):
        """Returns (status_int, headers_dict, t_first_byte)."""
        t_fb = None
        while b"\r\n\r\n" not in self.buf:
            self._fill()
            if t_fb is None:
                t_fb = time.perf_counter()
        head, self.buf = self.buf.split(b"\r\n\r\n", 1)
        lines = head.decode("latin-1", "replace").split("\r\n")
        parts = lines[0].split(" ")
        if len(parts) < 2 or not parts[1].isdigit():
            raise ConnectionError(f"malformed status line {lines[0]!r} (peer likely closed keepalive)")
        status = int(parts[1])
        hdrs = {}
        for l in lines[1:]:
            if ":" in l:
                k, v = l.split(":", 1)
                hdrs[k.strip().lower()] = v.strip()
        return status, hdrs, t_fb

    def iter_body(self, hdrs):
        """Yield decoded body bytes, handling chunked transfer-encoding."""
        if hdrs.get("transfer-encoding", "").lower() == "chunked":
            while True:
                while b"\r\n" not in self.buf:
                    self._fill()
                line, self.buf = self.buf.split(b"\r\n", 1)
                try:
                    n = int(line.split(b";")[0].strip(), 16)
                except ValueError:
                    raise ConnectionError(f"bad chunk size {line!r}")
                if n == 0:
                    # consume trailers (if any) through the terminating blank line
                    while True:
                        while b"\r\n" not in self.buf:
                            self._fill()
                        line, self.buf = self.buf.split(b"\r\n", 1)
                        if line == b"":
                            break
                    return
                while len(self.buf) < n + 2:
                    self._fill()
                yield self.buf[:n]
                self.buf = self.buf[n + 2:]
        else:
            raw_len = hdrs.get("content-length")
            if self.buf:
                yield self.buf
                got, self.buf = len(self.buf), b""
            else:
                got = 0
            if raw_len is None:
                # Connection: close delimits the body -- read until EOF.
                while True:
                    try:
                        c = self._fill()
                    except (ConnectionError, OSError):
                        return
                    yield self.buf
                    self.buf = b""
                    if not c:
                        return
            clen = int(raw_len)
            while got < clen:
                c = self._fill()
                got += len(c)
                yield self.buf
                self.buf = b""

# --------------------------------------------------------------- SSE tokens
def extract_delta(obj):
    """Pull user-visible text out of one SSE chunk. Returns (text, is_reasoning)."""
    try:
        d = obj["choices"][0].get("delta") or {}
    except (KeyError, IndexError, TypeError):
        return "", False
    for k in ("content", "text"):
        v = d.get(k)
        if isinstance(v, str) and v:
            return v, False
    for k in ("reasoning_content", "reasoning"):
        v = d.get(k)
        if isinstance(v, str) and v:
            return v, True
    return "", False

# ------------------------------------------------------------ one benchmark
def run_once(target, model, scenario, conn=None, timeout=120.0,
             content_only=False, proxy=None):
    host = target["host"]
    path = target["chat_path"]
    key = os.environ.get(target["api_key_env"], "")
    sc = SCENARIOS[scenario]

    payload = {"model": model, "messages": sc["messages"],
               "max_tokens": sc["max_tokens"], "stream": sc["stream"],
               "temperature": 0}
    body = json.dumps(payload).encode()

    reused = conn is not None and conn.sock is not None
    if not reused:
        conn = Conn(host, timeout=timeout, proxy=proxy).connect()
    r = dict(conn.t) if not reused else {"dns_ms": 0.0, "tcp_ms": 0.0, "tls_ms": 0.0, "connect_ms": 0.0}
    r.update({"target": target["label"], "model": model, "scenario": scenario,
              "reused_conn": reused, "ip": getattr(conn, "ip", None)})

    hdr = [f"POST {path} HTTP/1.1", f"Host: {host}",
           f"Authorization: Bearer {key}", "Content-Type: application/json",
           f"Content-Length: {len(body)}", "Accept: text/event-stream" if sc["stream"] else "Accept: application/json",
           "User-Agent: unifyai-perf/1.0", "Connection: keep-alive",
           # OpenRouter attribution headers; harmless elsewhere.
           "HTTP-Referer: https://unifyapi.ai", "X-Title: unifyai-perf"]
    req = ("\r\n".join(hdr) + "\r\n\r\n").encode() + body

    t_start = time.perf_counter()
    try:
        conn.sock.sendall(req)
        t_sent = time.perf_counter()
        status, hdrs, t_fb = conn.read_headers()
        r["status"] = status
        r["upstream_provider"] = hdrs.get("x-openrouter-provider") or hdrs.get("x-unifyai-channel") or ""
        r["ttfb_ms"] = ms(t_sent, t_fb)

        t_first_tok = None
        t_last_tok = None
        saw_reasoning = saw_content = False
        ntok = 0
        tok_times = []
        raw = b""
        pending = b""
        for chunk in conn.iter_body(hdrs):
            raw += chunk
            if not sc["stream"]:
                continue
            pending += chunk
            while b"\n" in pending:
                line, pending = pending.split(b"\n", 1)
                line = line.strip()
                if not line.startswith(b"data:"):
                    continue
                data = line[5:].strip()
                if data == b"[DONE]":
                    continue
                try:
                    obj = json.loads(data)
                except Exception:
                    continue
                text, is_reason = extract_delta(obj)
                if not text:
                    continue
                if is_reason:
                    saw_reasoning = True
                    if content_only:
                        continue
                else:
                    saw_content = True
                now = time.perf_counter()
                if t_first_tok is None:
                    t_first_tok = now
                t_last_tok = now
                ntok += 1
                tok_times.append(now)
        t_done = time.perf_counter()

        if not sc["stream"]:
            t_first_tok = t_fb
            t_last_tok = t_done
            try:
                j = json.loads(raw.decode("utf-8", "replace"))
                ntok = (j.get("usage") or {}).get("completion_tokens", 0)
                r["upstream_provider"] = r["upstream_provider"] or j.get("provider", "")
            except Exception:
                pass

        r["roundtrip_ms"] = ms(t_start, t_done)
        r["total_ms"] = r["connect_ms"] + r["roundtrip_ms"]
        r["ttft_ms"] = ms(t_start, t_first_tok) if t_first_tok else None
        r["ttft_total_ms"] = (r["connect_ms"] + r["ttft_ms"]) if r["ttft_ms"] is not None else None
        r["output_tokens"] = ntok
        r["saw_reasoning"] = saw_reasoning
        r["saw_content"] = saw_content
        r["reasoning_only"] = bool(saw_reasoning and not saw_content)
        if t_first_tok and t_last_tok and ntok >= 5:
            gen_s = t_last_tok - t_first_tok
            # deltas are a proxy for tokens; below ~50ms the sample is a burst,
            # not a generation rate, so the ratio is not reported.
            if gen_s >= 0.05:
                r["tps"] = round(ntok / gen_s, 1)
                r["gen_ms"] = round(gen_s * 1000, 1)
            gaps = [ms(tok_times[i - 1], tok_times[i]) for i in range(1, len(tok_times))]
            if gaps:
                r["itl_p50_ms"] = round(statistics.median(gaps), 1)
                r["itl_p95_ms"] = round(sorted(gaps)[int(len(gaps) * 0.95)], 1)
        if status >= 400:
            r["error_body"] = raw[:400].decode("utf-8", "replace")
    except Exception as e:
        r["error"] = f"{type(e).__name__}: {e}"
        conn.close()
    return r, conn

# ------------------------------------------------------------------ reports
def pct(v, p):
    if not v:
        return None
    v = sorted(v)
    if len(v) == 1:
        return v[0]
    k = (len(v) - 1) * p
    f = int(k)
    return v[f] + (v[min(f + 1, len(v) - 1)] - v[f]) * (k - f)

def summarize(rows, field):
    vals = [r[field] for r in rows if r.get(field) is not None and "error" not in r]
    if not vals:
        return None
    return {"n": len(vals), "min": round(min(vals), 1), "p50": round(pct(vals, .5), 1),
            "p95": round(pct(vals, .95), 1), "max": round(max(vals), 1)}

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--case", action="append", help="model key from targets.json model_map (repeatable)")
    ap.add_argument("--all", action="store_true", help="every case in model_map")
    ap.add_argument("--scenario", default="ttft", choices=list(SCENARIOS))
    ap.add_argument("-n", "--repeat", type=int, default=5)
    ap.add_argument("--warm", action="store_true", help="reuse one connection across repeats")
    ap.add_argument("--targets", default=os.path.join(HERE, "targets.json"))
    ap.add_argument("--json", default=None)
    ap.add_argument("--proxy", default=None, metavar="HOST:PORT",
                    help="tunnel via an HTTP CONNECT proxy (e.g. 127.0.0.1:7897). "
                         "Use the SAME setting for both targets or the comparison is invalid.")
    ap.add_argument("--content-only", action="store_true",
                    help="TTFT counts only the first *content* token, ignoring reasoning "
                         "deltas (stricter; reasoning models may then report no TTFT)")
    args = ap.parse_args()

    proxy = None
    if args.proxy:
        ph, _, pp = args.proxy.rpartition(":")
        proxy = (ph, int(pp))

    cfg = json.load(open(args.targets))
    tg, mm = cfg["targets"], cfg["model_map"]
    cases = list(mm) if args.all else (args.case or [])
    if not cases:
        print("nothing to run: pass --case <key> or --all", file=sys.stderr)
        print(f"available: {', '.join(mm)}", file=sys.stderr)
        return 2

    missing = [t["api_key_env"] for t in tg.values() if not os.environ.get(t["api_key_env"])]
    if missing:
        print(f"!! missing env vars: {', '.join(missing)} -- those targets will 401", file=sys.stderr)

    vantage = os.environ.get("PERF_VANTAGE", "unset")
    stamp = datetime.now(timezone.utc).isoformat(timespec="seconds")
    mode = "warm (keepalive reuse)" if args.warm else "cold (new connection each)"
    print(f"# scenario={args.scenario} ({SCENARIOS[args.scenario]['desc']})")
    print(f"# mode={mode}  repeat={args.repeat}  vantage={vantage}  "
          f"exit={'proxy '+args.proxy if args.proxy else 'direct'}  {stamp}\n")

    out = {"vantage": vantage, "utc": stamp, "scenario": args.scenario,
           "mode": mode, "repeat": args.repeat,
           "exit": ("proxy " + args.proxy) if args.proxy else "direct", "runs": []}

    hdr = f"{'case':<16} {'target':<12} {'ttft p50':>9} {'ttft p95':>9} {'rt p50':>9} {'conn p50':>9} {'tps':>7} {'ok':>6}"
    print(hdr); print("-" * len(hdr))

    for case in cases:
        for tkey, target in tg.items():
            model = mm[case].get(tkey)
            if not model:
                continue
            rows, conn, reconnects = [], None, 0
            for _ in range(args.repeat):
                row, conn = run_once(target, model, args.scenario,
                                     conn=conn if args.warm else None,
                                     content_only=args.content_only, proxy=proxy)
                if args.warm and "error" in row and row.get("reused_conn"):
                    # peer dropped the keepalive; re-establish and take the sample again
                    reconnects += 1
                    conn = None
                    row, conn = run_once(target, model, args.scenario, conn=None,
                                         content_only=args.content_only, proxy=proxy)
                row["case"] = case
                rows.append(row)
                if not args.warm:
                    conn.close(); conn = None
            if conn:
                conn.close()
            out["runs"].extend(rows)

            ok = [r for r in rows if "error" not in r and r.get("status") == 200]
            s_ttft = summarize(ok, "ttft_total_ms" if not args.warm else "ttft_ms")
            s_rt = summarize(ok, "total_ms" if not args.warm else "roundtrip_ms")
            s_cn = summarize(rows, "connect_ms")
            tps = [r["tps"] for r in ok if r.get("tps")]
            def f(x, k):
                return f"{x[k]:>9.0f}" if x else f"{'-':>9}"
            print(f"{case:<16} {target['label']:<12} {f(s_ttft,'p50')} {f(s_ttft,'p95')} "
                  f"{f(s_rt,'p50')} {f(s_cn,'p50')} "
                  f"{(round(statistics.fmean(tps),1) if tps else '-'):>7} "
                  f"{len(ok)}/{len(rows):>3}")
            if reconnects:
                print(f"{'':<16} {'':<12}   ~~ keepalive dropped {reconnects}x "
                      f"(those samples re-taken on a fresh connection)")
            ronly = sum(1 for r in ok if r.get("reasoning_only"))
            if ronly:
                print(f"{'':<16} {'':<12}   ~~ {ronly}/{len(ok)} produced reasoning only "
                      f"(no content within max_tokens)")
            bad = [r for r in rows if "error" in r or r.get("status") != 200]
            for b in bad[:2]:
                why = b.get("error") or f"HTTP {b.get('status')}: {b.get('error_body','')[:160]}"
                print(f"{'':<16} {'':<12}   !! {why}")
        print()

    if args.json:
        os.makedirs(os.path.dirname(args.json) or ".", exist_ok=True)
        json.dump(out, open(args.json, "w"), indent=2)
        print(f"raw -> {args.json}")
    return 0

if __name__ == "__main__":
    sys.exit(main())
