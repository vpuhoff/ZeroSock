package socks

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"zerosock/internal/metrics"
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

func TestHandleHandshakeUnsupportedVersion(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		done <- handleHandshake(server)
	}()

	if _, err := client.Write([]byte{0x04, 0x01}); err != nil {
		t.Fatalf("write client greeting: %v", err)
	}

	if err := <-done; err == nil || !strings.Contains(err.Error(), "unsupported socks version") {
		t.Fatalf("handleHandshake() error = %v; want unsupported version", err)
	}
}

func TestHandleHandshakeEmptyMethods(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		done <- handleHandshake(server)
	}()

	if _, err := client.Write([]byte{0x05, 0x00}); err != nil {
		t.Fatalf("write client greeting: %v", err)
	}

	if err := <-done; err == nil || !strings.Contains(err.Error(), "empty auth methods list") {
		t.Fatalf("handleHandshake() error = %v; want empty methods error", err)
	}
}

func TestHandleHandshakeNoSupportedMethod(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		done <- handleHandshake(server)
	}()

	if _, err := client.Write([]byte{0x05, 0x01, 0x02}); err != nil {
		t.Fatalf("write client greeting: %v", err)
	}

	reply := make([]byte, 2)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read server auth reply: %v", err)
	}
	if !bytes.Equal(reply, []byte{0x05, 0xff}) {
		t.Fatalf("unexpected auth reply: %v", reply)
	}

	if err := <-done; err == nil || !strings.Contains(err.Error(), "no supported authentication method") {
		t.Fatalf("handleHandshake() error = %v; want unsupported auth method", err)
	}
}

func TestReadRequestIPv4(t *testing.T) {
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

	if _, err := client.Write([]byte{0x05, 0x01, 0x00, 0x01, 127, 0, 0, 1, 0x01, 0xbb}); err != nil {
		t.Fatalf("write request: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("readRequest() error = %v", err)
	case req := <-done:
		if req.host != "127.0.0.1" || req.port != 443 {
			t.Fatalf("unexpected request: %+v", req)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("readRequest timed out")
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

func TestReadRequestUnsupportedVersion(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		_, err := readRequest(server)
		done <- err
	}()

	if _, err := client.Write([]byte{0x04, 0x01, 0x00, 0x01}); err != nil {
		t.Fatalf("write request header: %v", err)
	}

	if err := <-done; err == nil || !strings.Contains(err.Error(), "unsupported request version") {
		t.Fatalf("readRequest() error = %v; want unsupported request version", err)
	}
}

func TestReadRequestRejectsUnsupportedCommand(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		_, err := readRequest(server)
		done <- err
	}()

	if _, err := client.Write([]byte{0x05, 0x02, 0x00, 0x01}); err != nil {
		t.Fatalf("write request header: %v", err)
	}

	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read failure reply: %v", err)
	}
	if reply[1] != replyCommandNotSupp {
		t.Fatalf("unexpected reply code: got=%d want=%d", reply[1], replyCommandNotSupp)
	}

	if err := <-done; err == nil || !strings.Contains(err.Error(), "unsupported command") {
		t.Fatalf("readRequest() error = %v; want unsupported command", err)
	}
}

func TestReadRequestRejectsReservedByte(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		_, err := readRequest(server)
		done <- err
	}()

	if _, err := client.Write([]byte{0x05, 0x01, 0x01, 0x01}); err != nil {
		t.Fatalf("write request header: %v", err)
	}

	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read failure reply: %v", err)
	}
	if reply[1] != replyGeneralFailure {
		t.Fatalf("unexpected reply code: got=%d want=%d", reply[1], replyGeneralFailure)
	}

	if err := <-done; err == nil || !strings.Contains(err.Error(), "invalid reserved byte") {
		t.Fatalf("readRequest() error = %v; want invalid reserved byte", err)
	}
}

func TestReadRequestRejectsEmptyFQDN(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		_, err := readRequest(server)
		done <- err
	}()

	if _, err := client.Write([]byte{0x05, 0x01, 0x00, 0x03, 0x00}); err != nil {
		t.Fatalf("write request header: %v", err)
	}

	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read failure reply: %v", err)
	}
	if reply[1] != replyAddrTypeNotSupp {
		t.Fatalf("unexpected reply code: got=%d want=%d", reply[1], replyAddrTypeNotSupp)
	}

	if err := <-done; err == nil || !strings.Contains(err.Error(), "empty fqdn") {
		t.Fatalf("readRequest() error = %v; want empty fqdn", err)
	}
}

func TestReadRequestRejectsUnsupportedAtyp(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		_, err := readRequest(server)
		done <- err
	}()

	if _, err := client.Write([]byte{0x05, 0x01, 0x00, 0x09}); err != nil {
		t.Fatalf("write request header: %v", err)
	}

	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read failure reply: %v", err)
	}
	if reply[1] != replyAddrTypeNotSupp {
		t.Fatalf("unexpected reply code: got=%d want=%d", reply[1], replyAddrTypeNotSupp)
	}

	if err := <-done; err == nil || !strings.Contains(err.Error(), "unsupported atyp") {
		t.Fatalf("readRequest() error = %v; want unsupported atyp", err)
	}
}

func TestReadRequestRejectsPortZero(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		_, err := readRequest(server)
		done <- err
	}()

	host := "api.internal"
	buf := make([]byte, 0, 4+1+len(host)+2)
	buf = append(buf, 0x05, 0x01, 0x00, 0x03, byte(len(host)))
	buf = append(buf, []byte(host)...)
	buf = append(buf, 0x00, 0x00)

	if _, err := client.Write(buf); err != nil {
		t.Fatalf("write request: %v", err)
	}

	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read failure reply: %v", err)
	}
	if reply[1] != replyGeneralFailure {
		t.Fatalf("unexpected reply code: got=%d want=%d", reply[1], replyGeneralFailure)
	}

	if err := <-done; err == nil || !strings.Contains(err.Error(), "invalid destination port 0") {
		t.Fatalf("readRequest() error = %v; want invalid destination port", err)
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

func TestClassifyCloseType(t *testing.T) {
	for _, tt := range []struct {
		name string
		err1 error
		err2 error
		want string
	}{
		{name: "nil errors mean fin", want: "fin"},
		{name: "reset in first error", err1: errors.New("connection reset by peer"), want: "rst"},
		{name: "broken pipe in second error", err2: errors.New("broken pipe"), want: "rst"},
		{name: "forcibly closed is rst", err1: errors.New("forcibly closed by the remote host"), want: "rst"},
		{name: "closed network connection stays fin", err1: errors.New("use of closed network connection"), want: "fin"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyCloseType(tt.err1, tt.err2); got != tt.want {
				t.Fatalf("classifyCloseType(%v, %v) = %q; want %q", tt.err1, tt.err2, got, tt.want)
			}
		})
	}
}

func TestIsRSTError(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", want: false},
		{name: "connection reset", err: errors.New("connection reset by peer"), want: true},
		{name: "broken pipe uppercase", err: errors.New("BROKEN PIPE"), want: true},
		{name: "forcibly closed mixed case", err: errors.New("Forcibly Closed By The Remote Host"), want: true},
		{name: "closed network connection", err: errors.New("use of closed network connection"), want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRSTError(tt.err); got != tt.want {
				t.Fatalf("isRSTError(%v) = %v; want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsIgnorableCopyError(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", want: true},
		{name: "closed network connection", err: errors.New("use of closed network connection"), want: true},
		{name: "broken pipe", err: errors.New("broken pipe"), want: true},
		{name: "connection reset", err: errors.New("connection reset by peer"), want: true},
		{name: "forcibly closed", err: errors.New("forcibly closed by the remote host"), want: true},
		{name: "other error", err: errors.New("some other error"), want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := isIgnorableCopyError(tt.err); got != tt.want {
				t.Fatalf("isIgnorableCopyError(%v) = %v; want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestNormalizeHost(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   string
		want string
	}{
		{name: "trim lowercase dot", in: "  API.INTERNAL. ", want: "api.internal"},
		{name: "already normalized", in: "api.internal", want: "api.internal"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeHost(tt.in); got != tt.want {
				t.Fatalf("normalizeHost(%q) = %q; want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestAtypLabel(t *testing.T) {
	for _, tt := range []struct {
		name string
		atyp byte
		want string
	}{
		{name: "ipv4", atyp: atypIPv4, want: "ipv4"},
		{name: "fqdn", atyp: atypFQDN, want: "fqdn"},
		{name: "unknown", atyp: 0xff, want: "unknown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := atypLabel(tt.atyp); got != tt.want {
				t.Fatalf("atypLabel(%d) = %q; want %q", tt.atyp, got, tt.want)
			}
		})
	}
}

func TestClassifyDialError(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{name: "route not found", err: errors.New(`route for host "api.internal" not found`), want: "route_not_found"},
		{name: "no alive backends", err: errors.New(`no alive backends for host "api.internal"`), want: "no_alive_backends"},
		{name: "dial limit", err: errors.New(`dial inflight limit reached for host "api.internal"`), want: "dial_limit"},
		{name: "timeout", err: errors.New("dial tcp timeout"), want: "timeout"},
		{name: "fallback", err: errors.New("permission denied"), want: "dial_error"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyDialError(tt.err); got != tt.want {
				t.Fatalf("classifyDialError(%v) = %q; want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestHandleConnectionSuccessfulSession(t *testing.T) {
	backendLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("backend Listen() error = %v", err)
	}
	defer backendLn.Close()

	backendDone := make(chan error, 1)
	go func() {
		conn, err := backendLn.Accept()
		if err != nil {
			backendDone <- err
			return
		}
		defer conn.Close()
		_, err = io.Copy(conn, conn)
		backendDone <- err
	}()

	r, err := router.New(map[string][]string{
		"api.internal": {backendLn.Addr().String()},
	})
	if err != nil {
		t.Fatalf("router.New() error = %v", err)
	}

	m := metrics.NewCollector()
	dialer := newRouteDialer(r, time.Second, time.Second, 0)
	serverAddr, handleErrCh := startHandleConnectionServer(t, dialer, m)

	clientConn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	client := clientConn.(*net.TCPConn)
	defer client.Close()

	doNoAuthHandshake(t, client)
	reply := sendFQDNRequest(t, client, "api.internal", 443)
	if reply[1] != replySuccess {
		t.Fatalf("unexpected success reply code: got=%d want=%d", reply[1], replySuccess)
	}

	payload := []byte("hello through socks")
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("client Write() error = %v", err)
	}
	if err := client.CloseWrite(); err != nil {
		t.Fatalf("client CloseWrite() error = %v", err)
	}

	got := make([]byte, len(payload))
	if _, err := io.ReadFull(client, got); err != nil {
		t.Fatalf("client read echoed payload error = %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("echoed payload = %q; want %q", string(got), string(payload))
	}

	buf := make([]byte, 1)
	n, err := client.Read(buf)
	if n != 0 || err != io.EOF {
		t.Fatalf("client final read = (%d, %v); want (0, EOF)", n, err)
	}

	if err := <-handleErrCh; err != nil {
		t.Fatalf("handleConnection() error = %v", err)
	}
	if err := <-backendDone; err != nil {
		t.Fatalf("backend echo loop error = %v", err)
	}

	metricsText := m.RenderPrometheusText()
	assertMetricContains(t, metricsText, `zerosock_tcp_state_total{state="established"} 1`)
	assertMetricContains(t, metricsText, `zerosock_tcp_state_total{state="fin"} 1`)
	assertMetricContains(t, metricsText, `zerosock_requests_total{atyp="fqdn"} 1`)
	assertMetricContains(t, metricsText, `zerosock_requests_backend_total{backend="`+backendLn.Addr().String()+`",host="api.internal",result="success"} 1`)
	assertMetricContains(t, metricsText, `zerosock_session_duration_seconds_count 1`)
	assertMetricContains(t, metricsText, `zerosock_relay_bytes_total{direction="client_to_backend"} 19`)
	assertMetricContains(t, metricsText, `zerosock_relay_bytes_total{direction="backend_to_client"} 19`)
}

func TestHandleConnectionRejectsIPv4OutsideRoutes(t *testing.T) {
	r, err := router.New(map[string][]string{
		"api.internal": {"127.0.0.1:18080"},
	})
	if err != nil {
		t.Fatalf("router.New() error = %v", err)
	}

	m := metrics.NewCollector()
	dialer := newRouteDialer(r, time.Second, time.Second, 0)
	serverAddr, handleErrCh := startHandleConnectionServer(t, dialer, m)

	clientConn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	client := clientConn.(*net.TCPConn)
	defer client.Close()

	doNoAuthHandshake(t, client)
	reply := sendIPv4Request(t, client, net.IPv4(203, 0, 113, 10), 443)
	if reply[1] != replyHostUnreachable {
		t.Fatalf("unexpected failure reply code: got=%d want=%d", reply[1], replyHostUnreachable)
	}

	err = <-handleErrCh
	if err == nil || !strings.Contains(err.Error(), "not in any route") {
		t.Fatalf("handleConnection() error = %v; want whitelist failure", err)
	}

	metricsText := m.RenderPrometheusText()
	assertMetricContains(t, metricsText, `zerosock_route_failures_total{host="203.0.113.10:443",reason="ip_not_in_routes"} 1`)
	assertMetricContains(t, metricsText, `zerosock_connection_errors_total{stage="backend_dial"} 1`)
}

func TestHandleConnectionDialFailure(t *testing.T) {
	r, err := router.New(map[string][]string{
		"api.internal": {"127.0.0.1:1"},
	})
	if err != nil {
		t.Fatalf("router.New() error = %v", err)
	}

	m := metrics.NewCollector()
	dialer := newRouteDialer(r, 200*time.Millisecond, time.Second, 0)
	serverAddr, handleErrCh := startHandleConnectionServer(t, dialer, m)

	clientConn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	client := clientConn.(*net.TCPConn)
	defer client.Close()

	doNoAuthHandshake(t, client)
	reply := sendFQDNRequest(t, client, "api.internal", 443)
	if reply[1] != replyHostUnreachable {
		t.Fatalf("unexpected failure reply code: got=%d want=%d", reply[1], replyHostUnreachable)
	}

	err = <-handleErrCh
	if err == nil || !strings.Contains(err.Error(), `dial backend "127.0.0.1:1"`) {
		t.Fatalf("handleConnection() error = %v; want dial backend failure", err)
	}

	metricsText := m.RenderPrometheusText()
	assertMetricContains(t, metricsText, `zerosock_requests_total{atyp="fqdn"} 1`)
	assertMetricContains(t, metricsText, `zerosock_backend_dial_failures_total{host="api.internal",reason="dial_error"} 1`)
	assertMetricContains(t, metricsText, `zerosock_route_failures_total{host="api.internal",reason="dial_error"} 1`)
	assertMetricContains(t, metricsText, `zerosock_requests_backend_total{backend="127.0.0.1:1",host="api.internal",result="dial_error"} 1`)
	assertMetricContains(t, metricsText, `zerosock_connection_errors_total{stage="backend_dial"} 1`)
}

func TestHandleConnectionNoAliveBackends(t *testing.T) {
	r, err := router.New(map[string][]string{
		"api.internal": {"127.0.0.1:18080"},
	})
	if err != nil {
		t.Fatalf("router.New() error = %v", err)
	}
	if _, err := r.SetBackendAlive("api.internal", "127.0.0.1:18080", false); err != nil {
		t.Fatalf("SetBackendAlive() error = %v", err)
	}

	m := metrics.NewCollector()
	dialer := newRouteDialer(r, time.Second, time.Second, 0)
	serverAddr, handleErrCh := startHandleConnectionServer(t, dialer, m)

	clientConn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	client := clientConn.(*net.TCPConn)
	defer client.Close()

	doNoAuthHandshake(t, client)
	reply := sendFQDNRequest(t, client, "api.internal", 443)
	if reply[1] != replyHostUnreachable {
		t.Fatalf("unexpected failure reply code: got=%d want=%d", reply[1], replyHostUnreachable)
	}

	err = <-handleErrCh
	if err == nil || !strings.Contains(err.Error(), "no alive backends") {
		t.Fatalf("handleConnection() error = %v; want no alive backends", err)
	}

	metricsText := m.RenderPrometheusText()
	assertMetricContains(t, metricsText, `zerosock_backend_dial_failures_total{host="api.internal",reason="no_alive_backends"} 1`)
}

func startHandleConnectionServer(t *testing.T, dialer *routeDialer, m *metrics.Collector) (string, <-chan error) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("server Listen() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		defer ln.Close()
		conn, err := ln.Accept()
		if err != nil {
			errCh <- err
			return
		}
		errCh <- handleConnection(conn.(*net.TCPConn), dialer, m, time.Second, time.Second, 2*time.Second)
	}()

	return ln.Addr().String(), errCh
}

func doNoAuthHandshake(t *testing.T, conn net.Conn) {
	t.Helper()

	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}

	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read handshake reply: %v", err)
	}
	if !bytes.Equal(reply, []byte{0x05, 0x00}) {
		t.Fatalf("unexpected handshake reply: %v", reply)
	}
}

func sendFQDNRequest(t *testing.T, conn net.Conn, host string, port uint16) []byte {
	t.Helper()

	req := make([]byte, 0, 4+1+len(host)+2)
	req = append(req, 0x05, 0x01, 0x00, atypFQDN, byte(len(host)))
	req = append(req, []byte(host)...)
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, port)
	req = append(req, portBytes...)

	if _, err := conn.Write(req); err != nil {
		t.Fatalf("write fqdn request: %v", err)
	}

	reply := make([]byte, 10)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read fqdn reply: %v", err)
	}
	return reply
}

func sendIPv4Request(t *testing.T, conn net.Conn, ip net.IP, port uint16) []byte {
	t.Helper()

	req := make([]byte, 0, 10)
	req = append(req, 0x05, 0x01, 0x00, atypIPv4)
	req = append(req, ip.To4()...)
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, port)
	req = append(req, portBytes...)

	if _, err := conn.Write(req); err != nil {
		t.Fatalf("write ipv4 request: %v", err)
	}

	reply := make([]byte, 10)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read ipv4 reply: %v", err)
	}
	return reply
}

func assertMetricContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("metrics output missing %q\nfull output:\n%s", want, got)
	}
}
