# ZeroSock

High-performance SOCKS5 L4 router in Go.

## Features

- Custom minimal SOCKS5 parser (no third-party SOCKS library)
- Supported auth methods: `NO AUTH` only
- Supported commands: `CONNECT` only
- Supported ATYP: IPv4 (`0x01`) and FQDN (`0x03`)
- Unsupported by design: IPv6 (`0x04`), `BIND`, `UDP ASSOCIATE`
- Host-based routing by FQDN from local YAML config
- Round-robin load balancing across backend IP pools
- TCP healthchecks with `Alive/Dead` backend rotation
- Strict zero-copy-compatible relay (`io.Copy` between raw `*net.TCPConn`, no `bufio.Reader` in data plane)
- Built-in Prometheus metrics exporter (`/metrics`)
- Graceful shutdown (`SIGINT`, `SIGTERM`) with configurable grace period

## Requirements

- Go 1.21+
- Linux for kernel-level `splice` optimization behind `io.Copy`

## Quick start

1. Copy config template:
   - `cp config.example.yaml config.yaml`
2. Adjust `routes` to your backend IPs.
3. Run:
   - `go run ./cmd/zerosock -config config.yaml`

4. Scrape metrics:
   - `curl http://127.0.0.1:9090/metrics`

## Config

Example structure (full template in `config.example.yaml`):

```yaml
server:
  listen_addr: "0.0.0.0:1080"

metrics:
  enabled: true
  listen_addr: "127.0.0.1:9090"

healthcheck:
  interval_ms: 5000
  timeout_ms: 2000

tcp:
  keepalive_ms: 30000

timeouts:
  dial_ms: 4000
  shutdown_grace_period_ms: 10000

routes:
  "api.internal":
    - "10.0.1.10:8080"
    - "10.0.1.11:8080"
```

## Behavior

- If destination host from SOCKS5 request exists in `routes`, ZeroSock picks an `Alive` backend via round robin and dials backend IP directly.
- For IPv4 SOCKS requests, routing key is the IPv4 string (for example `203.0.113.10`) and must exist in `routes` if used.
- If host does not exist in config, request is denied.
- If all backends for host are dead, request is denied until healthcheck marks at least one backend alive.

## Metrics (Prometheus)

Exported base metrics include:

- `zerosock_connections_total`, `zerosock_connections_active`
- `zerosock_handshake_latency_seconds`
- `zerosock_requests_total{atyp=...}`
- `zerosock_route_failures_total{host,reason}`
- `zerosock_backend_dial_latency_seconds`, `zerosock_backend_dial_failures_total{host,reason}`
- `zerosock_relay_bytes_total{direction=...}`, `zerosock_relay_session_bytes{direction=...}`
- `zerosock_session_duration_seconds`
- `zerosock_healthchecks_total{host,backend,result}`, `zerosock_backend_alive{host,backend}`
