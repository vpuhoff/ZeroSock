package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultHealthInterval = 5 * time.Second
	defaultHealthTimeout  = 2 * time.Second
	defaultDialTimeout    = 4 * time.Second
	defaultTCPKeepAlive   = 30 * time.Second
	defaultShutdownGrace  = 10 * time.Second
)

type Config struct {
	Server struct {
		ListenAddr string `yaml:"listen_addr"`
	} `yaml:"server"`

	Healthcheck struct {
		IntervalMS int `yaml:"interval_ms"`
		TimeoutMS  int `yaml:"timeout_ms"`
	} `yaml:"healthcheck"`

	TCP struct {
		KeepAliveMS int `yaml:"keepalive_ms"`
	} `yaml:"tcp"`

	Timeouts struct {
		DialMS              int `yaml:"dial_ms"`
		ShutdownGracePeriod int `yaml:"shutdown_grace_period_ms"`
	} `yaml:"timeouts"`

	Routes map[string][]string `yaml:"routes"`
}

type RuntimeConfig struct {
	ListenAddr          string
	HealthcheckInterval time.Duration
	HealthcheckTimeout  time.Duration
	DialTimeout         time.Duration
	TCPKeepAlive        time.Duration
	ShutdownGrace       time.Duration
	Routes              map[string][]string
}

func Load(path string) (*RuntimeConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	return normalizeAndValidate(&cfg)
}

func normalizeAndValidate(cfg *Config) (*RuntimeConfig, error) {
	if strings.TrimSpace(cfg.Server.ListenAddr) == "" {
		return nil, errors.New("server.listen_addr is required")
	}
	if _, err := net.ResolveTCPAddr("tcp", cfg.Server.ListenAddr); err != nil {
		return nil, fmt.Errorf("invalid server.listen_addr: %w", err)
	}

	routes := make(map[string][]string, len(cfg.Routes))
	if len(cfg.Routes) == 0 {
		return nil, errors.New("routes must contain at least one host")
	}

	for host, backends := range cfg.Routes {
		normalizedHost := normalizeHost(host)
		if normalizedHost == "" {
			return nil, errors.New("routes contains empty host key")
		}
		if len(backends) == 0 {
			return nil, fmt.Errorf("routes[%q] must contain at least one backend", host)
		}

		normalizedBackends := make([]string, 0, len(backends))
		for _, backend := range backends {
			addr := strings.TrimSpace(backend)
			if addr == "" {
				return nil, fmt.Errorf("routes[%q] contains empty backend address", host)
			}
			tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
			if err != nil {
				return nil, fmt.Errorf("invalid backend %q for host %q: %w", addr, host, err)
			}
			if tcpAddr.IP == nil || tcpAddr.IP.IsUnspecified() {
				return nil, fmt.Errorf("backend %q for host %q must use a concrete IP", addr, host)
			}

			normalizedBackends = append(normalizedBackends, net.JoinHostPort(tcpAddr.IP.String(), fmt.Sprintf("%d", tcpAddr.Port)))
		}

		routes[normalizedHost] = normalizedBackends
	}

	healthInterval := durationFromMS(cfg.Healthcheck.IntervalMS, defaultHealthInterval)
	healthTimeout := durationFromMS(cfg.Healthcheck.TimeoutMS, defaultHealthTimeout)
	if healthTimeout > healthInterval {
		return nil, errors.New("healthcheck.timeout_ms must be <= healthcheck.interval_ms")
	}

	dialTimeout := durationFromMS(cfg.Timeouts.DialMS, defaultDialTimeout)
	keepAlive := durationFromMS(cfg.TCP.KeepAliveMS, defaultTCPKeepAlive)
	shutdownGrace := durationFromMS(cfg.Timeouts.ShutdownGracePeriod, defaultShutdownGrace)

	return &RuntimeConfig{
		ListenAddr:          cfg.Server.ListenAddr,
		HealthcheckInterval: healthInterval,
		HealthcheckTimeout:  healthTimeout,
		DialTimeout:         dialTimeout,
		TCPKeepAlive:        keepAlive,
		ShutdownGrace:       shutdownGrace,
		Routes:              routes,
	}, nil
}

func durationFromMS(ms int, fallback time.Duration) time.Duration {
	if ms <= 0 {
		return fallback
	}
	return time.Duration(ms) * time.Millisecond
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimSuffix(host, ".")
	return host
}
