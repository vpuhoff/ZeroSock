package socks

import (
	"context"
	"net"
	"testing"
	"time"

	"zerosock/internal/router"
)

func TestRouteDialerRejectsUnknownHost(t *testing.T) {
	r, err := router.New(map[string][]string{
		"api.internal": {"127.0.0.1:18080"},
	})
	if err != nil {
		t.Fatalf("router.New() error = %v", err)
	}

	d := newRouteDialer(r, 200*time.Millisecond, 5*time.Second)
	_, err = d.Dial(context.Background(), "tcp", "unknown.internal:443")
	if err == nil {
		t.Fatalf("Dial() expected error for unknown host")
	}
}

func TestRouteDialerDialsAliveBackend(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer ln.Close()

	accepted := make(chan struct{}, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			accepted <- struct{}{}
			_ = conn.Close()
		}
	}()

	r, err := router.New(map[string][]string{
		"api.internal": {ln.Addr().String()},
	})
	if err != nil {
		t.Fatalf("router.New() error = %v", err)
	}

	d := newRouteDialer(r, 500*time.Millisecond, 5*time.Second)
	conn, err := d.Dial(context.Background(), "tcp", "api.internal:443")
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	_ = conn.Close()

	select {
	case <-accepted:
	case <-time.After(1 * time.Second):
		t.Fatalf("backend did not receive connection")
	}
}
