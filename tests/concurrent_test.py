#!/usr/bin/env python3
import argparse
import json
import socket
import threading
import time

CHUNK = b"c" * (256 * 1024)


def recv_exact(sock: socket.socket, size: int) -> None:
    remaining = size
    while remaining:
        data = sock.recv(min(len(CHUNK), remaining))
        if not data:
            raise RuntimeError(f"peer closed with {remaining} bytes remaining")
        remaining -= len(data)


def socks_connect(proxy_port: int, target_port: int) -> socket.socket:
    sock = socket.create_connection(("127.0.0.1", proxy_port), timeout=10)
    sock.sendall(b"\x05\x01\x00")
    if sock.recv(2) != b"\x05\x00":
        raise RuntimeError("SOCKS negotiation failed")
    sock.sendall(b"\x05\x01\x00\x01\x7f\x00\x00\x01" + target_port.to_bytes(2, "big"))
    reply = sock.recv(10)
    if reply[:2] != b"\x05\x00":
        raise RuntimeError(f"SOCKS CONNECT failed: {reply.hex()}")
    return sock


def sink(args):
    server = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    server.bind(("127.0.0.1", 0))
    server.listen(args.streams)
    with open(args.port_file, "w", encoding="ascii") as handle:
        handle.write(str(server.getsockname()[1]))

    def consume(conn):
        with conn:
            recv_exact(conn, args.bytes)
            conn.sendall(b"\x01")

    threads = []
    for _ in range(args.streams):
        conn, _ = server.accept()
        thread = threading.Thread(target=consume, args=(conn,))
        thread.start()
        threads.append(thread)
    for thread in threads:
        thread.join()
    server.close()


def measure(args):
    errors = []
    timings = []
    lock = threading.Lock()

    def one_stream():
        started = time.perf_counter()
        try:
            with socks_connect(args.proxy_port, args.target_port) as sock:
                remaining = args.bytes
                while remaining:
                    part = CHUNK if remaining >= len(CHUNK) else CHUNK[:remaining]
                    sock.sendall(part)
                    remaining -= len(part)
                if sock.recv(1) != b"\x01":
                    raise RuntimeError("sink acknowledgement missing")
            elapsed = time.perf_counter() - started
            with lock:
                timings.append(elapsed)
        except Exception as exc:
            with lock:
                errors.append(str(exc))

    threads = [threading.Thread(target=one_stream) for _ in range(args.streams)]
    started = time.perf_counter()
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()
    elapsed = time.perf_counter() - started
    if errors:
        raise RuntimeError("; ".join(errors))
    total_bytes = args.streams * args.bytes
    result = {
        "mode": "socks5-tls-multiplexed",
        "streams": args.streams,
        "bytes_per_stream": args.bytes,
        "total_bytes": total_bytes,
        "wall_seconds": elapsed,
        "aggregate_mbps": total_bytes * 8 / elapsed / 1_000_000,
        "stream_seconds_min": min(timings),
        "stream_seconds_max": max(timings),
    }
    print(json.dumps(result, indent=2, sort_keys=True))
    print("concurrent-multiplexing-ok")


def main():
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest="command", required=True)
    sink_parser = sub.add_parser("sink")
    sink_parser.add_argument("--port-file", required=True)
    sink_parser.add_argument("--streams", type=int, required=True)
    sink_parser.add_argument("--bytes", type=int, required=True)
    measure_parser = sub.add_parser("measure")
    measure_parser.add_argument("--proxy-port", type=int, required=True)
    measure_parser.add_argument("--target-port", type=int, required=True)
    measure_parser.add_argument("--streams", type=int, default=8)
    measure_parser.add_argument("--bytes", type=int, default=8 * 1024 * 1024)
    args = parser.parse_args()
    if args.command == "sink":
        sink(args)
    else:
        measure(args)


if __name__ == "__main__":
    main()
