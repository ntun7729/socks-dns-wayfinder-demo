#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID}" -ne 0 ]]; then
  echo "run as root: sudo $0 /path/to/tunnel-credentials.json" >&2
  exit 1
fi

if [[ "$#" -ne 1 ]]; then
  echo "usage: sudo $0 /path/to/tunnel-credentials.json" >&2
  exit 2
fi

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
credentials_src="$1"
config_src="$repo_dir/cloudflared/s5dns-mux.yml"
if [[ ! -r "$credentials_src" ]]; then
  echo "credential file is not readable: $credentials_src" >&2
  exit 1
fi
if [[ ! -r "$config_src" ]]; then
  echo "live tunnel config is missing: $config_src" >&2
  exit 1
fi

tunnel_id="$(awk '$1 == "tunnel:" { print $2; exit }' "$config_src")"
if [[ ! "$tunnel_id" =~ ^[0-9a-fA-F-]{36}$ ]]; then
  echo "could not read a tunnel UUID from $config_src" >&2
  exit 1
fi

if ! id -u cloudflared >/dev/null 2>&1; then
  useradd --system --home-dir /var/lib/cloudflared --create-home --shell /usr/sbin/nologin cloudflared
fi

install -d -m 0750 -o root -g cloudflared /etc/cloudflared
install -d -m 0750 -o cloudflared -g cloudflared /var/lib/cloudflared
install -m 0640 -o root -g cloudflared "$credentials_src" "/etc/cloudflared/${tunnel_id}.json"
install -m 0640 -o root -g cloudflared "$config_src" /etc/cloudflared/s5dns-mux.yml
install -m 0644 -o root -g root "$repo_dir/systemd/cloudflared-s5dns.service" /etc/systemd/system/cloudflared-s5dns.service
systemctl daemon-reload

cat <<EOF
Cloudflared local-managed service artifacts installed for tunnel ${tunnel_id}.

Next steps:
  1. Ensure s5dns-server.service is installed and has its credentials in /etc/s5dns.
  2. Run: systemctl enable --now s5dns-server.service cloudflared-s5dns.service
  3. Check: systemctl status cloudflared-s5dns.service

The credentials were installed with mode 0640 and group cloudflared. The service was not started by this script.
EOF
