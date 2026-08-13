#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work_dir="$(mktemp -d)"
server_pid=""
client_pid=""
target_pid=""
wrong_pid=""
cleanup() {
  set +e
  for pid in "$wrong_pid" "$client_pid" "$server_pid" "$target_pid"; do
    if [[ -n "$pid" ]]; then kill "$pid" 2>/dev/null || true; fi
  done
  wait "$wrong_pid" "$client_pid" "$server_pid" "$target_pid" 2>/dev/null || true
  rm -rf "$work_dir"
}
trap cleanup EXIT

if [[ ! -x "$repo_dir/s5dns" ]]; then
  echo "build ./s5dns first" >&2
  exit 1
fi

"$repo_dir/scripts/generate-certs.sh" "$work_dir/state" localhost >/dev/null
python3 "$repo_dir/tests/local_targets.py" "$work_dir/tcp.port" "$work_dir/dns.port" >"$work_dir/target.log" 2>&1 &
target_pid=$!
for _ in $(seq 1 100); do
  [[ -s "$work_dir/tcp.port" && -s "$work_dir/dns.port" ]] && break
  sleep 0.05
done
[[ -s "$work_dir/tcp.port" && -s "$work_dir/dns.port" ]]
tcp_port="$(cat "$work_dir/tcp.port")"
dns_port="$(cat "$work_dir/dns.port")"

before_tun="$(find /sys/class/net -maxdepth 1 -type l -name 'tun*' -printf '%f\n' 2>/dev/null | sort)"
"$repo_dir/s5dns" server -listen 127.0.0.1:18443 -cert "$work_dir/state/server.crt" -key "$work_dir/state/server.key" -token-file "$work_dir/state/server.token" -dns-upstream "127.0.0.1:$dns_port" >"$work_dir/server.log" 2>&1 &
server_pid=$!
for _ in $(seq 1 100); do
  (echo >/dev/tcp/127.0.0.1/18443) 2>/dev/null && break
  sleep 0.05
done

"$repo_dir/s5dns" client -server 127.0.0.1:18443 -server-name localhost -ca "$work_dir/state/ca.crt" -token-file "$work_dir/state/client.token" -socks-listen 127.0.0.1:18080 -dns-listen 127.0.0.1:15353 >"$work_dir/client.log" 2>&1 &
client_pid=$!
for _ in $(seq 1 100); do
  (echo >/dev/tcp/127.0.0.1/18080) 2>/dev/null && break
  sleep 0.05
done

python3 - "$tcp_port" <<'PY'
import socket
import sys
port = int(sys.argv[1])
with socket.create_connection(("127.0.0.1", 18080), timeout=5) as sock:
    sock.sendall(b"\x05\x01\x00")
    assert sock.recv(2) == b"\x05\x00"
    target = socket.inet_aton("127.0.0.1") + int(port).to_bytes(2, "big")
    sock.sendall(b"\x05\x01\x00\x01" + target)
    reply = sock.recv(10)
    assert reply[:2] == b"\x05\x00", reply
    sock.sendall(b"GET / HTTP/1.0\r\nHost: local\r\n\r\n")
    data = sock.recv(4096)
    assert b"s5dns-tcp-ok" in data, data
print("socks5-connect-ok")
PY

python3 "$repo_dir/tests/dns_query.py" 127.0.0.1 15353

cp "$work_dir/state/client.token" "$work_dir/bad.token"
printf 'wrong-token\n' > "$work_dir/bad.token"
"$repo_dir/s5dns" client -server 127.0.0.1:18443 -server-name localhost -ca "$work_dir/state/ca.crt" -token-file "$work_dir/bad.token" -socks-listen 127.0.0.1:18081 -dns-listen 127.0.0.1:15354 >"$work_dir/wrong.log" 2>&1 &
wrong_pid=$!
for _ in $(seq 1 100); do
  (echo >/dev/tcp/127.0.0.1/18081) 2>/dev/null && break
  sleep 0.05
done
set +e
python3 - <<'PY'
import socket
with socket.create_connection(("127.0.0.1", 18081), timeout=5) as sock:
    sock.sendall(b"\x05\x01\x00")
    assert sock.recv(2) == b"\x05\x00"
    sock.sendall(b"\x05\x01\x00\x01\x7f\x00\x00\x01\x00\x50")
    reply = sock.recv(10)
    assert reply[:2] == b"\x05\x01", reply
PY
wrong_status=$?
set -e
[[ "$wrong_status" -eq 0 ]]

after_tun="$(find /sys/class/net -maxdepth 1 -type l -name 'tun*' -printf '%f\n' 2>/dev/null | sort)"
[[ "$before_tun" == "$after_tun" ]]

printf 'tls-authentication-ok\nno-tun-tap-evidence-ok\n'
