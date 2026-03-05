# ZeroSock Stress Test Results

This document summarizes stress and load tests executed during validation of ZeroSock.

## Test Environment Notes

- OS used during tests: Windows
- Proxy endpoint: `127.0.0.1:1080`
- Metrics endpoint: `127.0.0.1:9101`
- Important caveat: Linux-specific zero-copy syscall validation (`splice`) was not measured in this environment.

## 1) Git/Skopeo Functional Proxy Checks

### Git via SOCKS5

- Command pattern: `git -c http.proxy=socks5h://127.0.0.1:1080 ...`
- Result: successful clone/ls-remote via proxy.

### Skopeo via SOCKS5 (Docker fallback)

- Since local `skopeo` was unavailable, tests used:
  - `quay.io/skopeo/stable:latest`
  - `HTTPS_PROXY=socks5h://host.docker.internal:1080`
- Result: successful `skopeo inspect docker://quay.io/libpod/alpine`.

## 2) Skopeo Performance Comparison (Direct vs Proxy)

Workload:
- `skopeo copy docker://quay.io/prometheus/prometheus:latest dir:/out`

Results:
- Direct: `7.234 s`
- Through ZeroSock proxy: `7.369 s`
- Delta: `+0.135 s` (~`+1.9%`)

Proxy-confirmation metrics (proxy run):
- `zerosock_connections_total`: `+15`
- `zerosock_requests_total{atyp="fqdn"}`: `+15`
- `zerosock_relay_bytes_total{direction="backend_to_client"}`: `+144000129`
- No route-not-found failures for `quay.io`.

## 3) Large File Multi-Stream Load via curl

### 3.1 Four 1GB streams (Tele2)

Workload:
- 4 parallel streams via proxy:
  - `http://speedtest.tele2.net/1GB.zip`

Aggregate result:
- Total wall time: `89.791 s`

Per-stream results:
- `tele2-1`: `88.266 s`, `1073741824` bytes, `12164790` B/s
- `tele2-2`: `72.742 s`, `1073741824` bytes, `14761036` B/s
- `tele2-3`: `74.554 s`, `1073741824` bytes, `14402113` B/s
- `tele2-4`: `89.622 s`, `1073741824` bytes, `11980848` B/s

Metrics delta (before/after):
- `zerosock_requests_backend_total{host="speedtest.tele2.net",backend="90.130.70.73:80",result="success"}`: `+4`
- `zerosock_relay_bytes_total{direction="backend_to_client"}`: `+4294968292` (~4.0 GiB)
- `zerosock_connection_errors_total{stage="relay"}`: `+0`

### 3.2 Fifty 50MB streams (Tele2)

Workload:
- 50 parallel streams via proxy:
  - `http://speedtest.tele2.net/50MB.zip`

Aggregate result:
- Total wall time: `55.843 s`
- Successful streams: `50/50`
- Failed streams: `0`
- Average stream duration: `39.149 s`
- Total downloaded bytes: `2621440000` (50 × 50MB)

Metrics delta (before/after):
- `zerosock_requests_backend_total{host="speedtest.tele2.net",backend="90.130.70.73:80",result="success"}`: `+50`
- `zerosock_relay_bytes_total{direction="backend_to_client"}`: `+2621452300`
- `zerosock_connection_errors_total{stage="relay"}`: `+0`

Note:
- Additional background route-not-found attempts were observed from host environment traffic and are unrelated to Tele2 load streams.

## 4) Memory Stress (Open Connection Scaling)

Tool:
- `scripts/conn_mem_stress.py`

### 4.1 Levels: 10, 100, 1000

Run:
- `--levels 10,100,1000 --hold-seconds 8 --sample-interval-ms 500`

Summary:
- RSS min/avg/max: `12.27 / 16.97 / 23.25 MB`

Stage averages:
- baseline: `12.27 MB`
- hold_10: `12.37 MB`
- hold_100: `13.27 MB`
- hold_1000: `22.78 MB`
- after_close: `23.25 MB`

### 4.2 Levels: 100, 500, 2000

Run:
- `--levels 100,500,2000 --hold-seconds 8 --sample-interval-ms 500`

Summary:
- RSS min/avg/max: `12.28 / 22.62 / 33.90 MB`

Stage averages:
- baseline: `12.281 MB`
- hold_100: `13.247 MB`
- hold_500: `17.525 MB`
- hold_2000: `33.467 MB`
- after_close: `33.902 MB`

Estimated memory growth slope:
- `0 -> 100`: `0.966 MB / 100 conn`
- `100 -> 500`: `1.070 MB / 100 conn`
- `500 -> 2000`: `1.063 MB / 100 conn`
- overall `0 -> 2000`: `1.059 MB / 100 conn`

Interpretation:
- Memory growth is near-linear in tested ranges.

## 5) Hardening Rollout and Re-run (Nginx + ZeroSock)

Implemented hardening:
- Nginx high-concurrency config applied from `loadtest/nginx.conf`:
  - `worker_processes auto`
  - `worker_rlimit_nofile 262144`
  - `worker_connections 8192`, `multi_accept on`
  - `listen 80 backlog=8192 reuseport`
- ZeroSock controls added:
  - `server.max_connections`
  - `server.max_inflight_dials`
  - `timeouts.read_ms`, `timeouts.write_ms`, `timeouts.idle_ms`

Validation artifacts:
- `k6_50.log`, `k6_500.log`, `k6_2000.log`
- `metrics_k6_50_before.txt`, `metrics_k6_50_after.txt`
- `metrics_k6_500_before.txt`, `metrics_k6_500_after.txt`
- `metrics_k6_2000_before.txt`, `metrics_k6_2000_after.txt`

### 5.1 k6 Results (after hardening)

- **50 VUs / 20s**
  - `http_reqs`: `5`
  - `http_req_failed`: `0.00% (0/5)`
  - `http_req_duration avg`: `35.67s`
  - Proxy deltas:
    - `zerosock_requests_backend_total{host="load.local",backend="127.0.0.1:8088",result="success"}`: `+43`
    - `zerosock_connection_errors_total{stage="backend_dial"}`: `+1`

### 5.2 Comparison vs previous behavior

- High-load runs `500/2000` are intentionally excluded from this report revision.
- These scenarios will be rerun in a clean Linux environment and added as a separate update.
- Current section keeps only the successful and reproducible baseline result.

## 6) Known Test Source Caveats

- Final high-load conclusions are postponed until Linux rerun is complete.

