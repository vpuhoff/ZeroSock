package main

import (
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMainPassesConfigPathToRun(t *testing.T) {
	oldArgs := os.Args
	oldFlagSet := flag.CommandLine
	oldRunMain := runMain
	oldFatalf := fatalf
	oldLoggerFactory := newMainLogger
	defer func() {
		os.Args = oldArgs
		flag.CommandLine = oldFlagSet
		runMain = oldRunMain
		fatalf = oldFatalf
		newMainLogger = oldLoggerFactory
	}()

	os.Args = []string{"zerosock", "-config", "/tmp/test-config.yaml"}
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)

	newMainLogger = func() *log.Logger { return log.New(io.Discard, "", 0) }

	called := false
	runMain = func(configPath string, sigCh <-chan os.Signal, logger *log.Logger) error {
		called = true
		if configPath != "/tmp/test-config.yaml" {
			t.Fatalf("configPath = %q; want %q", configPath, "/tmp/test-config.yaml")
		}
		if sigCh == nil {
			t.Fatal("sigCh is nil")
		}
		if logger == nil {
			t.Fatal("logger is nil")
		}
		return nil
	}
	fatalf = func(logger *log.Logger, format string, args ...any) {
		t.Fatalf("fatalf should not be called on success")
	}

	main()

	if !called {
		t.Fatal("runMain was not called")
	}
}

func TestMainCallsFatalfOnRunError(t *testing.T) {
	oldArgs := os.Args
	oldFlagSet := flag.CommandLine
	oldRunMain := runMain
	oldFatalf := fatalf
	oldLoggerFactory := newMainLogger
	defer func() {
		os.Args = oldArgs
		flag.CommandLine = oldFlagSet
		runMain = oldRunMain
		fatalf = oldFatalf
		newMainLogger = oldLoggerFactory
	}()

	os.Args = []string{"zerosock"}
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)

	newMainLogger = func() *log.Logger { return log.New(io.Discard, "", 0) }

	wantErr := errors.New("boom")
	runMain = func(configPath string, sigCh <-chan os.Signal, logger *log.Logger) error {
		if configPath != "config.yaml" {
			t.Fatalf("configPath = %q; want default %q", configPath, "config.yaml")
		}
		return wantErr
	}

	fatalCalled := false
	fatalf = func(logger *log.Logger, format string, args ...any) {
		fatalCalled = true
		if len(args) != 1 || args[0] != wantErr {
			t.Fatalf("fatalf args = %#v; want %#v", args, []any{wantErr})
		}
	}

	main()

	if !fatalCalled {
		t.Fatal("fatalf was not called")
	}
}

func TestRunConfigError(t *testing.T) {
	err := run("/nonexistent/config.yaml", make(chan os.Signal), log.New(io.Discard, "", 0))
	if err == nil {
		t.Fatal("run() error = nil; want config error")
	}
	if !strings.Contains(err.Error(), "config error:") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunServeError(t *testing.T) {
	listener := listenTemp(t)
	defer listener.Close()

	configPath := writeConfigFile(t, listener.Addr().String(), "", false)
	err := run(configPath, make(chan os.Signal), log.New(io.Discard, "", 0))
	if err == nil {
		t.Fatal("run() error = nil; want serve error")
	}
	if !strings.Contains(err.Error(), "serve failed:") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunMetricsServeError(t *testing.T) {
	metricsListener := listenTemp(t)
	defer metricsListener.Close()

	configPath := writeConfigFile(t, freeAddr(t), metricsListener.Addr().String(), true)
	err := run(configPath, make(chan os.Signal), log.New(io.Discard, "", 0))
	if err == nil {
		t.Fatal("run() error = nil; want metrics serve error")
	}
	if !strings.Contains(err.Error(), "metrics serve failed:") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunGracefulShutdownOnSignal(t *testing.T) {
	listenAddr := freeAddr(t)
	configPath := writeConfigFile(t, listenAddr, "", false)

	sigCh := make(chan os.Signal, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- run(configPath, sigCh, log.New(io.Discard, "", 0))
	}()

	waitForDialSuccess(t, listenAddr)
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

func writeConfigFile(t *testing.T, listenAddr, metricsAddr string, metricsEnabled bool) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	metricsBlock := "enabled: false"
	if metricsEnabled {
		metricsBlock = "enabled: true\n  listen_addr: \"" + metricsAddr + "\""
	}

	content := "server:\n" +
		"  listen_addr: \"" + listenAddr + "\"\n" +
		"metrics:\n" +
		"  " + metricsBlock + "\n" +
		"healthcheck:\n" +
		"  interval_ms: 100\n" +
		"  timeout_ms: 50\n" +
		"tcp:\n" +
		"  keepalive_ms: 1000\n" +
		"timeouts:\n" +
		"  dial_ms: 100\n" +
		"  read_ms: 100\n" +
		"  write_ms: 100\n" +
		"  idle_ms: 1000\n" +
		"  shutdown_grace_period_ms: 100\n" +
		"backends:\n" +
		"  api:\n" +
		"    addresses:\n" +
		"      - \"127.0.0.1:1\"\n" +
		"    healthcheck: {}\n" +
		"routes:\n" +
		"  \"api.internal\": \"api\"\n"

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	return path
}

func listenTemp(t *testing.T) net.Listener {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	return ln
}

func freeAddr(t *testing.T) string {
	t.Helper()

	ln := listenTemp(t)
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func waitForDialSuccess(t *testing.T, addr string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(25 * time.Millisecond)
	}

	t.Fatalf("listener %s did not become reachable", addr)
}
