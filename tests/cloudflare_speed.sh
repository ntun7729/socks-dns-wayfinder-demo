#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work_dir="$(mktemp -d)"
target_pid=""
access_pid=""
client_pid=""
cleanup() {
  set +e
  for pid in "$client_pid" "$access_pid" "$target_pid"; do
    if [[ -n "$pid" ]]; then kill "$pid" 2>/dev/null || true; fi
  done
  wait "$client_pid" "$access_pid" "$target_pid" 2>/dev/null || true
  rm -rf "$work_dir"
}
trap cleanup EXIT

payload_bytes=$((32 * 1024 * 1024))
runs=3
proxy_port=18083
access_port=18446

tmp_ca="$work_dir/ca.crt"
tmp_token="$work_dir/client.token"
sudo cp /etc/s5dns/ca.crt "$tmp_ca"
sudo cp /etc/s5dns/client.token "$tmp_token"
sudo chown "$(id -u):$(id -g)" "$tmp_ca" "$tmp_token"
chmod 600 "$tmp_token"

python3 "$repo_dir/tests/speed_test.py" sink --port-file "$work_dir/target.port" --connections "$((runs * 2))" --bytes "$payload_bytes" >"$work_dir/target.log" 2>&1 &
target_pid=$!
for _ in $(seq 1 100); do
  [[ -s "$work_dir/target.port" ]] && break
  sleep 0.05
done
target_port="$(cat "$work_dir/target.port")"

cloudflared access tcp --hostname s5-edge-421b01.nyan.college --url "127.0.0.1:${access_port}" >"$work_dir/access.log" 2>&1 &
access_pid=$!
for _ in $(seq 1 160); do
  (echo >/dev/tcp/127.0.0.1/$access_port) 2>/dev/null && break
  sleep 0.05
done

"$repo_dir/s5dns" client -mux -server "127.0.0.1:${access_port}" -server-name localhost -ca "$tmp_ca" -token-file "$tmp_token" -socks-listen "127.0.0.1:${proxy_port}" -dns-listen 127.0.0.1:18538 >"$work_dir/client.log" 2>&1 &
client_pid=$!
for _ in $(seq 1 100); do
  (echo >/dev/tcp/127.0.0.1/$proxy_port) 2>/dev/null && break
  sleep 0.05
done

python3 "$repo_dir/tests/speed_test.py" measure --target-host 127.0.0.1 --target-port "$target_port" --bytes "$payload_bytes" --runs "$runs" --timeout 120 >"$work_dir/direct.json"
python3 "$repo_dir/tests/speed_test.py" measure --target-host 127.0.0.1 --target-port "$target_port" --proxy-host 127.0.0.1 --proxy-port "$proxy_port" --bytes "$payload_bytes" --runs "$runs" --timeout 120 >"$work_dir/cloudflare.json"
python3 "$repo_dir/tests/cloudflare_speed_summary.py" "$work_dir/direct.json" "$work_dir/cloudflare.json"
