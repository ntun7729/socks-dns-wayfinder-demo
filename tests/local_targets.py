#!/usr/bin/env python3
import signal
import socket
import sys
import threading

running = True


def stop(_signum, _frame):
    global running
    running = False


def dns_response(query: bytes) -> bytes:
    if len(query) < 12:
        return b""
    end = 12
    while end < len(query) and query[end] != 0:
        end += 1 + query[end]
    if end + 5 > len(query):
        return b""
    question_end = end + 5
    question = query[12:question_end]
    header = query[:2] + b"\x81\x80" + b"\x00\x01\x00\x01\x00\x00\x00\x00"
    answer = b"\xc0\x0c\x00\x01\x00\x01\x00\x00\x00\x3c\x00\x04\xcb\x00\x71\x07"
    return header + question + answer


def tcp_loop(sock: socket.socket):
    while running:
        try:
            sock.settimeout(0.5)
            conn, _ = sock.accept()
        except socket.timeout:
            continue
        except OSError:
            return
        with conn:
            try:
                conn.settimeout(2)
                conn.recv(4096)
                conn.sendall(b"HTTP/1.1 200 OK\r\nContent-Length: 13\r\nConnection: close\r\n\r\ns5dns-tcp-ok\n")
            except OSError:
                pass


def dns_loop(sock: socket.socket):
    while running:
        try:
            sock.settimeout(0.5)
            query, peer = sock.recvfrom(65535)
        except socket.timeout:
            continue
        except OSError:
            return
        response = dns_response(query)
        if response:
            try:
                sock.sendto(response, peer)
            except OSError:
                pass


def main():
    if len(sys.argv) != 3:
        raise SystemExit("usage: local_targets.py TCP_PORT_FILE DNS_PORT_FILE")
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    tcp = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    tcp.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    tcp.bind(("127.0.0.1", 0))
    tcp.listen(16)
    dns = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    dns.bind(("127.0.0.1", 0))
    with open(sys.argv[1], "w", encoding="ascii") as handle:
        handle.write(str(tcp.getsockname()[1]))
    with open(sys.argv[2], "w", encoding="ascii") as handle:
        handle.write(str(dns.getsockname()[1]))
    threading.Thread(target=tcp_loop, args=(tcp,), daemon=True).start()
    threading.Thread(target=dns_loop, args=(dns,), daemon=True).start()
    while running:
        signal.pause()
    tcp.close()
    dns.close()


if __name__ == "__main__":
    main()
