# s5dns

`s5dns` is a small reference implementation of a **user-space SOCKS5 plus DNS tunnel** for Ubuntu. It has a single binary with `server` and `client` roles. The client exposes a loopback SOCKS5 listener and an explicit loopback DNS listener; optimized `-mux` mode carries streams over one authenticated session. That session can use raw TLS/TCP or a domain-only WSS transport through Cloudflare. The server performs the final TCP connect or UDP DNS lookup.

This is intentionally a **proxy tunnel, not a transparent IP VPN**. It does not create a TUN/TAP device, change routes, rewrite `/etc/resolv.conf`, intercept packets, or forward arbitrary IP traffic. Applications must use SOCKS5, and DNS clients must be pointed explicitly at `127.0.0.1:5353`.

## Design

| Layer | Choice | Purpose |
|---|---|---|
| Local application interface | RFC 1928 SOCKS5 `CONNECT` | Supports IPv4, IPv6, and domain-name targets; domain names are resolved by the remote server. |
| Local DNS interface | UDP DNS wire messages on `127.0.0.1:5353` | Sends one bounded DNS request through the selected raw-TLS or WSS transport to the configured upstream resolver. |
| Tunnel transport | Raw TLS/TCP or WSS over HTTPS | Raw mode uses TLS 1.3 minimum; domain-only mode uses RFC 6455 binary WebSocket frames through an HTTPS hostname. |
| Peer authentication | Private CA where applicable plus shared credential | Raw TLS and plain `ws://` can use the private CA; public `wss://` validates the Cloudflare certificate with system roots and uses the shared s5dns password/UUID for application authentication. The credential may come from an environment variable, a command-line flag, or a legacy token file. |
| Remote operations | TCP `CONNECT` and UDP DNS only | Keeps the first prototype narrow and avoids a control plane. |

The SOCKS5 listener follows the standard version, address-type, and `CONNECT` request shape defined by [RFC 1928][1]. `BIND` and `UDP ASSOCIATE` are rejected in this version. The explicit DNS listener forwards the original DNS wire message to the server’s configured UDP upstream and returns the raw response. DNS messages are bounded to 4096 bytes; users who need larger responses should add TCP DNS or an EDNS-aware policy in a later iteration.

## Build

The build requires Go 1.22 or newer. From the repository root:

```bash
gofmt -w main.go
go build -trimpath -o s5dns .
./s5dns version
```

The binary uses only the Go standard library and is suitable for a static Linux build if desired:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w -buildid=' -o s5dns .
```

### Password/UUID authentication

The shared token is an application credential rather than a human login password. You can use any high-entropy random value, such as a UUID, on both ends. The preferred form is an environment variable because it does not appear in the process list:

```bash
export S5DNS_PASSWORD='replace-with-the-same-random-uuid-on-both-hosts'

./s5dns server \
  -password-env S5DNS_PASSWORD \
  -cert ./state/server.crt \
  -key ./state/server.key

./s5dns client -mux \
  -websocket-url wss://s5-edge-421b01.nyan.college/s5dns \
  -password-env S5DNS_PASSWORD
```

The `-password UUID` flag is also supported for short-lived manual tests, but it is visible to local users through process inspection. `-token-file` remains supported for existing systemd installations and older deployments; credential precedence is direct `-password`, then `-password-env`, then `-token-file`.

## Docker and GHCR image

The repository includes a multi-stage `Dockerfile` that produces a `scratch` image containing only the stripped static binary and the public CA bundle. It has no shell, package manager, or build tool in the final layer. The workflow in [`.github/workflows/publish-ghcr.yml`](.github/workflows/publish-ghcr.yml) publishes `linux/amd64` and `linux/arm64` images to GHCR on pushes to `main`, version tags such as `v0.5.0`, and manual workflow runs.

After the first successful workflow run, pull the image with:

```bash
docker pull ghcr.io/ntun7729/socks-dns-wayfinder-demo:latest
```

For a WSS client, the compact container only needs the shared password in an environment variable. It exposes SOCKS5 on port 1080 and explicit DNS on port 5353:

```bash
docker run --rm --name s5dns-client \
  --env S5DNS_PASSWORD='replace-with-the-same-random-uuid' \
  -p 127.0.0.1:1080:1080/tcp \
  -p 127.0.0.1:5353:5353/udp \
  ghcr.io/ntun7729/socks-dns-wayfinder-demo:latest \
  client -mux \
    -websocket-url wss://s5-edge-421b01.nyan.college/s5dns \
    -password-env S5DNS_PASSWORD \
    -socks-listen 0.0.0.0:1080 \
    -dns-listen 0.0.0.0:5353
```

For a server container behind the existing host-side `cloudflared` connector, use host networking so the WebSocket origin remains at `127.0.0.1:9443`. Mount the certificate and key read-only, and keep the key readable by UID/GID `65532` or use a separate secret mechanism supported by your container runtime:

```bash
docker run -d --restart unless-stopped --name s5dns-server \
  --network host \
  --env S5DNS_PASSWORD='replace-with-the-same-random-uuid' \
  --mount type=bind,src=/etc/s5dns/server.crt,dst=/etc/s5dns/server.crt,readonly \
  --mount type=bind,src=/etc/s5dns/server.key,dst=/etc/s5dns/server.key,readonly \
  ghcr.io/ntun7729/socks-dns-wayfinder-demo:latest \
  server -listen 127.0.0.1:8443 \
    -cert /etc/s5dns/server.crt \
    -key /etc/s5dns/server.key \
    -password-env S5DNS_PASSWORD \
    -ws-listen 127.0.0.1:9443
```

The image intentionally does not contain server certificates, private keys, or credentials. Supply those at runtime through read-only mounts and environment/secret injection.

### Render test deployment

The repository includes [`render.yaml`](render.yaml) and a Render-compatible default [`Dockerfile`](Dockerfile). The Render service is a **Web Service** so it can use Render’s free web-service tier and expose a health endpoint. The public client still reaches the s5dns WebSocket origin through Cloudflare; Render does not need to expose the tunnel origin publicly.[10] [11]

Create the service from the repository Blueprint or create a Render Web Service manually. Set **Dockerfile Path** to `./Dockerfile` (the default) and set **Docker Command** to `/usr/local/bin/render-supervisor` if Render asks for an explicit command. Do not use the compact `Dockerfile.compact` for this Render deployment. Set the health-check path to `/healthz`. No `server.crt` or `server.key` files are required for this WSS-only Render mode.

| Render setting | Value |
|---|---|
| Service type | Web Service |
| Plan | Free |
| `S5DNS_PASSWORD` | The same shared password/UUID used by clients; mark it secret. |
| `CLOUDFLARED_TUNNEL_TOKEN` | Optional remotely-managed Cloudflare tunnel token; leave empty to run only s5dns. |
| `S5DNS_DNS_UPSTREAM` | Optional, defaults to `1.1.1.1:53`. |
| Health check path | `/healthz` |

Render supplies environment variables to Docker services at runtime.[12] A Cloudflare remotely-managed tunnel can be run with only its tunnel token.[13] When `CLOUDFLARED_TUNNEL_TOKEN` is empty, the Render supervisor starts s5dns in WebSocket-only mode and listens on Render’s `PORT` for `/healthz`. When it is set, it additionally starts `cloudflared tunnel --no-autoupdate run --token ...` in the same container, with the s5dns origin at `127.0.0.1:9443`.

The principal Render image is intentionally separate from the small GHCR s5dns image because `cloudflared` needs its runtime base libraries. The default `Dockerfile` and `Dockerfile.render` include both binaries for Render; the GHCR workflow explicitly uses `Dockerfile.compact` so `ghcr.io/ntun7729/socks-dns-wayfinder-demo:latest` remains the small standalone s5dns image.

### Optional Cloudflare Tunnel profile

[`compose.yaml`](compose.yaml) keeps Cloudflare optional. The `s5dns-server` service runs normally without `cloudflared`; the `cloudflared` service is placed in a separate Compose profile. The helper [`scripts/compose-up.sh`](scripts/compose-up.sh) starts only s5dns when no tunnel token is present, and automatically enables the Cloudflare profile when `CLOUDFLARED_TUNNEL_TOKEN` is set.

For normal operation without Cloudflare:

```bash
export S5DNS_PASSWORD='the-same-random-uuid-used-by-the-client'
export S5DNS_CONFIG_DIR=/etc/s5dns
./scripts/compose-up.sh
```

To additionally start the Cloudflare connector, provide the tunnel token and run the same command:

```bash
export CLOUDFLARED_TUNNEL_TOKEN='paste-your-cloudflare-tunnel-token'
./scripts/compose-up.sh
```

The equivalent explicit commands are `docker compose up -d` without the token and `docker compose --profile cloudflare up -d` with the token.

To stop only the optional connector while leaving s5dns running:

```bash
docker compose stop cloudflared
```

When `CLOUDFLARED_TUNNEL_TOKEN` is absent, `docker compose up -d s5dns-server` does not attempt to start Cloudflare. If the `cloudflare` profile is explicitly enabled without a token, the connector exits instead of silently creating an unauthenticated tunnel.

## Generate demo credentials

Run the helper on the machine that will host the server certificate and shared token:

```bash
./scripts/generate-certs.sh ./state localhost
ls -l ./state
```

The generated `ca.crt` is copied to the client. The server and client token files contain the same random value. Keep `ca.key`, `server.key`, and both token files private. The sample client verifies the configured server name against the CA; it does not use `InsecureSkipVerify`.

## Run directly as root

The program does not require root for its network operations, but it can run from a root shell and can be installed as a system service. In two terminals on the same Ubuntu host, start the server first:

```bash
sudo ./s5dns server \
  -listen 127.0.0.1:8443 \
  -cert ./state/server.crt \
  -key ./state/server.key \
  -token-file ./state/server.token \
  -dns-upstream 1.1.1.1:53
```

Then start the client:

```bash
sudo ./s5dns client \
  -mux \
  -server 127.0.0.1:8443 \
  -server-name localhost \
  -ca ./state/ca.crt \
  -token-file ./state/client.token \
  -socks-listen 127.0.0.1:1080 \
  -dns-listen 127.0.0.1:5353
```

For a real two-host deployment, bind the server to an externally reachable address, allow only the chosen TLS port through the host firewall, and use a certificate whose SAN matches the server name passed to the client. Do not expose the server with an empty or shared public token. The `-mux` flag is recommended; omitting it keeps the older one-TLS-connection-per-SOCKS-request behavior for compatibility testing.

## Install as systemd services

Build the binary and generate credentials first. The installer accepts the credential directory as its single argument:

```bash
go build -trimpath -o s5dns .
sudo ./scripts/install.sh "$PWD/state"
```

The installed units run as dedicated unprivileged users and apply systemd restrictions including `NoNewPrivileges`, `PrivateDevices`, `ProtectSystem=strict`, `ProtectHome`, restricted address families, and an empty capability bounding set. Root is used for installation, protected files, and service management rather than for packet interception. These controls are based on the execution-environment directives documented by systemd.[4]

```bash
sudo systemctl enable --now s5dns-server.service
sudo systemctl enable --now s5dns-client.service
sudo systemctl status s5dns-server.service s5dns-client.service
```

The sample units assume that the client and server run on the same host and use `127.0.0.1:8443`. For a split deployment, edit `systemd/s5dns-client.service` before installation so that `-server` points to the remote server, then copy the CA and client token to the client host.

## Use through Cloudflare Tunnel

The preferred Cloudflare integration is now **domain-only WSS**. The client connects directly to `wss://s5-edge-421b01.nyan.college/s5dns` and does not need `cloudflared` or a local TCP forwarder. The host-side `cloudflared` connector publishes the loopback HTTP origin at `127.0.0.1:9443`; the s5dns server upgrades only `/s5dns` to WebSocket and returns 404 for other paths. Cloudflare supports proxied WebSockets, while RFC 6455 defines the upgrade, binary framing, masking, and close behavior.[8] [9] The sandbox is configured with `cloudflared` **2026.7.3**, tunnel `s5dns-mux`, and hostname `s5-edge-421b01.nyan.college`.

The host-side path is:

```text
s5dns server 127.0.0.1:8443       (raw TLS compatibility)
s5dns WebSocket HTTP 127.0.0.1:9443
        │
        └── cloudflared HTTP origin → Cloudflare HTTPS/WSS hostname
```

On the Ubuntu host, install the package and prepare the service artifacts:

```bash
sudo ./scripts/install-cloudflared.sh \
  /home/ubuntu/.cloudflared/<TUNNEL_UUID>.json
```

For this local-managed tunnel, install the generated credential file and route the hostname to `http://127.0.0.1:9443`. The checked-in server unit starts both the raw TLS listener and the WebSocket origin:

```bash
sudo systemctl enable --now s5dns-server.service
sudo systemctl enable --now cloudflared-s5dns.service
sudo systemctl status s5dns-server.service cloudflared-s5dns.service
```

For a locally managed tunnel, use [`cloudflared/config.yml.example`](cloudflared/config.yml.example), replace its tunnel UUID, credentials path, and hostname, and follow Cloudflare’s `tunnel login`, `tunnel create`, DNS-route, and `tunnel run` workflow.[6] The configuration must end with the included catch-all rule.[7]

On each client device, install only the `s5dns` binary and set the same shared password/UUID. No cloudflared process, private CA file, or client token file is needed for public WSS:

```bash
export S5DNS_PASSWORD='the-same-random-uuid-configured-on-the-server'
./s5dns client -mux \
  -websocket-url wss://s5-edge-421b01.nyan.college/s5dns \
  -password-env S5DNS_PASSWORD \
  -socks-listen 127.0.0.1:1080 \
  -dns-listen 127.0.0.1:5353
```

The checked-in helper [`cloudflared/domain-client.example.sh`](cloudflared/domain-client.example.sh) contains the domain-only client pattern. [`cloudflared/access-client.example.sh`](cloudflared/access-client.example.sh) remains available only for the older raw TCP compatibility path. The live non-secret configuration is in [`cloudflared/s5dns-mux.yml`](cloudflared/s5dns-mux.yml), while the tunnel credential remains outside the repository under `/etc/cloudflared/`. The connector is active with four registered Cloudflare edge connections, and the direct WSS sandbox check passed for SOCKS5 TCP and public DNS forwarding. Add a Cloudflare Access application policy for the hostname if you want outer SSO/MFA enforcement; the inner s5dns token remains mandatory regardless.

## Use the local interfaces

For SOCKS-aware tools:

```bash
curl --proxy socks5h://127.0.0.1:1080 https://example.com/
```

The `socks5h` form asks the application to send the domain name through SOCKS5 so that the remote server performs the resolution. To query the explicit DNS listener:

```bash
dig @127.0.0.1 -p 5353 example.com A
```

The DNS listener is deliberately explicit. It does not replace the system resolver, because changing resolver configuration would make this proxy look like a transparent VPN and would create host-wide side effects.

## Acceptance tests

The repository includes a local test script that starts an ephemeral server and client, checks TLS-authenticated SOCKS5 access to a local TCP target, checks DNS forwarding against a local UDP target, rejects a wrong token, and verifies that no TUN/TAP device is created by the client. Run it after building:

```bash
./tests/smoke.sh
```

The test uses only loopback services and does not require a public internet destination. It is therefore safe to run in a development VM, although the implementation itself should still be treated as experimental.

A separate root-capable benchmark compares direct loopback TCP throughput with the same payload sent through the SOCKS5/TLS path. It uses an in-process local sink, three 32 MiB samples per mode, and reports median throughput and median loss:

```bash
sudo ./tests/speed.sh
```

This is a **relative local overhead measurement**, not an internet speed test. It excludes WAN latency, server geography, congestion, and external resolver performance. The original development-sandbox result was approximately **41.46 Gbit/s direct loopback versus 7.65 Gbit/s through SOCKS5/TLS**, or **81.55% lower median throughput**. After adding pooled `io.CopyBuffer` forwarding, 1 MiB TCP socket buffers, and a TLS client session cache, the latest multiplexed rerun measured **23.54 Gbit/s direct versus 5.85 Gbit/s through SOCKS5/TLS**, or **75.17% lower median throughput**. The direct loopback baseline varies substantially in this virtualized sandbox, so this should not be read as a definitive single-flow gain; the concurrent result is the more relevant measurement for the multiplexed design. Run it on the target Ubuntu host for meaningful capacity planning.

A progressive streaming test is also included. It sends 40 paced 256 KiB chunks, verifies that HTTP-like headers and body bytes arrive before the full stream completes, and measures sustained delivery through multiplexed SOCKS5/TLS. The domain-only version uses the public WSS hostname and does not start cloudflared on the client:

```bash
sudo ./tests/streaming.sh
./tests/wss_streaming.sh
```

The latest root sandbox run delivered **10 MiB** in approximately **0.993 seconds** at **84.44 Mbit/s**, with headers and the first body bytes arriving after approximately **5.4 ms**. This is a local streaming-path check, not a real internet video-streaming measurement.

The concurrent benchmark opens **eight simultaneous SOCKS5 flows**, each carrying 8 MiB through the shared multiplexed session:

```bash
sudo ./tests/concurrent.sh
```

The latest root run completed successfully with approximately **5.92 Gbit/s aggregate throughput** and per-stream completion times around **82–90 ms**. This measures concurrent local capacity and multiplexing correctness, not WAN performance.

The domain-only WSS benchmark is available as `tests/wss_speed.sh`. Its default is three 4 MiB samples because the public WebSocket path has substantial sandbox/edge latency; set `WSS_PAYLOAD_BYTES` and `WSS_RUNS` to change the workload. The WSS client now automatically uses an 8 MiB TCP buffer, while mux data frames use the bounded 256 KiB payload size. After this change, the latest sandbox run measured **21.54 Gbit/s direct loopback versus 10.14 Mbit/s through WSS plus s5dns**. That is approximately **99.95% relative loss**, but about a **13× improvement** over the earlier 0.784 Mbit/s median. This is a Cloudflare-path measurement from the same sandbox, not an internet-client benchmark. The WSS streaming test improved from 136.7 seconds at 0.614 Mbit/s to **24.0 seconds at 3.49 Mbit/s** for 10 MiB, with headers after **4.87 seconds** and first body bytes after **5.76 seconds**. Progressive delivery still passed, but the result remains too slow for practical video streaming in this environment and requires external-client/WAN testing before deployment.


## Limitations and next decisions

The client supports an optimized `-mux` mode (release `0.4.0`) that authenticates once and carries multiple SOCKS5 streams over one persistent session. Raw mode uses TLS/TCP; domain-only mode uses RFC 6455 WSS binary frames through the Cloudflare hostname. The WebSocket adapter implements bounded messages, binary framing, client/server close behavior, periodic ping heartbeats, outer public-certificate validation, 256 KiB mux payloads, and an automatic 8 MiB WSS client TCP buffer. Existing open TCP streams are not replayed after a session loss. The forwarding path also uses pooled `io.CopyBuffer` buffers, tunable TCP read/write buffers through `-tcp-buffer`, and a client TLS session cache for reconnects. It supports only TCP `CONNECT` and DNS-over-UDP forwarding, has no access-control list beyond the shared token, and does not offer transparent routing.

The multiplexed session is still carried over one TCP connection, and WSS adds Cloudflare’s HTTP/WebSocket proxy path, so TCP-level head-of-line blocking and provider path variability remain possible. Cloudflare may close idle WebSockets, which is why the adapter sends heartbeat pings.[9] A production follow-up would need stronger identity lifecycle management, policy controls, metrics, structured audit logs, careful DNS-over-TCP and EDNS behavior, and a decision about whether QUIC is justified for lossy or high-latency networks.

## References

[1]: https://datatracker.ietf.org/doc/html/rfc1928 "RFC 1928 — SOCKS Protocol Version 5"
[2]: https://datatracker.ietf.org/doc/html/rfc1035 "RFC 1035 — Domain Names: Implementation and Specification"
[3]: https://pkg.go.dev/crypto/tls "Go crypto/tls package"
[4]: https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html "systemd.exec execution environment configuration"
[5]: https://developers.cloudflare.com/cloudflare-one/access-controls/applications/non-http/cloudflared-authentication/arbitrary-tcp/ "Cloudflare One — Arbitrary TCP"
[6]: https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/local-management/create-local-tunnel/ "Cloudflare One — Create a locally-managed tunnel"
[7]: https://developers.cloudflare.com/tunnel/advanced/local-management/configuration-file/ "Cloudflare Tunnel — Configuration file"
[8]: https://datatracker.ietf.org/doc/html/rfc6455 "RFC 6455 — The WebSocket Protocol"
[9]: https://developers.cloudflare.com/network/websockets/ "Cloudflare Network — WebSockets"
[10]: https://render.com/docs/background-workers "Render — Background Workers"
[11]: https://render.com/docs/service-types "Render — Services and Service Types"
[12]: https://render.com/docs/configure-environment-variables "Render — Environment Variables and Secrets"
[13]: https://developers.cloudflare.com/tunnel/advanced/tunnel-tokens/ "Cloudflare — Tunnel Tokens"
