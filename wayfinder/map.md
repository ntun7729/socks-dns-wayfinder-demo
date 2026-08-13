## Destination

A decision-ready and implementable specification for a **root-run Ubuntu user-space SOCKS5 plus DNS tunnel** that does not require TUN/TAP and does not depend on a separate control server. The destination includes a small reference implementation, systemd packaging, and a repeatable local acceptance test.

## Notes

This is an engineering demo of the Wayfinder workflow. The tracker is GitHub Issues in this repository. The implementation uses a single binary with client and server roles, an authenticated encrypted stream transport, explicit SOCKS5 CONNECT handling, and DNS forwarding without attempting transparent system-wide routing. No TUN/TAP, raw packet interception, or unrelated stealth/evasion behavior is in scope.

## Decisions so far

- [Decide secure transport and cryptographic boundary](https://github.com/ntun7729/socks-dns-wayfinder-demo/issues/2) — Use TLS 1.3 per request with a private CA, configured server-name verification, and a shared bearer token; do not add a separate control plane.

## Not yet specified

- The exact acceptance criteria and evidence for the SOCKS5/DNS behavior, authentication rejection, TLS verification, and no-TUN/TAP boundary remain in [Define acceptance tests and scope-boundary evidence](https://github.com/ntun7729/socks-dns-wayfinder-demo/issues/5).
- The production-level Ubuntu service and secret policy remains in [Choose Ubuntu service, secret, and privilege model](https://github.com/ntun7729/socks-dns-wayfinder-demo/issues/4).
- The broader SOCKS5 and DNS behavior decision remains in [Define SOCKS5 commands and DNS forwarding behavior](https://github.com/ntun7729/socks-dns-wayfinder-demo/issues/3).

## Out of scope

- Transparent system-wide IP VPN routing; it requires TUN/TAP, kernel packet interception, or a separate transparent proxy layer and is intentionally excluded.
- A centralized control plane, account-management service, relay fleet, traffic obfuscation, censorship circumvention, or stealth features.
- Production-grade multi-tenant operation, high availability, or third-party cloud provisioning.
