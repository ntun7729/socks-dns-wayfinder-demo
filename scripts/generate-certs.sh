#!/usr/bin/env bash
set -euo pipefail

out_dir="${1:-./state}"
server_name="${2:-localhost}"
umask 077
mkdir -p "$out_dir"

openssl req -x509 -newkey rsa:3072 -nodes -days 3650 \
  -subj "/CN=s5dns demo CA" \
  -keyout "$out_dir/ca.key" \
  -out "$out_dir/ca.crt" \
  -addext "basicConstraints=critical,CA:TRUE,pathlen:1" \
  -addext "keyUsage=critical,keyCertSign,cRLSign"

openssl req -newkey rsa:2048 -nodes \
  -subj "/CN=$server_name" \
  -keyout "$out_dir/server.key" \
  -out "$out_dir/server.csr"

cat > "$out_dir/server.ext" <<EOF
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=DNS:$server_name,IP:127.0.0.1
EOF

openssl x509 -req -sha256 -days 825 \
  -in "$out_dir/server.csr" \
  -CA "$out_dir/ca.crt" -CAkey "$out_dir/ca.key" -CAcreateserial \
  -out "$out_dir/server.crt" -extfile "$out_dir/server.ext"

openssl rand -hex 32 > "$out_dir/server.token"
cp "$out_dir/server.token" "$out_dir/client.token"
rm -f "$out_dir/server.csr" "$out_dir/server.ext" "$out_dir/ca.srl"
chmod 600 "$out_dir"/ca.key "$out_dir"/server.key "$out_dir"/server.token "$out_dir"/client.token
chmod 644 "$out_dir"/ca.crt "$out_dir"/server.crt
printf 'generated CA, server certificate, and shared token in %s\n' "$out_dir"
