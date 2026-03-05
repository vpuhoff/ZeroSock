package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"zerosock/internal/config"
	"zerosock/internal/health"
	"zerosock/internal/router"
	"zerosock/internal/socks"
)

func main() {
	logger := log.New(os.Stdout, "", log.LstdFlags)

	configPath := flag.String("config", "config.yaml", "path to YAML config")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Fatalf("config error: %v", err)
	}

	rt, err := router.New(cfg.Routes)
	if err != nil {
		logger.Fatalf("router init error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	checker := health.New(rt, cfg.HealthcheckInterval, cfg.HealthcheckTimeout, logger)
	go checker.Start(ctx)

	server, err := socks.New(cfg.ListenAddr, rt, cfg.DialTimeout, cfg.TCPKeepAlive, logger)
	if err != nil {
		logger.Fatalf("server init error: %v", err)
	}

	serveErrCh := make(chan error, 1)
	go func() {
		serveErrCh <- server.Serve()
	}()

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case err := <-serveErrCh:
		if err != nil {
			logger.Fatalf("serve failed: %v", err)
		}
		return
	case sig := <-sigCh:
		logger.Printf("shutdown: received signal %s", sig)
	}

	cancel()
	if err := server.Shutdown(); err != nil && !errors.Is(err, os.ErrClosed) {
		logger.Printf("shutdown: close listener error: %v", err)
	}

	logger.Printf("shutdown: allowing active tunnels to finish for %s", cfg.ShutdownGrace)
	graceTimer := time.NewTimer(cfg.ShutdownGrace)
	defer graceTimer.Stop()

	select {
	case <-graceTimer.C:
		logger.Printf("shutdown: grace period elapsed")
	case sig := <-sigCh:
		logger.Printf("shutdown: second signal %s, exiting immediately", sig)
	}
}
