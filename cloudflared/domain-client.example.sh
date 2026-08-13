#!/usr/bin/env bash
set -euo pipefail

# This client needs only the s5dns binary and the shared application password/UUID.
# It does not need cloudflared, TUN/TAP, or a local TCP forwarder.
: "${S5DNS_PASSWORD:?set S5DNS_PASSWORD to the server's shared password/UUID}"
export S5DNS_PASSWORD

exec ./s5dns client \
  -mux \
  -websocket-url wss://s5-edge-421b01.nyan.college/s5dns \
  -password-env S5DNS_PASSWORD \
  -socks-listen 127.0.0.1:1080 \
  -dns-listen 127.0.0.1:5353
