#!/usr/bin/env python3
import argparse
import signal
import socket
import time

running = True


def stop(_signum, _frame):
    global running
    running = False


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port-file", required=True)
    parser.add_argument("--chunks", type=int, default=40)
    parser.add_argument("--chunk-size", type=int, default=256 * 1024)
    parser.add_argument("--interval-ms", type=float, default=25.0)
    args = parser.parse_args()

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    payload = bytes((index % 251 for index in range(args.chunk_size)))
    total = args.chunks * args.chunk_size

    server = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    server.bind(("127.0.0.1", 0))
    server.listen(8)
    server.settimeout(0.5)
    with open(args.port_file, "w", encoding="ascii") as handle:
        handle.write(str(server.getsockname()[1]))

    while running:
        try:
            conn, _ = server.accept()
        except socket.timeout:
            continue
        with conn:
            conn.settimeout(5)
            request = bytearray()
            while b"\r\n\r\n" not in request and len(request) < 8192:
                part = conn.recv(4096)
                if not part:
                    break
                request.extend(part)
            if not request.startswith(b"GET "):
                continue
            headers = (
                b"HTTP/1.1 200 OK\r\n"
                b"Content-Type: video/mp2t\r\n"
                + f"Content-Length: {total}\r\nConnection: close\r\n\r\n".encode("ascii")
            )
            conn.sendall(headers)
            for _ in range(args.chunks):
                if not running:
                    break
                conn.sendall(payload)
                time.sleep(args.interval_ms / 1000.0)
        break
    server.close()


if __name__ == "__main__":
    main()
