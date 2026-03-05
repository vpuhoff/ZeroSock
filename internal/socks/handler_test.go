package socks

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"zerosock/internal/router"
)

func TestDialRouteRejectsUnknownHost(t *testing.T) {
	r, err := router.New(map[string][]string{
		"api.internal": {"127.0.0.1:18080"},
	})
	if err != nil {
		t.Fatalf("router.New() error = %v", err)
	}

	d := newRouteDialer(r, 200*time.Millisecond, 5*time.Second, 0)
	_, _, err = d.DialRoute("unknown.internal")
	if err == nil {
		t.Fatalf("DialRoute() expected error for unknown host")
	}
}

func TestDialRouteDialsAliveBackend(t *testing.T) {
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

	d := newRouteDialer(r, 500*time.Millisecond, 5*time.Second, 0)
	conn, _, err := d.DialRoute("api.internal")
	if err != nil {
		t.Fatalf("DialRoute() error = %v", err)
	}
	_ = conn.Close()

	select {
	case <-accepted:
	case <-time.After(1 * time.Second):
		t.Fatalf("backend did not receive connection")
	}
}

func TestDialRouteInflightLimit(t *testing.T) {
	r, err := router.New(map[string][]string{
		"api.internal": {"127.0.0.1:1"},
	})
	if err != nil {
		t.Fatalf("router.New() error = %v", err)
	}

	d := newRouteDialer(r, time.Second, 5*time.Second, 1)
	d.inflightSem <- struct{}{}
	_, _, err = d.DialRoute("api.internal")
	if err == nil {
		t.Fatalf("DialRoute() expected inflight limit error")
	}
	if !strings.Contains(err.Error(), "inflight limit reached") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleHandshakeNoAuth(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		done <- handleHandshake(server)
	}()

	_, err := client.Write([]byte{0x05, 0x01, 0x00})
	if err != nil {
		t.Fatalf("write client greeting: %v", err)
	}

	reply := make([]byte, 2)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read server auth reply: %v", err)
	}
	if !bytes.Equal(reply, []byte{0x05, 0x00}) {
		t.Fatalf("unexpected auth reply: %v", reply)
	}

	if err := <-done; err != nil {
		t.Fatalf("handleHandshake() error = %v", err)
	}
}

func TestReadRequestRejectsIPv6(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		_, err := readRequest(server)
		done <- err
	}()

	_, err := client.Write([]byte{
		0x05, 0x01, 0x00, 0x04, // header with IPv6 atyp
	})
	if err != nil {
		t.Fatalf("write request header: %v", err)
	}

	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read failure reply: %v", err)
	}
	if reply[1] != replyAddrTypeNotSupp {
		t.Fatalf("unexpected reply code: got=%d", reply[1])
	}

	if err := <-done; err == nil {
		t.Fatalf("expected readRequest error for IPv6")
	}
}

func TestReadRequestFQDN(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan *request, 1)
	errCh := make(chan error, 1)
	go func() {
		req, err := readRequest(server)
		if err != nil {
			errCh <- err
			return
		}
		done <- req
	}()

	host := "api.internal"
	buf := make([]byte, 0, 4+1+len(host)+2)
	buf = append(buf, 0x05, 0x01, 0x00, 0x03, byte(len(host)))
	buf = append(buf, []byte(host)...)
	port := make([]byte, 2)
	binary.BigEndian.PutUint16(port, 443)
	buf = append(buf, port...)

	if _, err := client.Write(buf); err != nil {
		t.Fatalf("write request: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("readRequest() error = %v", err)
	case req := <-done:
		if req.host != host || req.port != 443 {
			t.Fatalf("unexpected request: %+v", req)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("readRequest timed out")
	}
}
