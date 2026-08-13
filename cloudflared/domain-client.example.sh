#!/usr/bin/env bash
set -euo pipefail

# This client needs only the s5dns binary and the shared application token.
# It does not need cloudflared, TUN/TAP, or a local TCP forwarder.
exec ./s5dns client \
  -mux \
  -websocket-url wss://s5-edge-421b01.nyan.college/s5dns \
  -token-file /path/to/client.token \
  -socks-listen 127.0.0.1:1080 \
  -dns-listen 127.0.0.1:5353
