#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work_dir="$(mktemp -d)"
target_pid=""
client_pid=""
cleanup() {
  set +e
  for pid in "$client_pid" "$target_pid"; do
    [[ -n "$pid" ]] && kill "$pid" 2>/dev/null || true
  done
  wait "$client_pid" "$target_pid" 2>/dev/null || true
  rm -rf "$work_dir"
}
trap cleanup EXIT

sudo cp /etc/s5dns/client.token "$work_dir/client.token"
sudo chown "$(id -u):$(id -g)" "$work_dir/client.token"
chmod 600 "$work_dir/client.token"

python3 "$repo_dir/tests/local_targets.py" "$work_dir/tcp.port" "$work_dir/dns.port" >"$work_dir/target.log" 2>&1 &
target_pid=$!
for _ in $(seq 1 100); do
  [[ -s "$work_dir/tcp.port" ]] && break
  sleep 0.05
done
tcp_port="$(cat "$work_dir/tcp.port")"

"$repo_dir/s5dns" client -mux -websocket-url wss://s5-edge-421b01.nyan.college/s5dns -token-file "$work_dir/client.token" -socks-listen 127.0.0.1:18096 -dns-listen 127.0.0.1:18549 >"$work_dir/client.log" 2>&1 &
client_pid=$!
for _ in $(seq 1 160); do
  (echo >/dev/tcp/127.0.0.1/18096) 2>/dev/null && break
  sleep 0.05
done

curl --fail --silent --show-error --max-time 30 --proxy socks5h://127.0.0.1:18096 "http://127.0.0.1:${tcp_port}/" | grep -Fqx 's5dns-tcp-ok'
python3 "$repo_dir/tests/public_dns_query.py" 127.0.0.1 18549
printf 'wss-domain-socks-dns-ok\n'
