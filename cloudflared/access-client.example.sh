#!/usr/bin/env bash
set -euo pipefail

# Run on the client device. Cloudflare Access will open a browser for SSO
# unless automatic service-token authentication is configured in Cloudflare.
hostname="${CLOUDFLARE_HOSTNAME:?set CLOUDFLARE_HOSTNAME, e.g. vpn.example.com}"
forward_port="${CLOUDFLARED_FORWARD_PORT:-18443}"

exec cloudflared access tcp \
  --hostname "$hostname" \
  --url "127.0.0.1:${forward_port}"

# In a second terminal on the client device, point s5dns at the forwarded port:
# sudo ./s5dns client -mux -server 127.0.0.1:18443 \
#   -server-name localhost -ca /path/to/ca.crt \
#   -token-file /path/to/client.token -socks-listen 127.0.0.1:1080 \
#   -dns-listen 127.0.0.1:5353
