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

chunks=40
chunk_size=$((256 * 1024))
expected_bytes=$((chunks * chunk_size))
sudo cp /etc/s5dns/client.token "$work_dir/client.token"
sudo chown "$(id -u):$(id -g)" "$work_dir/client.token"
chmod 600 "$work_dir/client.token"

python3 "$repo_dir/tests/streaming_target.py" --port-file "$work_dir/target.port" --chunks "$chunks" --chunk-size "$chunk_size" --interval-ms 25 >"$work_dir/target.log" 2>&1 &
target_pid=$!
for _ in $(seq 1 100); do
  [[ -s "$work_dir/target.port" ]] && break
  sleep 0.05
done
target_port="$(cat "$work_dir/target.port")"

"$repo_dir/s5dns" client -mux -websocket-url wss://s5-edge-421b01.nyan.college/s5dns -token-file "$work_dir/client.token" -socks-listen 127.0.0.1:18092 -dns-listen 127.0.0.1:18545 >"$work_dir/client.log" 2>&1 &
client_pid=$!
for _ in $(seq 1 160); do
  (echo >/dev/tcp/127.0.0.1/18092) 2>/dev/null && break
  sleep 0.05
done

python3 "$repo_dir/tests/streaming_test.py" --proxy-port 18092 --target-port "$target_port" --expected-bytes "$expected_bytes"
