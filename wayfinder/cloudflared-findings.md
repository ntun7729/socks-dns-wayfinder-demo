# Cloudflared compatibility findings

Cloudflare’s official Arbitrary TCP guidance states that Cloudflare Tunnel can publish arbitrary TCP services, but the end-user side must also run the `cloudflared` daemon and use Cloudflare Access authentication. The documented requirements include a Cloudflare account, a site active on Cloudflare, and `cloudflared` installed on both the origin host and client machine. This means `cloudflared` can carry the existing s5dns TLS service, but it is not a transparent replacement for the local SOCKS5 client.

The official configuration-file guidance shows that a TCP origin is represented as an ingress service such as `tcp://localhost:8443`. Tunnel ingress files must end with a catch-all rule, usually `service: http_status:404`. Locally managed tunnels use a tunnel UUID and credentials file; remotely managed tunnels can instead use a tunnel token. An account-bound tunnel cannot be created or started end-to-end without the user’s Cloudflare authorization or tunnel token.

The compatible design for this project is:

| Component | Role |
| --- | --- |
| s5dns server | Listens on loopback TCP `127.0.0.1:8443` and keeps TLS/token authentication enabled. |
| cloudflared on the Ubuntu host | Publishes `tcp://127.0.0.1:8443` through a Cloudflare Tunnel. |
| cloudflared on the client host | Runs `cloudflared access tcp` and exposes a local TCP port. |
| s5dns client | Connects to that local forwarded port and continues exposing SOCKS5/DNS locally. |

Because Cloudflare Access authenticates the outer TCP connection and s5dns authenticates inside it, the setup has two independent authentication layers. The s5dns certificate verification and shared token should not be removed.

## References

[1]: https://developers.cloudflare.com/cloudflare-one/access-controls/applications/non-http/cloudflared-authentication/arbitrary-tcp/ "Cloudflare One — Arbitrary TCP"
[2]: https://developers.cloudflare.com/tunnel/advanced/local-management/configuration-file/ "Cloudflare Tunnel — Configuration file"

Cloudflare’s package repository lists Ubuntu 24.04 Noble as supported and recommends the signed APT repository using `/usr/share/keyrings/cloudflare-main.gpg`, followed by `sudo apt-get update && sudo apt-get install cloudflared`.[3] The official local-tunnel guide then uses `cloudflared tunnel login` to create an account certificate, `cloudflared tunnel create <NAME>` to create a tunnel and credentials file, an ingress configuration with the TCP origin, `cloudflared tunnel route dns <UUID or NAME> <hostname>` to create the DNS route, and `cloudflared tunnel run <UUID or NAME>` to start it.[4]

[3]: https://pkg.cloudflare.com/ "Cloudflare package repository"
[4]: https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/local-management/create-local-tunnel/ "Cloudflare One — Create a locally-managed tunnel"
