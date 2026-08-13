#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID}" -ne 0 ]]; then
  echo "run as root: sudo $0 [state-directory]" >&2
  exit 1
fi

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
state_dir="${1:-}"

install -Dm755 "$repo_dir/s5dns" /usr/local/bin/s5dns
getent group s5dns >/dev/null || groupadd --system s5dns
getent passwd s5dns >/dev/null || useradd --system --no-create-home --shell /usr/sbin/nologin --gid s5dns s5dns
getent group s5dns-client >/dev/null || groupadd --system s5dns-client
getent passwd s5dns-client >/dev/null || useradd --system --no-create-home --shell /usr/sbin/nologin --gid s5dns-client s5dns-client
install -d -m0751 -o root -g s5dns /etc/s5dns

if [[ -n "$state_dir" ]]; then
  for file in ca.crt server.crt; do
    install -Dm0644 -o root -g root "$state_dir/$file" "/etc/s5dns/$file"
  done
  install -Dm0640 -o root -g s5dns "$state_dir/server.key" /etc/s5dns/server.key
  install -Dm0640 -o root -g s5dns "$state_dir/server.token" /etc/s5dns/server.token
  install -Dm0640 -o root -g s5dns-client "$state_dir/client.token" /etc/s5dns/client.token
fi

install -Dm0644 "$repo_dir/systemd/s5dns-server.service" /etc/systemd/system/s5dns-server.service
install -Dm0644 "$repo_dir/systemd/s5dns-client.service" /etc/systemd/system/s5dns-client.service
systemctl daemon-reload
cat <<'EOF'
installed /usr/local/bin/s5dns and systemd units
start the server with: systemctl enable --now s5dns-server.service
start the local client with: systemctl enable --now s5dns-client.service
EOF
