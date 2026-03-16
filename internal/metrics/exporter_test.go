package metrics

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestStartHTTPServesMetricsAndShutsDown(t *testing.T) {
	addr := getFreeTCPAddr(t)
	logger := log.New(io.Discard, "", 0)
	collector := NewCollector()
	collector.IncTCPState("syn")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := StartHTTP(ctx, addr, collector, logger)

	var body []byte
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/metrics")
		if err == nil {
			body, err = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err == nil && resp.StatusCode == http.StatusOK {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}

	if len(body) == 0 {
		t.Fatal("metrics endpoint did not become ready")
	}
	if !strings.Contains(string(body), `zerosock_tcp_state_total{state="syn"} 1`) {
		t.Fatalf("unexpected metrics body:\n%s", string(body))
	}

	cancel()

	select {
	case err, ok := <-errCh:
		if ok && err != nil {
			t.Fatalf("StartHTTP() returned unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("metrics server did not shut down in time")
	}
}

func TestStartHTTPReturnsListenError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := StartHTTP(ctx, ln.Addr().String(), NewCollector(), log.New(io.Discard, "", 0))

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected listen error, got nil")
		}
		if !strings.Contains(err.Error(), "metrics listen failed") {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive listen error in time")
	}
}

func getFreeTCPAddr(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}
