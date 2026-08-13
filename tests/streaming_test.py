#!/usr/bin/env python3
import argparse
import json
import socket
import time


def recv_exact(sock: socket.socket, size: int) -> bytes:
    chunks = []
    remaining = size
    while remaining:
        chunk = sock.recv(remaining)
        if not chunk:
            raise RuntimeError("connection closed during SOCKS handshake")
        chunks.append(chunk)
        remaining -= len(chunk)
    return b"".join(chunks)


def socks_connect(proxy_host: str, proxy_port: int, target_host: str, target_port: int) -> socket.socket:
    sock = socket.create_connection((proxy_host, proxy_port), timeout=10)
    sock.sendall(b"\x05\x01\x00")
    if recv_exact(sock, 2) != b"\x05\x00":
        raise RuntimeError("SOCKS method negotiation failed")
    sock.sendall(b"\x05\x01\x00\x01" + socket.inet_aton(target_host) + target_port.to_bytes(2, "big"))
    reply = recv_exact(sock, 10)
    if reply[:2] != b"\x05\x00":
        raise RuntimeError(f"SOCKS CONNECT failed: {reply.hex()}")
    return sock


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--proxy-host", default="127.0.0.1")
    parser.add_argument("--proxy-port", type=int, required=True)
    parser.add_argument("--target-host", default="127.0.0.1")
    parser.add_argument("--target-port", type=int, required=True)
    parser.add_argument("--expected-bytes", type=int, required=True)
    args = parser.parse_args()

    started = time.perf_counter()
    with socks_connect(args.proxy_host, args.proxy_port, args.target_host, args.target_port) as sock:
        sock.sendall(b"GET /stream HTTP/1.1\r\nHost: stream.test\r\nConnection: close\r\n\r\n")
        response = bytearray()
        headers_at = None
        body = 0
        first_body_at = None
        chunks_observed = 0
        while body < args.expected_bytes:
            data = sock.recv(64 * 1024)
            if not data:
                raise RuntimeError(f"stream ended early at {body}/{args.expected_bytes} bytes")
            response.extend(data)
            if headers_at is None:
                marker = response.find(b"\r\n\r\n")
                if marker >= 0:
                    headers_at = time.perf_counter()
                    body = len(response) - marker - 4
                    if body:
                        first_body_at = headers_at
                        chunks_observed += 1
            else:
                body += len(data)
                chunks_observed += 1
            if body and first_body_at is None:
                first_body_at = time.perf_counter()
            if body >= args.expected_bytes:
                break
        finished = time.perf_counter()

    if headers_at is None or first_body_at is None:
        raise RuntimeError("stream headers or first body bytes were not observed")
    if body != args.expected_bytes:
        raise RuntimeError(f"unexpected body length {body}, expected {args.expected_bytes}")

    result = {
        "mode": "socks5-tls-stream",
        "bytes": body,
        "header_latency_ms": (headers_at - started) * 1000,
        "first_body_latency_ms": (first_body_at - started) * 1000,
        "duration_seconds": finished - started,
        "throughput_mbps": body * 8 / (finished - started) / 1_000_000,
        "chunks_observed": chunks_observed,
    }
    print(json.dumps(result, indent=2, sort_keys=True))
    print("streaming-ok")


if __name__ == "__main__":
    main()
