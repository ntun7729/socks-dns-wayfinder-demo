## Question

What transport and cryptographic boundary should the reference implementation use to carry SOCKS5 streams and DNS requests between a client and server without TUN/TAP or a separate control plane?

The answer must identify a standard-library or narrowly scoped dependency option, explain server authentication and confidentiality, state whether certificate pinning or a shared secret is used, and define the minimum protocol versioning and replay/stream-lifecycle rules needed for a safe demo. It must also distinguish a proxy tunnel from a transparent VPN so the user-facing contract is not overstated.
