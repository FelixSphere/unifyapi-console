#!/usr/bin/env python3
"""
Tests for bench.py's wire-format handling. Stdlib unittest, no deps, no network.

    python3 test_bench.py

These exist because the body reader is the one place where a bug is *invisible*:
a truncated read still produces a 200 with plausible-looking timings, so the
harness reports a clean success for precisely the stalled upstream it was built
to detect. Every path through iter_body is pinned here for that reason.
"""
import importlib.util
import os
import socket
import unittest

_spec = importlib.util.spec_from_file_location(
    "bench", os.path.join(os.path.dirname(os.path.abspath(__file__)), "bench.py"))
bench = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(bench)


class FakeSocket:
    """Yields queued chunks; an Exception in the queue is raised at that point.
    Raises ConnectionError once drained, matching a peer close."""

    def __init__(self, chunks):
        self.chunks, self.i = list(chunks), 0

    def recv(self, _n):
        if self.i >= len(self.chunks):
            raise ConnectionError("closed by peer")
        chunk = self.chunks[self.i]
        self.i += 1
        if isinstance(chunk, Exception):
            raise chunk
        return chunk


def conn_with(chunks):
    c = bench.Conn.__new__(bench.Conn)
    c.sock, c.buf = FakeSocket(chunks), b""
    return c


class IterBody(unittest.TestCase):
    def body(self, headers, chunks):
        return b"".join(conn_with(chunks).iter_body(headers))

    def test_content_length_single_read(self):
        self.assertEqual(self.body({"content-length": "5"}, [b"HELLO"]), b"HELLO")

    def test_content_length_split_across_reads(self):
        self.assertEqual(
            self.body({"content-length": "10"}, [b"HEL", b"LOWOR", b"LD"]), b"HELLOWORLD")

    def test_chunked_with_terminator(self):
        self.assertEqual(
            self.body({"transfer-encoding": "chunked"}, [b"5\r\nHELLO\r\n0\r\n\r\n"]), b"HELLO")

    def test_chunked_multiple_chunks(self):
        self.assertEqual(
            self.body({"transfer-encoding": "chunked"},
                      [b"3\r\nABC\r\n", b"3\r\nDEF\r\n", b"0\r\n\r\n"]), b"ABCDEF")

    def test_chunked_rejects_bad_chunk_size(self):
        with self.assertRaises(ConnectionError):
            self.body({"transfer-encoding": "chunked"}, [b"ZZ\r\nABC\r\n"])

    def test_close_delimited_reads_to_eof(self):
        # No content-length and no chunked: the peer close is the only delimiter.
        self.assertEqual(self.body({}, [b"ABC", b"DEF"]), b"ABCDEF")

    def test_close_delimited_stall_propagates(self):
        """A stalled read must NOT be mistaken for end-of-body.

        socket.timeout IS TimeoutError, which subclasses OSError -- so a catch
        written as `except (ConnectionError, OSError)` swallows the stall and
        returns a truncated body that run_once scores as a clean 200 with an
        understated roundtrip_ms. That is the exact failure this harness exists
        to measure, so it has to reach the caller.
        """
        with self.assertRaises(TimeoutError):
            self.body({}, [b"PARTIAL-", socket.timeout("timed out")])


class ReadHeaders(unittest.TestCase):
    def test_parses_status_and_headers(self):
        c = conn_with([b"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n"])
        status, hdrs, _ = c.read_headers()
        self.assertEqual(status, 200)
        self.assertEqual(hdrs["content-type"], "text/event-stream")

    def test_malformed_status_line_is_reported(self):
        # A reused keepalive connection left mis-framed shows up here first;
        # it must name that cause rather than surfacing a bare IndexError.
        c = conn_with([b"\r\ngarbage\r\n\r\n"])
        with self.assertRaises(ConnectionError):
            c.read_headers()


class ExtractDelta(unittest.TestCase):
    def test_content_delta(self):
        self.assertEqual(
            bench.extract_delta({"choices": [{"delta": {"content": "hi"}}]}), ("hi", False))

    def test_reasoning_delta_is_flagged(self):
        # Flagged, not discarded: TTFT counts the first token of any kind, and
        # a short max_tokens can end a reasoning model before any content.
        self.assertEqual(
            bench.extract_delta({"choices": [{"delta": {"reasoning_content": "t"}}]}), ("t", True))
        self.assertEqual(
            bench.extract_delta({"choices": [{"delta": {"reasoning": "t"}}]}), ("t", True))

    def test_empty_and_malformed_yield_nothing(self):
        for obj in ({"choices": [{"delta": {"content": ""}}]},
                    {"choices": [{"delta": {"role": "assistant"}}]},
                    {"choices": []}, {}, {"choices": [{}]}):
            self.assertEqual(bench.extract_delta(obj), ("", False), obj)


if __name__ == "__main__":
    unittest.main(verbosity=2)
