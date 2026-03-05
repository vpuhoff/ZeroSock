package socks

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"zerosock/internal/router"
)

type routeDialer struct {
	router      *router.Router
	dialTimeout time.Duration
	keepAlive   time.Duration
}

func newRouteDialer(r *router.Router, dialTimeout, keepAlive time.Duration) *routeDialer {
	return &routeDialer{
		router:      r,
		dialTimeout: dialTimeout,
		keepAlive:   keepAlive,
	}
}

func (d *routeDialer) DialRoute(routeHost string) (*net.TCPConn, string, error) {
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

func handleConnection(client *net.TCPConn, dialer *routeDialer) error {
	if err := handleHandshake(client); err != nil {
		return err
	}

	req, err := readRequest(client)
	if err != nil {
		return err
	}

	backendConn, _, err := dialer.DialRoute(req.RouteKey())
	if err != nil {
		_ = writeFailureReply(client, replyHostUnreachable)
		return err
	}
	defer backendConn.Close()

	if err := writeSuccessReply(client, backendConn.LocalAddr()); err != nil {
		return fmt.Errorf("write success reply: %w", err)
	}

	return relay(client, backendConn)
}

func relay(client, backend *net.TCPConn) error {
	errCh := make(chan error, 2)
	go copyHalf(backend, client, errCh)
	go copyHalf(client, backend, errCh)

	var firstErr error
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil && !isIgnorableCopyError(err) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func copyHalf(dst, src *net.TCPConn, errCh chan<- error) {
	_, err := io.Copy(dst, src)
	_ = dst.CloseWrite()
	errCh <- err
}

func isIgnorableCopyError(err error) bool {
	if err == nil {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "closed network connection") || strings.Contains(msg, "broken pipe")
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimSuffix(host, ".")
	return host
}
