package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"zerosock/internal/config"
)

func TestApplyReloadPolicyKeepsNonReloadableSettings(t *testing.T) {
	current := &config.RuntimeConfig{
		ListenAddr:        "127.0.0.1:1080",
		MetricsEnabled:    true,
		MetricsListenAddr: "127.0.0.1:9101",
		MaxConnections:    10,
		MaxInflightDials:  20,
		DialTimeout:       time.Second,
		ReadTimeout:       2 * time.Second,
		WriteTimeout:      3 * time.Second,
		IdleTimeout:       4 * time.Second,
		TCPKeepAlive:      5 * time.Second,
		ShutdownGrace:     6 * time.Second,
	}
	next := &config.RuntimeConfig{
		ListenAddr:        "127.0.0.1:2080",
		MetricsEnabled:    false,
		MetricsListenAddr: "127.0.0.1:9201",
		MaxConnections:    11,
		MaxInflightDials:  21,
		DialTimeout:       7 * time.Second,
		ReadTimeout:       8 * time.Second,
		WriteTimeout:      9 * time.Second,
		IdleTimeout:       10 * time.Second,
		TCPKeepAlive:      11 * time.Second,
		ShutdownGrace:     12 * time.Second,
	}

	applied, warnings := applyReloadPolicy(current, next)

	if applied.ListenAddr != current.ListenAddr ||
		applied.MetricsEnabled != current.MetricsEnabled ||
		applied.MetricsListenAddr != current.MetricsListenAddr ||
		applied.MaxConnections != current.MaxConnections ||
		applied.MaxInflightDials != current.MaxInflightDials ||
		applied.DialTimeout != current.DialTimeout ||
		applied.ReadTimeout != current.ReadTimeout ||
		applied.WriteTimeout != current.WriteTimeout ||
		applied.IdleTimeout != current.IdleTimeout ||
		applied.TCPKeepAlive != current.TCPKeepAlive {
		t.Fatalf("non-reloadable settings were unexpectedly changed: %+v", applied)
	}
	if applied.ShutdownGrace != next.ShutdownGrace {
		t.Fatalf("ShutdownGrace = %s; want %s", applied.ShutdownGrace, next.ShutdownGrace)
	}
	if len(warnings) == 0 {
		t.Fatal("expected warnings for ignored runtime settings")
	}
}

func TestRunReloadsRoutesOnSIGHUP(t *testing.T) {
	backendA := startTaggedBackendForRun(t, "A:")
	defer backendA.Close()
	backendB := startTaggedBackendForRun(t, "B:")
	defer backendB.Close()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	listenAddr := freeAddr(t)
	writeHotReloadConfig(t, configPath, listenAddr, "", false, backendA.Addr().String())

	sigCh := make(chan os.Signal, 2)
	errCh := make(chan error, 1)
	go func() {
		errCh <- run(configPath, sigCh, log.New(io.Discard, "", 0))
	}()

	waitForDialSuccess(t, listenAddr)
	if got := performReloadRequest(t, listenAddr, "api.internal", "ping"); got != "A:ping" {
		t.Fatalf("before reload got %q; want %q", got, "A:ping")
	}

	writeHotReloadConfig(t, configPath, listenAddr, "", false, backendB.Addr().String())
	sigCh <- syscall.SIGHUP

	if got := waitForReloadResponse(t, listenAddr, "api.internal", "pong", "B:pong"); got != "B:pong" {
		t.Fatalf("after reload got %q; want %q", got, "B:pong")
	}

	sigCh <- syscall.SIGTERM
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run() did not shut down in time")
	}
}

func TestRunReloadKeepsOldConfigOnInvalidFile(t *testing.T) {
	backendA := startTaggedBackendForRun(t, "A:")
	defer backendA.Close()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	listenAddr := freeAddr(t)
	writeHotReloadConfig(t, configPath, listenAddr, "", false, backendA.Addr().String())

	sigCh := make(chan os.Signal, 2)
	errCh := make(chan error, 1)
	go func() {
		errCh <- run(configPath, sigCh, log.New(io.Discard, "", 0))
	}()

	waitForDialSuccess(t, listenAddr)
	if got := performReloadRequest(t, listenAddr, "api.internal", "ping"); got != "A:ping" {
		t.Fatalf("before reload got %q; want %q", got, "A:ping")
	}

	if err := os.WriteFile(configPath, []byte("not: [valid"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	sigCh <- syscall.SIGHUP

	if got := waitForReloadResponse(t, listenAddr, "api.internal", "still", "A:still"); got != "A:still" {
		t.Fatalf("after invalid reload got %q; want %q", got, "A:still")
	}

	sigCh <- syscall.SIGTERM
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run() did not shut down in time")
	}
}

func TestRunReloadAppliesRoutesButKeepsListenAddresses(t *testing.T) {
	backendA := startTaggedBackendForRun(t, "A:")
	defer backendA.Close()
	backendB := startTaggedBackendForRun(t, "B:")
	defer backendB.Close()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	listenAddr := freeAddr(t)
	metricsAddr := freeAddr(t)
	writeHotReloadConfig(t, configPath, listenAddr, metricsAddr, true, backendA.Addr().String())

	sigCh := make(chan os.Signal, 2)
	errCh := make(chan error, 1)
	go func() {
		errCh <- run(configPath, sigCh, log.New(io.Discard, "", 0))
	}()

	waitForDialSuccess(t, listenAddr)
	waitForHTTPMetrics(t, metricsAddr)

	newListenAddr := freeAddr(t)
	newMetricsAddr := freeAddr(t)
	writeHotReloadConfig(t, configPath, newListenAddr, newMetricsAddr, true, backendB.Addr().String())
	sigCh <- syscall.SIGHUP

	if got := waitForReloadResponse(t, listenAddr, "api.internal", "swap", "B:swap"); got != "B:swap" {
		t.Fatalf("after reload on old listen addr got %q; want %q", got, "B:swap")
	}
	if _, err := net.DialTimeout("tcp", newListenAddr, 100*time.Millisecond); err == nil {
		t.Fatalf("new listen addr %s unexpectedly accepts connections", newListenAddr)
	}
	waitForHTTPMetrics(t, metricsAddr)
	if _, err := http.Get("http://" + newMetricsAddr + "/metrics"); err == nil {
		t.Fatalf("new metrics addr %s unexpectedly serves metrics", newMetricsAddr)
	}

	sigCh <- syscall.SIGTERM
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run() did not shut down in time")
	}
}

func writeHotReloadConfig(t *testing.T, path, listenAddr, metricsAddr string, metricsEnabled bool, backendAddr string) {
	t.Helper()

	metricsBlock := "enabled: false"
	if metricsEnabled {
		metricsBlock = fmt.Sprintf("enabled: true\n  listen_addr: %q", metricsAddr)
	}

	content := "server:\n" +
		fmt.Sprintf("  listen_addr: %q\n", listenAddr) +
		"metrics:\n" +
		"  " + metricsBlock + "\n" +
		"healthcheck:\n" +
		"  interval_ms: 100\n" +
		"  timeout_ms: 50\n" +
		"tcp:\n" +
		"  keepalive_ms: 1000\n" +
		"timeouts:\n" +
		"  dial_ms: 200\n" +
		"  read_ms: 1000\n" +
		"  write_ms: 1000\n" +
		"  idle_ms: 5000\n" +
		"  shutdown_grace_period_ms: 100\n" +
		"backends:\n" +
		"  api:\n" +
		"    addresses:\n" +
		fmt.Sprintf("      - %q\n", backendAddr) +
		"    healthcheck: {}\n" +
		"routes:\n" +
		"  \"api.internal\": \"api\"\n"

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func startTaggedBackendForRun(t *testing.T, prefix string) net.Listener {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				buf := make([]byte, 1024)
				for {
					n, err := conn.Read(buf)
					if n > 0 {
						_, _ = conn.Write([]byte(prefix + string(buf[:n])))
					}
					if err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	return ln
}

func performReloadRequest(t *testing.T, listenAddr, host, payload string) string {
	t.Helper()

	conn, err := net.Dial("tcp", listenAddr)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read greeting reply: %v", err)
	}
	if !strings.HasPrefix(string(reply), "\x05\x00") {
		t.Fatalf("unexpected greeting reply: %v", reply)
	}

	req := make([]byte, 0, 4+1+len(host)+2)
	req = append(req, 0x05, 0x01, 0x00, 0x03, byte(len(host)))
	req = append(req, []byte(host)...)
	req = append(req, 0x01, 0xbb)
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("write request: %v", err)
	}

	connectReply := make([]byte, 10)
	if _, err := io.ReadFull(conn, connectReply); err != nil {
		t.Fatalf("read connect reply: %v", err)
	}
	if connectReply[1] != 0x00 {
		t.Fatalf("unexpected connect reply code: %d", connectReply[1])
	}

	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	buf := make([]byte, len(payload)+2)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}
	return string(buf[:n])
}

func waitForReloadResponse(t *testing.T, listenAddr, host, payload, want string) string {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		last = performReloadRequest(t, listenAddr, host, payload)
		if last == want {
			return last
		}
		time.Sleep(25 * time.Millisecond)
	}
	return last
}

func waitForHTTPMetrics(t *testing.T, addr string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/metrics")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("metrics endpoint %s did not become ready", addr)
}
