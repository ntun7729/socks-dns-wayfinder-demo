#!/usr/bin/env python3
import socket
import struct
import sys

if len(sys.argv) != 3:
    raise SystemExit("usage: dns_query.py HOST PORT")

host = sys.argv[1]
port = int(sys.argv[2])
query = b"\x12\x34\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00" + b"\x07example\x03com\x00" + b"\x00\x01\x00\x01"
with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
    sock.settimeout(5)
    sock.sendto(query, (host, port))
    response, _ = sock.recvfrom(4096)

if response[:2] != b"\x12\x34" or len(response) < 12:
    raise SystemExit("invalid DNS response")
answers = struct.unpack("!H", response[6:8])[0]
if answers != 1 or response[-4:] != b"\xcb\x00\x71\x07":
    raise SystemExit(f"unexpected DNS response: answers={answers} tail={response[-4:].hex()}")
print("dns-forward-ok")
