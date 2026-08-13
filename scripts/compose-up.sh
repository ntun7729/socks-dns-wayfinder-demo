#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${S5DNS_PASSWORD:-}" ]]; then
  echo "set S5DNS_PASSWORD to the shared s5dns password/UUID" >&2
  exit 2
fi

compose_args=()
if [[ -n "${CLOUDFLARED_TUNNEL_TOKEN:-}" ]]; then
  echo "CLOUDFLARED_TUNNEL_TOKEN is set; starting s5dns with Cloudflare Tunnel"
  compose_args=(--profile cloudflare)
else
  echo "CLOUDFLARED_TUNNEL_TOKEN is not set; starting s5dns without Cloudflare Tunnel"
fi

exec docker compose "${compose_args[@]}" up -d
