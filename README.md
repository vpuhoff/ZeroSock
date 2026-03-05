# ZeroSock

High-performance SOCKS5 L4 router in Go.

## Features

- SOCKS5 input (`NO AUTH`, `CONNECT`) via `github.com/armon/go-socks5`
- Host-based routing by FQDN from local YAML config
- Round-robin load balancing across backend IP pools
- TCP healthchecks with `Alive/Dead` backend rotation
- Zero-copy-friendly data path (`io.Copy` on raw TCP connections, no custom wrappers)
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

## Config

Example structure (full template in `config.example.yaml`):

```yaml
server:
  listen_addr: "0.0.0.0:1080"

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
- If host does not exist in config, request is denied.
- If all backends for host are dead, request is denied until healthcheck marks at least one backend alive.
