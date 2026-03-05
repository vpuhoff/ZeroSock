# ZeroSock Stress Test Results

This document summarizes stress and load tests executed during validation of ZeroSock.

## Test Environment Notes

- OS used during tests: Windows (sections 1–4, 5.1); **Linux** (sections 5.2 and 5.3 — 500 VU and 2000 VU k6 runs).
- **k6 500 VU / 2000 VU (5.2, 5.3):** Ubuntu 22, 10 virtual CPUs (vCPU) in Hyper-V; host CPU: Intel Core i9-10900F @ 2.80 GHz.
- Proxy endpoint: `127.0.0.1:1080`
- Metrics endpoint: `127.0.0.1:9101`
- Linux-specific zero-copy (`splice`) applies to the Linux k6 runs (5.2, 5.3); it was not measured in the Windows environment.

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

### 5.2 k6 Results — 500 VUs / 120s (Linux)

Source: `k6_500vu.log` (run: `VUS_LIST=500 DURATION_500=120s bash scripts/run_k6_linux.sh`).

- **Profile:** 500 VUs, 2m duration, 30s graceful stop.
- **Target:** `http://load.local/payload_100mb.bin` via `socks5h://127.0.0.1:1080`.
- **Checks:** 987 total, **100% succeeded** (0 failed), ✓ status is 200.
- **HTTP:**
  - `http_reqs`: **987** (6.58/s)
  - `http_req_failed`: **0.00%**
  - `http_req_duration`: avg **1m11s**, min 28.43s, med 1m10s, max 1m50s, p(90) 1m23s, p(95) 1m27s
- **Iterations:** 987 complete, 17 interrupted (during graceful stop).
- **Network:** data received **105 GB** at ~**700 MB/s**; data sent 93 kB.
- **Artifacts:** `k6_runs/20260305_162310/` (k6_500.log, metrics before/after).

Interpretation: With 120s duration, almost all 500 VUs completed at least one full 100 MB download; only 17 iterations were still in flight when the test ended. Latency is dominated by transfer time for 100 MB over the proxy.

### 5.3 k6 Results — 2000 VUs / 10 min, 10 MB payload (Linux)

Source: `k6_runs/20260305_165318/` (run with **10 MB** file to avoid timeout tuning: `VUS_LIST=2000 DURATION_2000=600s`, target e.g. `payload_10mb.bin`).

- **Profile:** 2000 VUs, 10m duration, 30s graceful stop.
- **Target:** `http://load.local/payload_10mb.bin` (10 MB) via `socks5h://127.0.0.1:1080`.
- **Checks:** 59 952 total, **96.70% succeeded** (57 977), 3.29% failed (1 975) — ✗ status is 200 for the failed share.
- **HTTP:**
  - `http_reqs`: **59 952** (~99.8/s)
  - `http_req_failed`: **3.29%** (1 975)
  - `http_req_duration`: avg **19.92s**, min 140ms, med 19.31s, max 58.71s, p(90) 29.2s, p(95) 32.23s
- **Iterations:** 59 952 complete, 0 interrupted.
- **Network:** data received **618 GB** at ~**1.0 GB/s**; data sent 5.0 MB.
- **Proxy metrics (after run):**
  - `zerosock_requests_backend_total{host="load.local",result="success"}`: 6 006
  - `zerosock_requests_backend_total{host="load.local",result="relay_error"}`: 1 997
  - `zerosock_connection_errors_total{stage="relay"}`: 1 997
- **Artifacts:** `k6_runs/20260305_165318/k6_2000.log`, `metrics_k6_2000_before.txt`, `metrics_k6_2000_after.txt`.

Interpretation: Payload reduced to 10 MB so the run could complete without raising timeouts. Throughput ~100 req/s and ~1 GB/s; ~3.3% of requests hit relay errors (timeouts/closed), consistent with proxy-side `relay_error` count. For stricter success rate at 2000 VU, increase `timeouts.idle_ms` and nginx `send_timeout`, or keep 10 MB for load/throughput validation.

### 5.4 Comparison vs previous behavior

- Section 5.1: baseline 50 VU / 20s.
- Section 5.2: Linux 500 VU / 120s, 100 MB payload, 100% success.
- Section 5.3: Linux 2000 VU / 10 min, 10 MB payload, 96.7% success; relay errors ~3.3%.

## 6) Known Test Source Caveats

- Sections 5.2 and 5.3 use Linux runs; zero-copy (`splice`) applies there.
- 2000 VU with 10 MB payload is documented in 5.3; 100 MB at 2000 VU would require higher timeouts.

