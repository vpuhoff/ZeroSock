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
  max_connections: 4096
  max_inflight_dials: 2048

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
  read_ms: 10000
  write_ms: 10000
  idle_ms: 300000
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
- `server.max_connections` limits simultaneously handled client sessions.
- `server.max_inflight_dials` limits concurrent backend dial attempts.
- `timeouts.read_ms`, `timeouts.write_ms`, and `timeouts.idle_ms` control socket deadlines.

## Metrics (Prometheus)

Exported base metrics include:

- `zerosock_connections_total`, `zerosock_connections_active`
- `zerosock_handshake_latency_seconds`
- `zerosock_requests_total{atyp=...}`
- `zerosock_requests_backend_total{host,backend,result}`
- `zerosock_route_failures_total{host,reason}`
- `zerosock_backend_dial_latency_seconds`, `zerosock_backend_dial_failures_total{host,reason}`
- `zerosock_relay_bytes_total{direction=...}`, `zerosock_relay_session_bytes{direction=...}`
- `zerosock_session_duration_seconds`
- `zerosock_healthchecks_total{host,backend,result}`, `zerosock_backend_alive{host,backend}`

## Performance & Benchmarks

ZeroSock has been rigorously stress-tested to validate its throughput, memory stability, and concurrency limits. Built for maximum efficiency, it operates near the physical limits of the OS network stack, offering L4 performance comparable to industry standards like HAProxy.

### Key Highlights

- **Zero-Copy Routing:** On Linux environments, ZeroSock utilizes the `splice()` system call. This allows data to be transferred directly between sockets within the kernel space, completely bypassing user-space overhead.
- **High Throughput:** In loopback stress tests (10 vCPUs, k6, Nginx backend), ZeroSock successfully processed **~1 GB/s (8 Gbps)** of payload data without becoming the CPU bottleneck.
- **Ultra-Low Memory Footprint:** Memory consumption scales linearly and predictably. Under a sustained load of **2000 concurrent connections**, the proxy consumed only **~34 MB of RAM** (averaging a mere ~17 KB per connection), with zero memory leaks after the connections were closed.
- **Minimal Latency:** Functional tests (e.g., pulling Docker images via Skopeo) showed that ZeroSock adds an almost imperceptible **+1.9% (+135 ms)** overhead compared to direct, unproxied connections.

### Concurrency Limits & Scaling

- **Up to 500 Concurrent Users:** Flawless stability. Achieved a **100% success rate** with 500 VUs downloading 100 MB payloads simultaneously over 2 minutes.
- **Extreme Load (2000+ Concurrent Users):** Maintained ~1 GB/s overall throughput with a **96.7% success rate**. Under extreme CPU contention, ~3.3% of connections may experience relay timeouts. For environments expecting 2000+ simultaneous active transfers, tuning proxy limits (`timeouts.idle_ms`) and backend server timeouts is recommended.

### The Verdict

ZeroSock acts as a highly optimized "scalpel" for SOCKS5 proxying. By stripping away heavy L7 features, it delivers raw, kernel-level networking performance and an exceptionally small resource footprint.

For full test methodology, metrics, and environment details, see [STRESS_TEST_RESULTS.md](STRESS_TEST_RESULTS.md).
