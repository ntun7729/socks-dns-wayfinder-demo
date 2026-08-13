## Destination

A decision-ready and implementable specification for a **root-run Ubuntu user-space SOCKS5 plus DNS tunnel** that does not require TUN/TAP and does not depend on a separate control server. The destination includes a small reference implementation, systemd packaging, and a repeatable local acceptance test.

## Notes

This is an engineering demo of the Wayfinder workflow. The tracker is GitHub Issues in this repository. The implementation may use a single binary with client and server roles, an authenticated encrypted stream transport, explicit SOCKS5 CONNECT handling, and DNS forwarding without attempting transparent system-wide routing. No TUN/TAP, raw packet interception, or unrelated stealth/evasion behavior is in scope.

## Decisions so far

## Not yet specified

- The transport and cryptographic boundary, including whether the first prototype uses TLS, Noise, or a standard secure tunnel library.
- The exact SOCKS5 authentication, stream framing, connection lifecycle, and error semantics.
- The DNS behavior: SOCKS-resolved names versus an explicit DNS forwarding endpoint, UDP/TCP policy, and local resolver integration.
- The exposure model, least-privilege posture, secret handling, and systemd hardening for root deployment.
- The acceptance criteria, interoperability checks, and limits of a user-space proxy that is not a transparent VPN.

## Out of scope

- Transparent system-wide IP VPN routing; it requires TUN/TAP, kernel packet interception, or a separate transparent proxy layer and is intentionally excluded.
- A centralized control plane, account-management service, relay fleet, traffic obfuscation, censorship circumvention, or stealth features.
- Production-grade multi-tenant operation, high availability, or third-party cloud provisioning.
