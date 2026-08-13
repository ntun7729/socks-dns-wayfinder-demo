#!/usr/bin/env python3
import argparse
import json
import socket
import statistics
import sys
import time

CHUNK = b"s5dns-throughput-test-" + b"x" * (1024 * 1024 - 22)


def recv_exact(sock: socket.socket, size: int) -> None:
    remaining = size
    while remaining:
        data = sock.recv(min(1024 * 1024, remaining))
        if not data:
            raise RuntimeError(f"peer closed with {remaining} bytes remaining")
        remaining -= len(data)


def connect_target(sock: socket.socket, target_host: str, target_port: int) -> None:
    ip = socket.inet_aton(target_host)
    sock.sendall(b"\x05\x01\x00\x01" + ip + target_port.to_bytes(2, "big"))
    reply = recv_exact_bytes(sock, 10)
    if reply[:2] != b"\x05\x00":
        raise RuntimeError(f"SOCKS CONNECT failed: {reply.hex()}")


def recv_exact_bytes(sock: socket.socket, size: int) -> bytes:
    chunks = []
    remaining = size
    while remaining:
        data = sock.recv(remaining)
        if not data:
            raise RuntimeError("peer closed during SOCKS handshake")
        chunks.append(data)
        remaining -= len(data)
    return b"".join(chunks)


def socks_connect(proxy_host: str, proxy_port: int, target_host: str, target_port: int) -> socket.socket:
    sock = socket.create_connection((proxy_host, proxy_port), timeout=10)
    sock.sendall(b"\x05\x01\x00")
    if recv_exact_bytes(sock, 2) != b"\x05\x00":
        raise RuntimeError("SOCKS no-auth negotiation failed")
    connect_target(sock, target_host, target_port)
    return sock


def direct_connect(target_host: str, target_port: int) -> socket.socket:
    return socket.create_connection((target_host, target_port), timeout=10)


def measure(args: argparse.Namespace) -> dict:
    samples = []
    payload_bytes = args.bytes
    for run in range(args.runs):
        started = time.perf_counter()
        if args.proxy_port:
            sock = socks_connect(args.proxy_host, args.proxy_port, args.target_host, args.target_port)
        else:
            sock = direct_connect(args.target_host, args.target_port)
        with sock:
            remaining = payload_bytes
            while remaining:
                chunk = CHUNK if remaining >= len(CHUNK) else CHUNK[:remaining]
                sock.sendall(chunk)
                remaining -= len(chunk)
            if sock.recv(1) != b"\x01":
                raise RuntimeError("sink acknowledgement missing")
        elapsed = time.perf_counter() - started
        mbps = (payload_bytes * 8 / elapsed) / 1_000_000
        samples.append({"run": run + 1, "seconds": elapsed, "mbps": mbps})
    values = [sample["mbps"] for sample in samples]
    return {
        "mode": "socks5-tls" if args.proxy_port else "direct-loopback",
        "bytes": payload_bytes,
        "runs": args.runs,
        "samples": samples,
        "median_mbps": statistics.median(values),
        "min_mbps": min(values),
        "max_mbps": max(values),
    }


def sink(args: argparse.Namespace) -> None:
    server = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    server.bind(("127.0.0.1", 0))
    server.listen(16)
    with open(args.port_file, "w", encoding="ascii") as handle:
        handle.write(str(server.getsockname()[1]))
    for _ in range(args.connections):
        conn, _ = server.accept()
        with conn:
            recv_exact(conn, args.bytes)
            conn.sendall(b"\x01")
    server.close()


def main() -> None:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)

    sink_parser = subparsers.add_parser("sink")
    sink_parser.add_argument("--bytes", type=int, required=True)
    sink_parser.add_argument("--connections", type=int, required=True)
    sink_parser.add_argument("--port-file", required=True)

    measure_parser = subparsers.add_parser("measure")
    measure_parser.add_argument("--target-host", default="127.0.0.1")
    measure_parser.add_argument("--target-port", type=int, required=True)
    measure_parser.add_argument("--proxy-host")
    measure_parser.add_argument("--proxy-port", type=int)
    measure_parser.add_argument("--bytes", type=int, required=True)
    measure_parser.add_argument("--runs", type=int, default=3)

    args = parser.parse_args()
    if args.command == "sink":
        sink(args)
        return
    if bool(args.proxy_host) != bool(args.proxy_port):
        parser.error("proxy-host and proxy-port must be supplied together")
    result = measure(args)
    print(json.dumps(result, sort_keys=True))


if __name__ == "__main__":
    try:
        main()
    except (OSError, RuntimeError) as exc:
        print(f"speed test failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
