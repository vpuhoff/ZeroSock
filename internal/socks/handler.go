package socks

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"zerosock/internal/metrics"
	"zerosock/internal/router"
)

type routeDialer struct {
	router      *router.Router
	dialTimeout time.Duration
	keepAlive   time.Duration
	inflightSem chan struct{}
}

func newRouteDialer(r *router.Router, dialTimeout, keepAlive time.Duration, maxInflightDials int) *routeDialer {
	var inflightSem chan struct{}
	if maxInflightDials > 0 {
		inflightSem = make(chan struct{}, maxInflightDials)
	}
	return &routeDialer{
		router:      r,
		dialTimeout: dialTimeout,
		keepAlive:   keepAlive,
		inflightSem: inflightSem,
	}
}

func (d *routeDialer) DialRoute(routeHost string) (*net.TCPConn, string, error) {
	if d.inflightSem != nil {
		select {
		case d.inflightSem <- struct{}{}:
		default:
			return nil, "", fmt.Errorf("dial inflight limit reached for host %q", routeHost)
		}
		defer func() { <-d.inflightSem }()
	}

	target, err := d.router.Pick(normalizeHost(routeHost))
	if err != nil {
		if errors.Is(err, router.ErrRouteNotFound) {
			return nil, "", fmt.Errorf("route for host %q not found", routeHost)
		}
		if errors.Is(err, router.ErrNoAliveBackends) {
			return nil, "", fmt.Errorf("no alive backends for host %q", routeHost)
		}
		return nil, "", fmt.Errorf("pick backend for host %q: %w", routeHost, err)
	}

	dialer := &net.Dialer{
		Timeout:   d.dialTimeout,
		KeepAlive: d.keepAlive,
	}
	conn, err := dialer.Dial("tcp", target)
	if err != nil {
		return nil, target, fmt.Errorf("dial backend %q for host %q: %w", target, routeHost, err)
	}

	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		_ = conn.Close()
		return nil, target, fmt.Errorf("backend connection for %q is not TCP", target)
	}
	_ = tcpConn.SetKeepAlive(true)
	_ = tcpConn.SetKeepAlivePeriod(d.keepAlive)
	return tcpConn, target, nil
}

func handleConnection(client *net.TCPConn, dialer *routeDialer, m *metrics.Collector, readTimeout, writeTimeout, idleTimeout time.Duration) error {
	sessionStart := time.Now()
	handshakeStart := time.Now()

	if readTimeout > 0 {
		_ = client.SetReadDeadline(time.Now().Add(readTimeout))
	}
	if err := handleHandshake(client); err != nil {
		m.IncConnectionError("handshake")
		return err
	}

	if readTimeout > 0 {
		_ = client.SetReadDeadline(time.Now().Add(readTimeout))
	}
	req, err := readRequest(client)
	m.ObserveHandshakeLatency(time.Since(handshakeStart))
	_ = client.SetReadDeadline(time.Time{})
	if err != nil {
		m.IncConnectionError("request")
		return err
	}
	routeHost := req.RouteKey()
	m.IncRequest(atypLabel(req.atyp))

	dialStart := time.Now()
	backendConn, backendAddr, err := dialer.DialRoute(routeHost)
	m.ObserveBackendDialLatency(time.Since(dialStart))
	if err != nil {
		reason := classifyDialError(err)
		m.IncBackendDialFailure(routeHost, reason)
		m.IncRouteFailure(routeHost, reason)
		m.IncRequestByBackend(routeHost, backendAddr, reason)
		m.IncConnectionError("backend_dial")
		_ = writeFailureReply(client, replyHostUnreachable)
		return err
	}
	defer backendConn.Close()

	if writeTimeout > 0 {
		_ = client.SetWriteDeadline(time.Now().Add(writeTimeout))
	}
	if err := writeSuccessReply(client, backendConn.LocalAddr()); err != nil {
		m.IncRequestByBackend(routeHost, backendAddr, "reply_error")
		m.IncConnectionError("reply")
		return fmt.Errorf("write success reply: %w", err)
	}
	_ = client.SetWriteDeadline(time.Time{})

	if idleTimeout > 0 {
		// Fixed full-session idle cap without wrapping sockets, preserving io.Copy path.
		_ = client.SetDeadline(time.Now().Add(idleTimeout))
		_ = backendConn.SetDeadline(time.Now().Add(idleTimeout))
	}

	if err := relay(client, backendConn, m); err != nil {
		m.IncRequestByBackend(routeHost, backendAddr, "relay_error")
		m.IncConnectionError("relay")
		return err
	}
	m.IncRequestByBackend(routeHost, backendAddr, "success")
	m.ObserveSessionDuration(time.Since(sessionStart))
	return nil
}

func relay(client, backend *net.TCPConn, m *metrics.Collector) error {
	errCh := make(chan error, 2)
	go copyHalf(backend, client, "client_to_backend", m, errCh)
	go copyHalf(client, backend, "backend_to_client", m, errCh)

	var firstErr error
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil && !isIgnorableCopyError(err) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func copyHalf(dst, src *net.TCPConn, direction string, m *metrics.Collector, errCh chan<- error) {
	n, err := io.Copy(dst, src)
	m.AddRelayBytes(direction, n)
	_ = dst.CloseWrite()
	errCh <- err
}

func isIgnorableCopyError(err error) bool {
	if err == nil {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "closed network connection") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "forcibly closed by the remote host")
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimSuffix(host, ".")
	return host
}

func atypLabel(atyp byte) string {
	switch atyp {
	case atypIPv4:
		return "ipv4"
	case atypFQDN:
		return "fqdn"
	default:
		return "unknown"
	}
}

func classifyDialError(err error) string {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "route for host"):
		return "route_not_found"
	case strings.Contains(msg, "no alive backends"):
		return "no_alive_backends"
	case strings.Contains(msg, "inflight limit reached"):
		return "dial_limit"
	case strings.Contains(msg, "timeout"):
		return "timeout"
	default:
		return "dial_error"
	}
}
