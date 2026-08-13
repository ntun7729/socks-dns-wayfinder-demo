#!/usr/bin/env python3
import socket
import struct
import sys

if len(sys.argv) != 3:
    raise SystemExit("usage: public_dns_query.py HOST PORT")

host = sys.argv[1]
port = int(sys.argv[2])
query = b"\x56\x78\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00" + b"\x07example\x03com\x00" + b"\x00\x01\x00\x01"
with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
    sock.settimeout(8)
    sock.sendto(query, (host, port))
    response, _ = sock.recvfrom(4096)

if len(response) < 12 or response[:2] != b"\x56\x78" or not (response[2] & 0x80):
    raise SystemExit("invalid DNS response")
answers = struct.unpack("!H", response[6:8])[0]
print(f"dns-public-ok answers={answers} bytes={len(response)}")
