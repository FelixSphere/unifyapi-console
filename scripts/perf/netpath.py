#!/usr/bin/env python3
"""
netpath.py -- measure the *network path* cost to each endpoint, no API key required.

This isolates the part of latency that a CDN / edge terminator changes:
  DNS -> TCP connect -> TLS handshake -> first byte of an HTTP response.

Everything measured here is paid on EVERY cold connection, before a single
token of the user's prompt reaches a model. It is the floor under both TTFT
and round-trip time, and it is the floor OpenRouter lowers by terminating TLS
at an anycast edge instead of in one region.

Usage:
  python3 netpath.py                      # default targets, 20 samples each
  python3 netpath.py -n 50
  python3 netpath.py --json results/netpath.json
"""
import argparse, json, os, socket, ssl, statistics, sys, time
from datetime import datetime, timezone

DEFAULT_TARGETS = [
    ("UnifyAI",    "app.unifyapi.ai", "/api/tenant/"),
    ("OpenRouter", "openrouter.ai",   "/api/v1/models"),
]

def ms(a, b):
    return (b - a) * 1000.0

def _connect_tunnel(sock, host, port):
    """Establish an HTTP CONNECT tunnel through an already-connected proxy."""
    sock.sendall(f"CONNECT {host}:{port} HTTP/1.1\r\n"
                 f"Host: {host}:{port}\r\n\r\n".encode())
    resp = b""
    while b"\r\n\r\n" not in resp:
        c = sock.recv(4096)
        if not c:
            raise ConnectionError("proxy closed during CONNECT")
        resp += c
    status = resp.split(b"\r\n", 1)[0]
    if b" 200 " not in status:
        raise ConnectionError(f"proxy CONNECT refused: {status[:120]!r}")


def probe(host, path, port=443, timeout=15.0, alpn=("http/1.1",), proxy=None):
    """One cold connection. Returns per-phase milliseconds.

    With proxy set, DNS/TCP are to the proxy and the TLS phase is measured
    through the tunnel -- which is the only way these numbers can be read
    alongside bench.py's when it is given the same --proxy."""
    r = {"host": host, "via": f"proxy {proxy[0]}:{proxy[1]}" if proxy else "direct"}
    t0 = time.perf_counter()
    dial_host, dial_port = proxy if proxy else (host, port)
    try:
        infos = socket.getaddrinfo(dial_host, dial_port, socket.AF_INET, socket.SOCK_STREAM)
    except Exception as e:
        return {**r, "error": f"dns: {e}"}
    t_dns = time.perf_counter()
    family, stype, proto, _, sockaddr = infos[0]
    r["ip"] = sockaddr[0]
    r["dns_ms"] = ms(t0, t_dns)

    s = socket.socket(family, stype, proto)
    s.settimeout(timeout)
    s.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
    try:
        s.connect(sockaddr)
        if proxy:
            _connect_tunnel(s, host, port)
    except Exception as e:
        s.close()
        return {**r, "error": f"tcp: {e}"}
    t_tcp = time.perf_counter()
    r["tcp_ms"] = ms(t_dns, t_tcp)

    ctx = ssl.create_default_context()
    ctx.set_alpn_protocols(list(alpn))  # http/1.1 only: we speak HTTP/1.1 below
    try:
        ss = ctx.wrap_socket(s, server_hostname=host)
    except Exception as e:
        s.close()
        return {**r, "error": f"tls: {e}"}
    t_tls = time.perf_counter()
    r["tls_ms"] = ms(t_tcp, t_tls)
    r["tls_version"] = ss.version()
    r["alpn"] = ss.selected_alpn_protocol()

    # Speak HTTP/1.1 regardless of ALPN so the first-byte number is comparable.
    req = (f"GET {path} HTTP/1.1\r\nHost: {host}\r\n"
           "User-Agent: unifyai-perf/1.0\r\nAccept: */*\r\nConnection: close\r\n\r\n")
    try:
        ss.sendall(req.encode())
        t_sent = time.perf_counter()
        first = ss.recv(1)
        t_fb = time.perf_counter()
        rest = b""
        while True:
            chunk = ss.recv(65536)
            if not chunk:
                break
            rest += chunk
            if len(rest) > 262144:
                break
        t_done = time.perf_counter()
    except Exception as e:
        ss.close()
        return {**r, "error": f"http: {e}"}
    finally:
        try:
            ss.close()
        except Exception:
            pass

    head = (first + rest).split(b"\r\n\r\n", 1)[0].decode("latin-1", "replace")
    r["status"] = head.split("\r\n", 1)[0]
    r["server"] = next((l.split(":", 1)[1].strip() for l in head.split("\r\n")
                        if l.lower().startswith("server:")), "")
    r["ttfb_ms"] = ms(t_sent, t_fb)
    r["connect_total_ms"] = ms(t0, t_tls)      # DNS+TCP+TLS: the cold-start tax
    r["total_ms"] = ms(t0, t_done)
    return r

def warm_rtt(host, path, port=443, n=10, timeout=15.0, proxy=None):
    """Application RTT on an ALREADY-established connection (keepalive reuse).

    This is what a client pays per request once the connection is warm, and it
    isolates one network round trip with no handshake in it."""
    ctx = ssl.create_default_context()
    ctx.set_alpn_protocols(["http/1.1"])
    try:
        raw = socket.create_connection(proxy if proxy else (host, port), timeout=timeout)
        raw.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
        if proxy:
            _connect_tunnel(raw, host, port)
        ss = ctx.wrap_socket(raw, server_hostname=host)
    except Exception as e:
        return {"error": f"setup: {e}"}
    rtts = []
    try:
        for _ in range(n):
            req = (f"GET {path} HTTP/1.1\r\nHost: {host}\r\n"
                   "User-Agent: unifyai-perf/1.0\r\nAccept: */*\r\n\r\n")
            t0 = time.perf_counter()
            ss.sendall(req.encode())
            buf = b""
            # read headers, then the declared body, keeping the connection open
            while b"\r\n\r\n" not in buf:
                c = ss.recv(65536)
                if not c:
                    raise ConnectionError("closed during headers")
                buf += c
            t1 = time.perf_counter()
            rtts.append(ms(t0, t1))
            head, body = buf.split(b"\r\n\r\n", 1)
            hl = head.decode("latin-1", "replace").lower()
            clen = next((int(l.split(":", 1)[1]) for l in hl.split("\r\n")
                         if l.startswith("content-length:")), None)
            if clen is None:
                break  # chunked/close-delimited: not worth draining, one sample is enough
            while len(body) < clen:
                c = ss.recv(65536)
                if not c:
                    break
                body += c
    except Exception as e:
        if not rtts:
            return {"error": f"warm: {e}"}
    finally:
        try:
            ss.close()
        except Exception:
            pass
    return {"samples": rtts}

def summarize(vals):
    if not vals:
        return {}
    v = sorted(vals)
    def pct(p):
        if len(v) == 1:
            return v[0]
        k = (len(v) - 1) * p
        f = int(k)
        return v[f] + (v[min(f + 1, len(v) - 1)] - v[f]) * (k - f)
    return {"n": len(v), "min": round(v[0], 1), "p50": round(pct(0.50), 1),
            "p95": round(pct(0.95), 1), "max": round(v[-1], 1),
            "mean": round(statistics.fmean(v), 1)}

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("-n", "--samples", type=int, default=20)
    ap.add_argument("--json", default=None, help="write raw results to this path")
    ap.add_argument("--target", action="append", default=None,
                    help="label=host=path (repeatable); overrides defaults")
    ap.add_argument("--proxy", default=None, metavar="HOST:PORT",
                    help="tunnel via an HTTP CONNECT proxy (e.g. 127.0.0.1:7897). "
                         "Use the same value you pass to bench.py, or the two "
                         "tools measure different network paths.")
    args = ap.parse_args()

    proxy = None
    if args.proxy:
        ph, _, pp = args.proxy.rpartition(":")
        if not ph or not pp.isdigit():
            ap.error(f"--proxy must be HOST:PORT, got {args.proxy!r}")
        proxy = (ph, int(pp))

    targets = DEFAULT_TARGETS
    if args.target:
        targets = []
        for t in args.target:
            parts = t.split("=", 2)
            if len(parts) != 3:
                ap.error(f"--target must be label=host=path, got {t!r}")
            targets.append(tuple(parts))

    vantage = {
        "hostname": socket.gethostname(),
        "utc": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        "note": os.environ.get("PERF_VANTAGE", "unset -- set PERF_VANTAGE to name this location/region"),
    }
    print(f"# vantage: {vantage['note']}  ({vantage['utc']})")
    print(f"# cold-connection samples per target: {args.samples}")
    print(f"# exit: {'proxy ' + args.proxy if args.proxy else 'direct'}\n")

    out = {"vantage": vantage,
           "exit": ("proxy " + args.proxy) if args.proxy else "direct",
           "targets": {}}
    for label, host, path in targets:
        cold = [probe(host, path, proxy=proxy) for _ in range(args.samples)]
        ok = [c for c in cold if "error" not in c]
        errs = [c["error"] for c in cold if "error" in c]
        warm = warm_rtt(host, path, n=max(5, args.samples // 2), proxy=proxy)

        out["targets"][label] = {"host": host, "path": path, "cold": cold, "warm": warm}
        print(f"═══ {label}  ({host})")
        if not ok:
            print(f"    all {len(cold)} probes failed: {errs[:3]}")
            continue
        s = ok[0]
        print(f"    ip={s.get('ip')}  tls={s.get('tls_version')}  alpn={s.get('alpn')}  "
              f"server={s.get('server') or '-'}  status={s.get('status')}")
        for field, name in (("dns_ms", "DNS"), ("tcp_ms", "TCP connect"),
                            ("tls_ms", "TLS handshake"), ("connect_total_ms", "COLD CONNECT (D+T+T)"),
                            ("ttfb_ms", "server think+TTFB")):
            st = summarize([c[field] for c in ok if field in c])
            print(f"    {name:<22} p50 {st['p50']:>8.1f} ms   p95 {st['p95']:>8.1f}   min {st['min']:>8.1f}")
        if "samples" in warm:
            st = summarize(warm["samples"])
            print(f"    {'WARM req RTT (keepalive)':<22} p50 {st['p50']:>8.1f} ms   p95 {st['p95']:>8.1f}   min {st['min']:>8.1f}")
        else:
            print(f"    warm RTT unavailable: {warm.get('error')}")
        if errs:
            print(f"    ({len(errs)} failed probes: {errs[:2]})")
        print()

    if args.json:
        os.makedirs(os.path.dirname(args.json) or ".", exist_ok=True)
        with open(args.json, "w") as f:
            json.dump(out, f, indent=2)
        print(f"raw -> {args.json}")

if __name__ == "__main__":
    sys.exit(main())
