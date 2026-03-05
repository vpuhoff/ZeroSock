package health

import (
	"io"
	"log"
	"net"
	"testing"
	"time"

	"zerosock/internal/router"
)

func TestProbeAllUpdatesBackendStatus(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer ln.Close()

	r, err := router.New(map[string][]string{
		"api.internal": {ln.Addr().String()},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	logger := log.New(io.Discard, "", 0)
	c := New(r, 200*time.Millisecond, 100*time.Millisecond, logger)
	c.probeAll()

	snapshot := r.Snapshot()
	if len(snapshot) != 1 || !snapshot[0].Alive {
		t.Fatalf("expected backend to be alive, got=%+v", snapshot)
	}

	_ = ln.Close()
	c.probeAll()

	snapshot = r.Snapshot()
	if len(snapshot) != 1 || snapshot[0].Alive {
		t.Fatalf("expected backend to be dead, got=%+v", snapshot)
	}
}
