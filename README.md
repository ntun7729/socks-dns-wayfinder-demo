# s5dns

`s5dns` is a small reference implementation of a **user-space SOCKS5 plus DNS tunnel** for Ubuntu. It has a single binary with `server` and `client` roles. The client exposes a loopback SOCKS5 listener and an explicit loopback DNS listener; each request is carried over its own outbound TLS connection to the server. The server performs the final TCP connect or UDP DNS lookup.

This is intentionally a **proxy tunnel, not a transparent IP VPN**. It does not create a TUN/TAP device, change routes, rewrite `/etc/resolv.conf`, intercept packets, or forward arbitrary IP traffic. Applications must use SOCKS5, and DNS clients must be pointed explicitly at `127.0.0.1:5353`.

## Design

| Layer | Choice | Purpose |
|---|---|---|
| Local application interface | RFC 1928 SOCKS5 `CONNECT` | Supports IPv4, IPv6, and domain-name targets; domain names are resolved by the remote server. |
| Local DNS interface | UDP DNS wire messages on `127.0.0.1:5353` | Sends one bounded DNS request per TLS connection to the configured upstream resolver. |
| Tunnel transport | TCP with TLS 1.3 minimum | Provides confidentiality and integrity using Go’s standard `crypto/tls` package. |
| Peer authentication | Private CA plus shared token | The client verifies the server certificate and name; the server checks a constant-time token comparison. |
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
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o s5dns .
```

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
  -server 127.0.0.1:8443 \
  -server-name localhost \
  -ca ./state/ca.crt \
  -token-file ./state/client.token \
  -socks-listen 127.0.0.1:1080 \
  -dns-listen 127.0.0.1:5353
```

For a real two-host deployment, bind the server to an externally reachable address, allow only the chosen TLS port through the host firewall, and use a certificate whose SAN matches the server name passed to the client. Do not expose the server with an empty or shared public token.

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

This is a **relative local overhead measurement**, not an internet speed test. It excludes WAN latency, server geography, congestion, and external resolver performance. The measured result in the development sandbox was approximately **41.46 Gbit/s direct loopback versus 7.65 Gbit/s through SOCKS5/TLS**, or **81.55% lower median throughput** under that specific six-CPU virtualized environment. Run it on the target Ubuntu host for meaningful capacity planning.

## Limitations and next decisions

The prototype has one TLS connection per SOCKS5 or DNS request rather than a multiplexed session. It supports only TCP `CONNECT` and DNS-over-UDP forwarding, has no access-control list beyond the shared token, and does not offer transparent routing. A production follow-up would need connection multiplexing, a stronger identity lifecycle than a bearer token, policy controls, metrics, structured audit logs, and a careful treatment of DNS-over-TCP and EDNS behavior.

## References

[1]: https://datatracker.ietf.org/doc/html/rfc1928 "RFC 1928 — SOCKS Protocol Version 5"
[2]: https://datatracker.ietf.org/doc/html/rfc1035 "RFC 1035 — Domain Names: Implementation and Specification"
[3]: https://pkg.go.dev/crypto/tls "Go crypto/tls package"
[4]: https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html "systemd.exec execution environment configuration"
