package socks

import (
	"io"
	"log"
	"net"
	"testing"
	"time"

	"zerosock/internal/metrics"
	"zerosock/internal/router"
)

func TestNewInitializesConnLimitSem(t *testing.T) {
	r, err := router.New(map[string][]string{
		"api.internal": {"127.0.0.1:18080"},
	})
	if err != nil {
		t.Fatalf("router.New() error = %v", err)
	}

	srv, err := New(
		"127.0.0.1:0",
		r,
		time.Second,
		time.Second,
		1,
		2,
		time.Second,
		time.Second,
		time.Second,
		log.New(io.Discard, "", 0),
		metrics.NewCollector(),
		nil,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if srv.connLimitSem == nil {
		t.Fatal("New() did not initialize connLimitSem")
	}
	if cap(srv.connLimitSem) != 1 {
		t.Fatalf("connLimitSem cap = %d; want 1", cap(srv.connLimitSem))
	}
}

func TestShutdownWithoutListener(t *testing.T) {
	srv := &Server{}
	if err := srv.Shutdown(); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestWaitReturnsFalseThenTrue(t *testing.T) {
	srv := &Server{}
	srv.wg.Add(1)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if srv.Wait(20 * time.Millisecond) {
			t.Error("Wait() = true; want false while waitgroup is active")
		}
	}()

	<-done
	srv.wg.Done()

	if !srv.Wait(time.Second) {
		t.Fatal("Wait() = false; want true after waitgroup is done")
	}
}

func TestServerServeAndShutdown(t *testing.T) {
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
	srv, err := New(
		getFreeServerAddr(t),
		r,
		time.Second,
		time.Second,
		0,
		0,
		time.Second,
		time.Second,
		2*time.Second,
		log.New(io.Discard, "", 0),
		m,
		nil,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	serveErrCh := make(chan error, 1)
	go func() {
		serveErrCh <- srv.Serve()
	}()

	waitForListenerReady(t, srv)

	clientConn, err := net.Dial("tcp", srv.listenAddr)
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

	payload := []byte("server path")
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("client Write() error = %v", err)
	}
	if err := client.CloseWrite(); err != nil {
		t.Fatalf("client CloseWrite() error = %v", err)
	}

	got := make([]byte, len(payload))
	if _, err := io.ReadFull(client, got); err != nil {
		t.Fatalf("client ReadFull() error = %v", err)
	}

	buf := make([]byte, 1)
	n, err := client.Read(buf)
	if n != 0 || err != io.EOF {
		t.Fatalf("client final read = (%d, %v); want (0, EOF)", n, err)
	}

	if err := srv.Shutdown(); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	select {
	case err := <-serveErrCh:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve() did not return after Shutdown()")
	}

	if !srv.Wait(2 * time.Second) {
		t.Fatal("Wait() = false; want true after client handler completes")
	}

	if err := <-backendDone; err != nil {
		t.Fatalf("backend echo loop error = %v", err)
	}

	metricsText := m.RenderPrometheusText()
	assertMetricContains(t, metricsText, `zerosock_connections_total 1`)
	assertMetricContains(t, metricsText, `zerosock_connections_active 0`)
	assertMetricContains(t, metricsText, `zerosock_tcp_state_total{state="syn"} 1`)
	assertMetricContains(t, metricsText, `zerosock_tcp_state_total{state="established"} 1`)
	assertMetricContains(t, metricsText, `zerosock_tcp_state_total{state="fin"} 1`)
}

func TestServeClientHandshakeErrorReleasesResources(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer ln.Close()

	serverConnCh := make(chan *net.TCPConn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			serverConnCh <- conn.(*net.TCPConn)
		}
	}()

	clientConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	client := clientConn.(*net.TCPConn)
	defer client.Close()

	serverConn := <-serverConnCh

	m := metrics.NewCollector()
	m.IncConnectionAccepted()
	connLimitSem := make(chan struct{}, 1)
	connLimitSem <- struct{}{}

	srv := &Server{
		logger:       log.New(io.Discard, "", 0),
		metrics:      m,
		connLimitSem: connLimitSem,
		dialer:       &routeDialer{},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.wg.Add(1)
		srv.serveClient(serverConn)
	}()

	if _, err := client.Write([]byte{0x04, 0x01, 0x00}); err != nil {
		t.Fatalf("client Write() error = %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("serveClient() did not finish in time")
	}

	if got := len(connLimitSem); got != 0 {
		t.Fatalf("connLimitSem len = %d; want 0 after release", got)
	}

	metricsText := m.RenderPrometheusText()
	assertMetricContains(t, metricsText, `zerosock_connections_active 0`)
	assertMetricContains(t, metricsText, `zerosock_connection_errors_total{stage="handshake"} 1`)
}

func TestServerUpdateRouterAffectsNewConnections(t *testing.T) {
	backendA := startTaggedBackend(t, "A:")
	defer backendA.Close()
	backendB := startTaggedBackend(t, "B:")
	defer backendB.Close()

	r1, err := router.New(map[string][]string{
		"api.internal": {backendA.Addr().String()},
	})
	if err != nil {
		t.Fatalf("router.New() error = %v", err)
	}

	m := metrics.NewCollector()
	srv, err := New(
		getFreeServerAddr(t),
		r1,
		time.Second,
		time.Second,
		0,
		0,
		time.Second,
		time.Second,
		2*time.Second,
		log.New(io.Discard, "", 0),
		m,
		nil,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	serveErrCh := make(chan error, 1)
	go func() {
		serveErrCh <- srv.Serve()
	}()
	waitForListenerReady(t, srv)

	if got := performServerRequest(t, srv.listenAddr, "api.internal", "ping"); got != "A:ping" {
		t.Fatalf("before UpdateRouter() got %q; want %q", got, "A:ping")
	}

	r2, err := router.New(map[string][]string{
		"api.internal": {backendB.Addr().String()},
	})
	if err != nil {
		t.Fatalf("router.New() second error = %v", err)
	}
	srv.UpdateRouter(r2)

	if got := performServerRequest(t, srv.listenAddr, "api.internal", "pong"); got != "B:pong" {
		t.Fatalf("after UpdateRouter() got %q; want %q", got, "B:pong")
	}

	if err := srv.Shutdown(); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := <-serveErrCh; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
}

func startTaggedBackend(t *testing.T, prefix string) net.Listener {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("backend Listen() error = %v", err)
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

func performServerRequest(t *testing.T, serverAddr, host, payload string) string {
	t.Helper()

	clientConn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	client := clientConn.(*net.TCPConn)
	defer client.Close()

	doNoAuthHandshake(t, client)
	reply := sendFQDNRequest(t, client, host, 443)
	if reply[1] != replySuccess {
		t.Fatalf("unexpected success reply code: got=%d want=%d", reply[1], replySuccess)
	}

	if _, err := client.Write([]byte(payload)); err != nil {
		t.Fatalf("client Write() error = %v", err)
	}

	buf := make([]byte, len(payload)+2)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("client Read() error = %v", err)
	}
	return string(buf[:n])
}

func getFreeServerAddr(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func waitForListenerReady(t *testing.T, srv *Server) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		srv.mu.Lock()
		ready := srv.listener != nil
		srv.mu.Unlock()
		if ready {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("listener %s did not become ready", srv.listenAddr)
}
