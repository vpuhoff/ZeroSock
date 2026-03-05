package socks

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"zerosock/internal/router"
)

type Server struct {
	listenAddr string
	keepAlive  time.Duration
	dialer     *routeDialer
	logger     *log.Logger

	mu       sync.Mutex
	listener net.Listener
	wg       sync.WaitGroup
}

func New(listenAddr string, r *router.Router, dialTimeout, keepAlive time.Duration, logger *log.Logger) (*Server, error) {
	return &Server{
		listenAddr: listenAddr,
		keepAlive:  keepAlive,
		dialer:     newRouteDialer(r, dialTimeout, keepAlive),
		logger:     logger,
	}, nil
}

func (s *Server) Serve() error {
	lc := net.ListenConfig{
		KeepAlive: s.keepAlive,
	}
	ln, err := lc.Listen(context.Background(), "tcp", s.listenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.listenAddr, err)
	}

	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()

	s.logger.Printf("socks5: listening on %s", s.listenAddr)
	for {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			if errors.Is(acceptErr, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept: %w", acceptErr)
		}

		client, ok := conn.(*net.TCPConn)
		if !ok {
			s.logger.Printf("socks5: rejecting non-tcp connection type=%T", conn)
			_ = conn.Close()
			continue
		}

		_ = client.SetKeepAlive(true)
		_ = client.SetKeepAlivePeriod(s.keepAlive)

		s.wg.Add(1)
		go s.serveClient(client)
	}
}

func (s *Server) Shutdown() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return nil
	}
	return s.listener.Close()
}

func (s *Server) Wait(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func (s *Server) serveClient(client *net.TCPConn) {
	defer s.wg.Done()
	defer client.Close()

	if err := handleConnection(client, s.dialer); err != nil {
		s.logger.Printf("socks5: connection error from=%s err=%v", client.RemoteAddr(), err)
	}
}
