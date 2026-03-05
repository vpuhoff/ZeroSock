package socks

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	socks5 "github.com/armon/go-socks5"

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

func (d *routeDialer) Dial(_ context.Context, network, addr string) (net.Conn, error) {
	if network != "tcp" {
		return nil, fmt.Errorf("unsupported network: %s", network)
	}

	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid destination %q: %w", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid destination port %q: %w", portStr, err)
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("destination port out of range: %d", port)
	}

	target, err := d.router.Pick(normalizeHost(host))
	if err != nil {
		if errors.Is(err, router.ErrRouteNotFound) {
			return nil, fmt.Errorf("route for host %q not found", host)
		}
		if errors.Is(err, router.ErrNoAliveBackends) {
			return nil, fmt.Errorf("no alive backends for host %q", host)
		}
		return nil, fmt.Errorf("pick backend for host %q: %w", host, err)
	}

	dialer := &net.Dialer{
		Timeout:   d.dialTimeout,
		KeepAlive: d.keepAlive,
	}
	conn, err := dialer.Dial("tcp", target)
	if err != nil {
		return nil, fmt.Errorf("dial backend %q for host %q: %w", target, host, err)
	}

	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(d.keepAlive)
	}

	return conn, nil
}

// passThroughResolver preserves FQDN and prevents system DNS lookups.
type passThroughResolver struct{}

func (passThroughResolver) Resolve(ctx context.Context, _ string) (context.Context, net.IP, error) {
	return ctx, nil, nil
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimSuffix(host, ".")
	return host
}

func newServerConfig(r *router.Router, dialTimeout, keepAlive time.Duration) *socks5.Config {
	rd := newRouteDialer(r, dialTimeout, keepAlive)
	return &socks5.Config{
		Resolver: passThroughResolver{},
		Dial:     rd.Dial,
	}
}
