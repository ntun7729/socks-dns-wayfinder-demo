#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work_dir="$(mktemp -d)"
server_pid=""
client_pid=""
sink_pid=""
cleanup() {
  set +e
  for pid in "$client_pid" "$server_pid" "$sink_pid"; do
    if [[ -n "$pid" ]]; then kill "$pid" 2>/dev/null || true; fi
  done
  wait "$client_pid" "$server_pid" "$sink_pid" 2>/dev/null || true
  rm -rf "$work_dir"
}
trap cleanup EXIT

bytes=$((32 * 1024 * 1024))
runs=3
connections=$((runs * 2))
"$repo_dir/scripts/generate-certs.sh" "$work_dir/state" localhost >/dev/null 2>"$work_dir/cert.log"

python3 "$repo_dir/tests/speed_test.py" sink --bytes "$bytes" --connections "$connections" --port-file "$work_dir/target.port" >"$work_dir/sink.log" 2>&1 &
sink_pid=$!
for _ in $(seq 1 100); do
  [[ -s "$work_dir/target.port" ]] && break
  sleep 0.05
done
[[ -s "$work_dir/target.port" ]]
target_port="$(cat "$work_dir/target.port")"

"$repo_dir/s5dns" server -listen 127.0.0.1:19443 -cert "$work_dir/state/server.crt" -key "$work_dir/state/server.key" -token-file "$work_dir/state/server.token" -dns-upstream 127.0.0.1:53 >"$work_dir/server.log" 2>&1 &
server_pid=$!
for _ in $(seq 1 100); do
  (echo >/dev/tcp/127.0.0.1/19443) 2>/dev/null && break
  sleep 0.05
done

"$repo_dir/s5dns" client -mux -server 127.0.0.1:19443 -server-name localhost -ca "$work_dir/state/ca.crt" -token-file "$work_dir/state/client.token" -socks-listen 127.0.0.1:19080 -dns-listen 127.0.0.1:19535 >"$work_dir/client.log" 2>&1 &
client_pid=$!
for _ in $(seq 1 100); do
  (echo >/dev/tcp/127.0.0.1/19080) 2>/dev/null && break
  sleep 0.05
done

python3 "$repo_dir/tests/speed_test.py" measure --target-port "$target_port" --bytes "$bytes" --runs "$runs" >"$work_dir/direct.json"
python3 "$repo_dir/tests/speed_test.py" measure --target-port "$target_port" --proxy-host 127.0.0.1 --proxy-port 19080 --bytes "$bytes" --runs "$runs" >"$work_dir/tunnel.json"
python3 "$repo_dir/tests/speed_summary.py" --direct "$work_dir/direct.json" --tunnel "$work_dir/tunnel.json"
