#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work_dir="$(mktemp -d)"
target_pid=""
server_pid=""
client_pid=""
cleanup() {
  set +e
  for pid in "$client_pid" "$server_pid" "$target_pid"; do
    if [[ -n "$pid" ]]; then kill "$pid" 2>/dev/null || true; fi
  done
  wait "$client_pid" "$server_pid" "$target_pid" 2>/dev/null || true
  rm -rf "$work_dir"
}
trap cleanup EXIT

chunks=40
chunk_size=$((256 * 1024))
expected_bytes=$((chunks * chunk_size))
"$repo_dir/scripts/generate-certs.sh" "$work_dir/state" localhost >/dev/null 2>"$work_dir/cert.log"

python3 "$repo_dir/tests/streaming_target.py" --port-file "$work_dir/target.port" --chunks "$chunks" --chunk-size "$chunk_size" --interval-ms 25 >"$work_dir/target.log" 2>&1 &
target_pid=$!
for _ in $(seq 1 100); do
  [[ -s "$work_dir/target.port" ]] && break
  sleep 0.05
done
[[ -s "$work_dir/target.port" ]]
target_port="$(cat "$work_dir/target.port")"

"$repo_dir/s5dns" server -listen 127.0.0.1:20443 -cert "$work_dir/state/server.crt" -key "$work_dir/state/server.key" -token-file "$work_dir/state/server.token" -dns-upstream 127.0.0.1:53 >"$work_dir/server.log" 2>&1 &
server_pid=$!
for _ in $(seq 1 100); do
  (echo >/dev/tcp/127.0.0.1/20443) 2>/dev/null && break
  sleep 0.05
done

"$repo_dir/s5dns" client -server 127.0.0.1:20443 -server-name localhost -ca "$work_dir/state/ca.crt" -token-file "$work_dir/state/client.token" -socks-listen 127.0.0.1:20080 -dns-listen 127.0.0.1:20535 >"$work_dir/client.log" 2>&1 &
client_pid=$!
for _ in $(seq 1 100); do
  (echo >/dev/tcp/127.0.0.1/20080) 2>/dev/null && break
  sleep 0.05
done

python3 "$repo_dir/tests/streaming_test.py" --proxy-port 20080 --target-port "$target_port" --expected-bytes "$expected_bytes"
