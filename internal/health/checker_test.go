package health

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"testing"
	"time"

	"zerosock/internal/config"
	"zerosock/internal/router"
)

func TestProbeAllUpdatesBackendStatus(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()
	r, err := router.New(map[string][]string{
		"api.internal": {addr},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	groups := []config.BackendGroupConfig{
		{Name: "g1", Addresses: []string{addr}, Interval: 200 * time.Millisecond, Timeout: 100 * time.Millisecond, Path: ""},
	}
	hostToGroup := map[string]string{"api.internal": "g1"}
	logger := log.New(io.Discard, "", 0)
	c := New(r, groups, hostToGroup, logger, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Start(ctx)

	time.Sleep(250 * time.Millisecond)
	snapshot := r.Snapshot()
	if len(snapshot) != 1 || !snapshot[0].Alive {
		t.Fatalf("expected backend to be alive, got=%+v", snapshot)
	}

	_ = ln.Close()
	time.Sleep(250 * time.Millisecond)
	snapshot = r.Snapshot()
	if len(snapshot) != 1 || snapshot[0].Alive {
		t.Fatalf("expected backend to be dead, got=%+v", snapshot)
	}
}

func TestL7Probe(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	srv := &http.Server{Handler: mux}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)
	defer srv.Close()

	addr := ln.Addr().String()
	r, err := router.New(map[string][]string{"svc": {addr}})
	if err != nil {
		t.Fatalf("router.New() error = %v", err)
	}

	groups := []config.BackendGroupConfig{
		{Name: "svc", Addresses: []string{addr}, Interval: 50 * time.Millisecond, Timeout: 2 * time.Second, Path: "/healthz"},
	}
	hostToGroup := map[string]string{"svc": "svc"}
	logger := log.New(io.Discard, "", 0)
	c := New(r, groups, hostToGroup, logger, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Start(ctx)

	time.Sleep(100 * time.Millisecond)
	snapshot := r.Snapshot()
	if len(snapshot) != 1 || !snapshot[0].Alive {
		t.Fatalf("L7: expected backend alive, got=%+v", snapshot)
	}
	cancel()
}

func TestL4OneGroupTwoHosts(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()
	r, err := router.New(map[string][]string{
		"host1": {addr},
		"host2": {addr},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	groups := []config.BackendGroupConfig{
		{Name: "pool", Addresses: []string{addr}, Interval: 100 * time.Millisecond, Timeout: 50 * time.Millisecond, Path: ""},
	}
	hostToGroup := map[string]string{"host1": "pool", "host2": "pool"}
	logger := log.New(io.Discard, "", 0)
	c := New(r, groups, hostToGroup, logger, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Start(ctx)

	time.Sleep(150 * time.Millisecond)
	snapshot := r.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 snapshot entries (host1, host2), got %d", len(snapshot))
	}
	for _, s := range snapshot {
		if !s.Alive {
			t.Fatalf("expected both backends alive, got=%+v", snapshot)
		}
	}
	cancel()
}
