# Multiplexing design notes

The next transport version keeps the existing TLS 1.3 and shared-token authentication boundary but changes the client/server data channel from one TLS connection per SOCKS request to one persistent authenticated TLS connection carrying multiple logical streams. The design borrows the useful parts of established multiplexed transports without claiming to implement HTTP/2 or QUIC.

Each frame is length-prefixed and contains a frame type, a stream ID, and a bounded payload. Client-initiated stream IDs are odd, monotonically increasing values. The first frame for a stream is `OPEN`, carrying the SOCKS target. The server replies with `OPEN_OK` or `OPEN_ERR`. Application bytes use `DATA`; each side can send `FIN` for half-close and `RESET` to abort a logical stream. A `PING`/`PONG` pair keeps the session observable and a `GOAWAY` frame stops new streams during shutdown.

The client has one writer goroutine for the TLS connection so frame bytes cannot interleave. The reader goroutine demultiplexes frames into per-stream channels. Per-stream channels are bounded, and the implementation stops reading from a stream when its local SOCKS socket is not consuming; the connection-level reader remains active so one stalled stream does not block control frames or unrelated streams. A modest maximum frame payload and maximum concurrent-stream limit bound memory and denial-of-service exposure.

A lost persistent session fails existing logical streams and causes the next SOCKS request to establish a fresh authenticated session. Automatic replay of an already-open TCP stream is deliberately not attempted because replaying arbitrary application bytes is unsafe. DNS remains on its explicit request-per-TLS path initially; multiplexed DNS request IDs can be added later without coupling UDP request lifetimes to TCP stream shutdown.

This shape follows the general properties described by HTTP/2: multiple concurrent exchanges on one connection, explicit stream identity, frame-based interleaving, per-stream error handling, and flow-control boundaries.[1] QUIC similarly defines ordered bidirectional streams, cancellation, and credit-based stream creation, but QUIC is not being introduced in this iteration.[2]

## References

[1]: https://datatracker.ietf.org/doc/html/rfc7540 "RFC 7540 — HTTP/2"
[2]: https://datatracker.ietf.org/doc/html/rfc9000 "RFC 9000 — QUIC"
