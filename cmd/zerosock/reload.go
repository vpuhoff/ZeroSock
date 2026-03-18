package main

import (
	"context"
	"fmt"
	"log"
	"sync"

	"zerosock/internal/config"
	"zerosock/internal/health"
	"zerosock/internal/metrics"
	"zerosock/internal/router"
	"zerosock/internal/socks"
)

type runtimeState struct {
	mu            sync.RWMutex
	ctx           context.Context
	logger        *log.Logger
	server        *socks.Server
	metrics       *metrics.Collector
	cfg           *config.RuntimeConfig
	checkerCancel context.CancelFunc
}

func newRuntimeState(
	ctx context.Context,
	logger *log.Logger,
	server *socks.Server,
	collector *metrics.Collector,
	cfg *config.RuntimeConfig,
	checkerCancel context.CancelFunc,
) *runtimeState {
	return &runtimeState{
		ctx:           ctx,
		logger:        logger,
		server:        server,
		metrics:       collector,
		cfg:           cfg,
		checkerCancel: checkerCancel,
	}
}

func buildReloadTarget(configPath string, logger *log.Logger, collector *metrics.Collector) (*config.RuntimeConfig, *router.Router, *health.Checker, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("config error: %w", err)
	}

	rt, err := router.New(cfg.Routes)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("router init error: %w", err)
	}

	checker := health.New(rt, cfg.BackendGroups, cfg.HostToGroup, logger, collector)
	return cfg, rt, checker, nil
}

func startChecker(ctx context.Context, checker *health.Checker) context.CancelFunc {
	checkerCtx, cancel := context.WithCancel(ctx)
	go checker.Start(checkerCtx)
	return cancel
}

func (r *runtimeState) currentConfig() *config.RuntimeConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg
}

func (r *runtimeState) stopChecker() {
	r.mu.Lock()
	cancel := r.checkerCancel
	r.checkerCancel = nil
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (r *runtimeState) reload(configPath string) error {
	nextCfg, nextRouter, nextChecker, err := buildReloadTarget(configPath, r.logger, r.metrics)
	if err != nil {
		return err
	}

	r.mu.Lock()
	currentCfg := r.cfg
	appliedCfg, warnings := applyReloadPolicy(currentCfg, nextCfg)
	nextCancel := startChecker(r.ctx, nextChecker)
	oldCancel := r.checkerCancel
	r.server.UpdateRouter(nextRouter)
	r.cfg = appliedCfg
	r.checkerCancel = nextCancel
	r.mu.Unlock()

	for _, warning := range warnings {
		r.logger.Printf("reload: %s", warning)
	}
	if oldCancel != nil {
		oldCancel()
	}

	r.logger.Printf("reload: config applied from %s", configPath)
	return nil
}

func applyReloadPolicy(currentCfg, nextCfg *config.RuntimeConfig) (*config.RuntimeConfig, []string) {
	applied := *nextCfg
	var warnings []string

	if nextCfg.ListenAddr != currentCfg.ListenAddr {
		warnings = append(warnings, fmt.Sprintf("server.listen_addr changed (%s -> %s), keeping current listener until restart", currentCfg.ListenAddr, nextCfg.ListenAddr))
		applied.ListenAddr = currentCfg.ListenAddr
	}
	if nextCfg.MetricsListenAddr != currentCfg.MetricsListenAddr {
		warnings = append(warnings, fmt.Sprintf("metrics.listen_addr changed (%s -> %s), keeping current listener until restart", currentCfg.MetricsListenAddr, nextCfg.MetricsListenAddr))
		applied.MetricsListenAddr = currentCfg.MetricsListenAddr
	}
	if nextCfg.MetricsEnabled != currentCfg.MetricsEnabled {
		warnings = append(warnings, fmt.Sprintf("metrics.enabled changed (%t -> %t), keeping current metrics process state until restart", currentCfg.MetricsEnabled, nextCfg.MetricsEnabled))
		applied.MetricsEnabled = currentCfg.MetricsEnabled
	}
	if nextCfg.MaxConnections != currentCfg.MaxConnections {
		warnings = append(warnings, "server.max_connections changed, keeping current limit until restart")
		applied.MaxConnections = currentCfg.MaxConnections
	}
	if nextCfg.MaxInflightDials != currentCfg.MaxInflightDials {
		warnings = append(warnings, "server.max_inflight_dials changed, keeping current limit until restart")
		applied.MaxInflightDials = currentCfg.MaxInflightDials
	}
	if nextCfg.DialTimeout != currentCfg.DialTimeout {
		warnings = append(warnings, "timeouts.dial_ms changed, keeping current dial timeout until restart")
		applied.DialTimeout = currentCfg.DialTimeout
	}
	if nextCfg.ReadTimeout != currentCfg.ReadTimeout {
		warnings = append(warnings, "timeouts.read_ms changed, keeping current read timeout until restart")
		applied.ReadTimeout = currentCfg.ReadTimeout
	}
	if nextCfg.WriteTimeout != currentCfg.WriteTimeout {
		warnings = append(warnings, "timeouts.write_ms changed, keeping current write timeout until restart")
		applied.WriteTimeout = currentCfg.WriteTimeout
	}
	if nextCfg.IdleTimeout != currentCfg.IdleTimeout {
		warnings = append(warnings, "timeouts.idle_ms changed, keeping current idle timeout until restart")
		applied.IdleTimeout = currentCfg.IdleTimeout
	}
	if nextCfg.TCPKeepAlive != currentCfg.TCPKeepAlive {
		warnings = append(warnings, "tcp.keepalive_ms changed, keeping current TCP keepalive until restart")
		applied.TCPKeepAlive = currentCfg.TCPKeepAlive
	}

	return &applied, warnings
}
