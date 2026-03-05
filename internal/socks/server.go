package socks

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	socks5 "github.com/armon/go-socks5"

	"zerosock/internal/router"
)

type Server struct {
	listenAddr string
	keepAlive  time.Duration
	logger     *log.Logger
	socks      *socks5.Server

	mu       sync.Mutex
	listener net.Listener
}

func New(listenAddr string, r *router.Router, dialTimeout, keepAlive time.Duration, logger *log.Logger) (*Server, error) {
	cfg := newServerConfig(r, dialTimeout, keepAlive)
	cfg.Logger = logger

	srv, err := socks5.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("init socks server: %w", err)
	}

	return &Server{
		listenAddr: listenAddr,
		keepAlive:  keepAlive,
		logger:     logger,
		socks:      srv,
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
	err = s.socks.Serve(ln)
	if err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("socks serve: %w", err)
	}
	return nil
}

func (s *Server) Shutdown() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return nil
	}
	return s.listener.Close()
}
