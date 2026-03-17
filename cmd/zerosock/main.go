package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"zerosock/internal/config"
	"zerosock/internal/health"
	"zerosock/internal/metrics"
	"zerosock/internal/router"
	"zerosock/internal/socks"
)

var (
	newMainLogger = func() *log.Logger {
		return log.New(os.Stdout, "", log.LstdFlags)
	}
	runMain         = run
	validateMainCfg = validateConfig
	fatalf          = func(logger *log.Logger, format string, args ...any) {
		logger.Fatalf(format, args...)
	}
)

func main() {
	logger := newMainLogger()

	configPath := flag.String("config", "config.yaml", "path to YAML config")
	checkConfigPath := flag.String("c", "", "validate config and exit")
	flag.Parse()

	if *checkConfigPath != "" {
		if err := validateMainCfg(*checkConfigPath, logger); err != nil {
			fatalf(logger, "%v", err)
		}
		return
	}

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	if err := runMain(*configPath, sigCh, logger); err != nil {
		fatalf(logger, "%v", err)
	}
}

func validateConfig(configPath string, logger *log.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("config error: %w", err)
	}
	if _, err := router.New(cfg.Routes); err != nil {
		return fmt.Errorf("router init error: %w", err)
	}
	logger.Printf("config OK: %s", configPath)
	return nil
}

func run(configPath string, sigCh <-chan os.Signal, logger *log.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("config error: %w", err)
	}

	rt, err := router.New(cfg.Routes)
	if err != nil {
		return fmt.Errorf("router init error: %w", err)
	}

	metricCollector := metrics.NewCollector()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	checker := health.New(rt, cfg.BackendGroups, cfg.HostToGroup, logger, metricCollector)
	go checker.Start(ctx)

	server, err := socks.New(
		cfg.ListenAddr,
		rt,
		cfg.DialTimeout,
		cfg.TCPKeepAlive,
		cfg.MaxConnections,
		cfg.MaxInflightDials,
		cfg.ReadTimeout,
		cfg.WriteTimeout,
		cfg.IdleTimeout,
		logger,
		metricCollector,
	)
	if err != nil {
		return fmt.Errorf("server init error: %w", err)
	}

	var metricsErrCh <-chan error
	if cfg.MetricsEnabled {
		metricsErrCh = metrics.StartHTTP(ctx, cfg.MetricsListenAddr, metricCollector, logger)
	}

	serveErrCh := make(chan error, 1)
	go func() {
		serveErrCh <- server.Serve()
	}()

	select {
	case err := <-serveErrCh:
		if err != nil {
			cancel()
			_ = server.Shutdown()
			return fmt.Errorf("serve failed: %w", err)
		}
		return nil
	case err := <-metricsErrCh:
		if err != nil {
			cancel()
			_ = server.Shutdown()
			return fmt.Errorf("metrics serve failed: %w", err)
		}
		return nil
	case sig := <-sigCh:
		logger.Printf("shutdown: received signal %s", sig)
	}

	cancel()
	if err := server.Shutdown(); err != nil && !errors.Is(err, os.ErrClosed) {
		logger.Printf("shutdown: close listener error: %v", err)
	}

	logger.Printf("shutdown: allowing active tunnels to finish for %s", cfg.ShutdownGrace)
	waitDone := make(chan bool, 1)
	go func() {
		waitDone <- server.Wait(cfg.ShutdownGrace)
	}()

	select {
	case done := <-waitDone:
		if done {
			logger.Printf("shutdown: all active tunnels finished")
		} else {
			logger.Printf("shutdown: grace period elapsed with active tunnels")
		}
	case sig := <-sigCh:
		logger.Printf("shutdown: second signal %s, exiting immediately", sig)
	}

	return nil
}
