## Resolution

Use a single Go binary with `server` and `client` roles. The client opens one outbound TCP connection per SOCKS5 CONNECT or DNS request to the server’s TLS listener. This avoids a control plane and avoids multiplexing complexity in the first prototype; the outer connection is the per-request data channel.

The outer channel uses Go’s standard `crypto/tls` package with TLS 1.3 as the minimum version. The server presents a certificate signed by a small local CA. The client trusts only the configured CA file and verifies the configured server name, so the prototype does not rely on `InsecureSkipVerify`. After the TLS handshake, the client sends a fixed protocol magic, role byte, and a shared bearer token. The server rejects an invalid magic, unknown role, or wrong token before handling any request. The token is stored in a root-readable secret file or environment file with mode `0600` for the root-run demo and must be rotated manually if exposed.

The protocol is intentionally narrow. A SOCKS stream sends a `CONNECT` control record containing the SOCKS address type, address, and port; the server returns a status and bind address, then the same TLS connection carries raw bidirectional TCP bytes until either side closes. A DNS request sends the raw DNS wire message in a bounded record; the server forwards it over UDP to a configured upstream and returns the raw response. Each request has a five-second dial or DNS deadline and a four-kilobyte DNS size limit. No custom application-level encryption is added because TLS already supplies confidentiality and integrity; no claim is made that the bearer token protects against an attacker who has stolen it.

The local SOCKS listener binds to `127.0.0.1:1080` by default and supports RFC 1928 `CONNECT` with IPv4, IPv6, and domain-name targets. The local SOCKS handshake advertises no authentication because loopback binding is the default; the remote leg is authenticated by the TLS-protected token. `BIND` and `UDP ASSOCIATE` are rejected in this first version. Domain names are resolved by the remote server through its normal resolver, which is the intended DNS-leak reduction for applications that use SOCKS5 domain targets.

The local DNS listener is explicit rather than transparent: it binds to `127.0.0.1:5353` by default and accepts ordinary UDP DNS wire messages. It does not rewrite `/etc/resolv.conf`, alter routing, create a TUN/TAP device, or intercept packets. Users can query it directly with a DNS client such as `dig @127.0.0.1 -p 5353 example.com`.

The server systemd unit is recommended to run as a dedicated unprivileged user with `NoNewPrivileges`, `PrivateTmp`, `ProtectSystem=strict`, `ProtectHome`, `PrivateDevices`, restricted address families, an empty capability bounding set, and resource limits. A root shell can still launch the binary directly for demonstration. Root is needed for installation and for any protected port or file policy, not for the network operations themselves.

## References

[1]: https://datatracker.ietf.org/doc/html/rfc1928 "RFC 1928 — SOCKS Protocol Version 5"
[2]: https://datatracker.ietf.org/doc/html/rfc1035 "RFC 1035 — Domain Names: Implementation and Specification"
[3]: https://pkg.go.dev/crypto/tls "Go crypto/tls package"
[4]: https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html "systemd.exec execution environment configuration"
