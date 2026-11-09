# swagCore proxy data plane

The proxy data plane is separate from the JSON control channel.

## Components

- The node agent keeps a second outbound WebSocket connection at `/proxy/agent`.
- The connection carries Yamux-multiplexed binary streams. It does not serialize user payloads as JSON.
- Each TCP stream is opened only when a client asks the gateway for a connection.
- SOCKS5 UDP uses short-lived multiplexed streams, one per datagram.
- The agent resolves destination names on the egress node and rejects private, loopback, link-local, multicast, documentation and other non-public IP ranges before dialing. This is intended to reduce SSRF and DNS-rebinding risk.

## Gateway configuration

The gateway is disabled unless both environment variables are set:

- `PROXY_USER`
- `PROXY_PASSWORD`

Optional:
- `PROXY_LISTEN` (default `127.0.0.1:8182`)

Keep the listener on loopback unless a TLS-terminating ingress is configured. Do not expose port 8182 directly to the internet. HTTP Basic authentication and SOCKS5 username/password authentication are enabled; credentials must only travel over a trusted local connection or TLS.

After setting variables in `/opt/swagcore/env`, restart `swagcore-server.service`.

## Client protocols currently implemented

- HTTP forward proxy
- HTTPS via HTTP CONNECT
- SOCKS5 TCP CONNECT
- SOCKS5 UDP ASSOCIATE (unfragmented datagrams)

The gateway currently selects among connected proxy-capable nodes round-robin. No traffic is sent to a node while no client stream/datagram is active. The agent's separate tunnel does keep a small Yamux keepalive running, in addition to the existing control-channel heartbeat.

## Operational note

The control server can run with the gateway disabled. The node data tunnel endpoint remains available so agents can establish their idle tunnel. The gateway is intentionally not exposed on a public port by default. Public ingress, user management, per-user quotas, connection caps and detailed traffic accounting are follow-up hardening tasks; do not treat this initial gateway as a finished multi-tenant commercial proxy service.