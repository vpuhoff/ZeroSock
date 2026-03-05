#!/usr/bin/env python3
"""
Connection stress tool for ZeroSock memory profiling.

Opens many concurrent TCP connections to the proxy listener, holds them,
and samples proxy process RSS over time.
"""

from __future__ import annotations

import argparse
import csv
import os
import socket
import subprocess
import sys
import time
from dataclasses import dataclass
from typing import List, Optional


@dataclass
class Sample:
    ts: float
    stage: str
    target: int
    open_conn: int
    rss_mb: float


def parse_levels(raw: str) -> List[int]:
    levels = []
    for part in raw.split(","):
        part = part.strip()
        if not part:
            continue
        value = int(part)
        if value <= 0:
            raise ValueError("levels must be positive integers")
        levels.append(value)
    if not levels:
        raise ValueError("at least one level is required")
    return levels


def detect_pid_from_port(port: int) -> Optional[int]:
    if os.name == "nt":
        try:
            out = subprocess.check_output(
                ["cmd.exe", "/c", f"netstat -ano | findstr :{port}"], stderr=subprocess.STDOUT, text=True
            )
        except subprocess.CalledProcessError:
            return None
        for line in out.splitlines():
            line = line.strip()
            if "LISTENING" not in line:
                continue
            parts = line.split()
            if len(parts) >= 5:
                try:
                    return int(parts[-1])
                except ValueError:
                    continue
        return None

    try:
        out = subprocess.check_output(["sh", "-lc", f"ss -ltnp | grep ':{port} '"], text=True)
    except subprocess.CalledProcessError:
        return None
    for line in out.splitlines():
        marker = "pid="
        if marker not in line:
            continue
        tail = line.split(marker, 1)[1]
        pid_part = "".join(ch for ch in tail if ch.isdigit())
        if pid_part:
            return int(pid_part)
    return None


def get_rss_mb(pid: int) -> float:
    if os.name == "nt":
        cmd = [
            "powershell",
            "-NoProfile",
            "-Command",
            f"(Get-Process -Id {pid}).WorkingSet64",
        ]
        out = subprocess.check_output(cmd, text=True).strip()
        bytes_value = int(out)
        return bytes_value / (1024 * 1024)

    with open(f"/proc/{pid}/status", "r", encoding="utf-8", errors="ignore") as f:
        for line in f:
            if line.startswith("VmRSS:"):
                parts = line.split()
                kb = int(parts[1])
                return kb / 1024.0
    raise RuntimeError(f"VmRSS not found for pid={pid}")


def open_connection(host: str, port: int, timeout_sec: float) -> socket.socket:
    s = socket.create_connection((host, port), timeout=timeout_sec)
    s.setblocking(True)
    return s


def sample(samples: List[Sample], stage: str, target: int, open_conn: int, pid: int) -> None:
    try:
        rss = get_rss_mb(pid)
    except Exception:
        rss = float("nan")
    samples.append(Sample(time.time(), stage, target, open_conn, rss))


def write_csv(path: str, samples: List[Sample]) -> None:
    with open(path, "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(["timestamp", "stage", "target", "open_connections", "rss_mb"])
        for s in samples:
            w.writerow([f"{s.ts:.3f}", s.stage, s.target, s.open_conn, f"{s.rss_mb:.3f}"])


def summarize(samples: List[Sample]) -> str:
    valid = [s.rss_mb for s in samples if s.rss_mb == s.rss_mb]
    if not valid:
        return "RSS samples unavailable"
    return (
        f"RSS min/avg/max: {min(valid):.2f} / "
        f"{(sum(valid) / len(valid)):.2f} / {max(valid):.2f} MB"
    )


def main() -> int:
    parser = argparse.ArgumentParser(description="Open many connections and track proxy RSS.")
    parser.add_argument("--host", default="127.0.0.1", help="Proxy host to connect")
    parser.add_argument("--port", type=int, default=1080, help="Proxy port to connect")
    parser.add_argument("--levels", default="10,100,1000", help="Comma-separated connection targets")
    parser.add_argument("--hold-seconds", type=float, default=10.0, help="How long to hold each level")
    parser.add_argument("--connect-timeout", type=float, default=2.0, help="TCP connect timeout")
    parser.add_argument("--ramp-delay-ms", type=float, default=2.0, help="Delay between connection opens")
    parser.add_argument("--sample-interval-ms", type=float, default=500.0, help="RSS sample interval")
    parser.add_argument("--pid", type=int, default=0, help="Proxy PID (auto-detect if omitted)")
    parser.add_argument("--csv", default="conn_mem_stress.csv", help="Output CSV with samples")
    args = parser.parse_args()

    levels = parse_levels(args.levels)
    pid = args.pid or detect_pid_from_port(args.port)
    if not pid:
        print(f"[fail] cannot detect proxy PID on port {args.port}; use --pid", file=sys.stderr)
        return 1

    print(f"[info] target={args.host}:{args.port} pid={pid} levels={levels}")

    socks: List[socket.socket] = []
    samples: List[Sample] = []
    ramp_delay = args.ramp_delay_ms / 1000.0
    sample_interval = args.sample_interval_ms / 1000.0

    try:
        sample(samples, "baseline", 0, len(socks), pid)

        for level in levels:
            stage = f"ramp_to_{level}"
            print(f"[info] ramping to {level} connections")

            while len(socks) < level:
                try:
                    s = open_connection(args.host, args.port, args.connect_timeout)
                except Exception as e:
                    print(f"[warn] open failed at {len(socks)}->{level}: {e}", file=sys.stderr)
                    time.sleep(ramp_delay)
                    continue
                socks.append(s)
                if len(socks) % 25 == 0 or len(socks) == level:
                    sample(samples, stage, level, len(socks), pid)
                if ramp_delay > 0:
                    time.sleep(ramp_delay)

            print(f"[info] holding {level} connections for {args.hold_seconds:.1f}s")
            hold_end = time.time() + args.hold_seconds
            while time.time() < hold_end:
                sample(samples, f"hold_{level}", level, len(socks), pid)
                time.sleep(sample_interval)

        sample(samples, "before_close", levels[-1], len(socks), pid)
    finally:
        for s in socks:
            try:
                s.close()
            except Exception:
                pass
        time.sleep(0.5)
        sample(samples, "after_close", 0, 0, pid)

    write_csv(args.csv, samples)
    print(f"[ok] samples saved to {args.csv}")
    print(f"[ok] {summarize(samples)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
