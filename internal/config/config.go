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
	defaultReadTimeout    = 10 * time.Second
	defaultWriteTimeout   = 10 * time.Second
	defaultIdleTimeout    = 5 * time.Minute
)

type Config struct {
	Server struct {
		ListenAddr       string `yaml:"listen_addr"`
		MaxConnections   int    `yaml:"max_connections"`
		MaxInflightDials int    `yaml:"max_inflight_dials"`
	} `yaml:"server"`

	Metrics struct {
		Enabled    *bool  `yaml:"enabled"`
		ListenAddr string `yaml:"listen_addr"`
	} `yaml:"metrics"`

	Healthcheck struct {
		IntervalMS int    `yaml:"interval_ms"`
		TimeoutMS  int    `yaml:"timeout_ms"`
		Path       string `yaml:"path"` // defaults for backend groups
	} `yaml:"healthcheck"`

	TCP struct {
		KeepAliveMS int `yaml:"keepalive_ms"`
	} `yaml:"tcp"`

	Timeouts struct {
		DialMS              int `yaml:"dial_ms"`
		ShutdownGracePeriod int `yaml:"shutdown_grace_period_ms"`
		ReadMS              int `yaml:"read_ms"`
		WriteMS             int `yaml:"write_ms"`
		IdleMS              int `yaml:"idle_ms"`
	} `yaml:"timeouts"`

	AutoDiscovery struct {
		Enabled *bool `yaml:"enabled"`
	} `yaml:"auto_discovery"`

	Backends map[string]BackendGroup `yaml:"backends"`
	Routes   map[string]string        `yaml:"routes"` // host -> group name
}

type BackendGroup struct {
	Addresses   []string `yaml:"addresses"`
	Healthcheck struct {
		IntervalMS int    `yaml:"interval_ms"`
		TimeoutMS  int    `yaml:"timeout_ms"`
		Path       string `yaml:"path"`
	} `yaml:"healthcheck"`
}

// BackendGroupConfig is the resolved group config passed to the health checker.
type BackendGroupConfig struct {
	Name      string
	Addresses []string
	Interval  time.Duration
	Timeout   time.Duration
	Path      string
}

type RuntimeConfig struct {
	ListenAddr         string
	MetricsEnabled     bool
	MetricsListenAddr  string
	AutoDiscoveryEnabled bool
	MaxConnections  int
	MaxInflightDials int
	DialTimeout     time.Duration
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	TCPKeepAlive    time.Duration
	ShutdownGrace   time.Duration
	Routes          map[string][]string            // host -> addresses (for router)
	BackendGroups   []BackendGroupConfig           // for health checker
	HostToGroup     map[string]string              // normalized host -> group name
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

// LoadRaw reads and parses config file into raw Config struct (no validation).
func LoadRaw(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	return &cfg, nil
}

// Save writes Config to file.
func Save(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// AddAutoDiscoveredRoute adds a new backend group and route for the given host and addresses.
// Group name is derived from host (e.g. "api.example.com" -> "auto-api-example-com").
// Returns true if added, false if route already exists.
func AddAutoDiscoveredRoute(cfg *Config, host string, addresses []string) (added bool, err error) {
	if cfg.Backends == nil {
		cfg.Backends = make(map[string]BackendGroup)
	}
	if cfg.Routes == nil {
		cfg.Routes = make(map[string]string)
	}

	hostKey := normalizeHost(host)
	if hostKey == "" {
		return false, errors.New("empty host")
	}
	if len(addresses) == 0 {
		return false, errors.New("no addresses")
	}

	if _, exists := cfg.Routes[hostKey]; exists {
		return false, nil
	}

	groupName := "auto-" + strings.ReplaceAll(strings.ReplaceAll(hostKey, ".", "-"), ":", "-")
	if _, exists := cfg.Backends[groupName]; exists {
		groupName = groupName + "-" + fmt.Sprintf("%d", len(cfg.Backends))
	}

	cfg.Backends[groupName] = BackendGroup{
		Addresses:   addresses,
		Healthcheck: struct {
			IntervalMS int    `yaml:"interval_ms"`
			TimeoutMS  int    `yaml:"timeout_ms"`
			Path       string `yaml:"path"`
		}{},
	}
	cfg.Routes[hostKey] = groupName
	return true, nil
}

func normalizeAndValidate(cfg *Config) (*RuntimeConfig, error) {
	if strings.TrimSpace(cfg.Server.ListenAddr) == "" {
		return nil, errors.New("server.listen_addr is required")
	}
	if _, err := net.ResolveTCPAddr("tcp", cfg.Server.ListenAddr); err != nil {
		return nil, fmt.Errorf("invalid server.listen_addr: %w", err)
	}

	metricsEnabled := true
	if cfg.Metrics.Enabled != nil {
		metricsEnabled = *cfg.Metrics.Enabled
	}
	metricsListenAddr := strings.TrimSpace(cfg.Metrics.ListenAddr)
	if metricsEnabled {
		if metricsListenAddr == "" {
			metricsListenAddr = "127.0.0.1:9090"
		}
		if _, err := net.ResolveTCPAddr("tcp", metricsListenAddr); err != nil {
			return nil, fmt.Errorf("invalid metrics.listen_addr: %w", err)
		}
	}

	defaultHealthInterval := durationFromMS(cfg.Healthcheck.IntervalMS, defaultHealthInterval)
	defaultHealthTimeout := durationFromMS(cfg.Healthcheck.TimeoutMS, defaultHealthTimeout)
	if defaultHealthTimeout > defaultHealthInterval {
		return nil, errors.New("healthcheck.timeout_ms must be <= healthcheck.interval_ms")
	}
	defaultHealthPath := strings.TrimSpace(cfg.Healthcheck.Path)
	if defaultHealthPath != "" && !strings.HasPrefix(defaultHealthPath, "/") {
		defaultHealthPath = "/" + defaultHealthPath
	}

	if len(cfg.Backends) == 0 {
		return nil, errors.New("backends must contain at least one group")
	}
	if len(cfg.Routes) == 0 {
		return nil, errors.New("routes must contain at least one host")
	}

	autoDiscoveryEnabled := false
	if cfg.AutoDiscovery.Enabled != nil && *cfg.AutoDiscovery.Enabled {
		autoDiscoveryEnabled = true
	}

	routes := make(map[string][]string, len(cfg.Routes))
	hostToGroup := make(map[string]string, len(cfg.Routes))
	var backendGroups []BackendGroupConfig

	for groupName, group := range cfg.Backends {
		groupName = strings.TrimSpace(groupName)
		if groupName == "" {
			return nil, errors.New("backends contains empty group name")
		}
		if len(group.Addresses) == 0 {
			return nil, fmt.Errorf("backends[%q] must contain at least one address", groupName)
		}

		normalizedAddrs := make([]string, 0, len(group.Addresses))
		for _, addr := range group.Addresses {
			addr = strings.TrimSpace(addr)
			if addr == "" {
				return nil, fmt.Errorf("backends[%q] contains empty address", groupName)
			}
			tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
			if err != nil {
				return nil, fmt.Errorf("invalid backend %q in group %q: %w", addr, groupName, err)
			}
			if tcpAddr.IP == nil || tcpAddr.IP.IsUnspecified() {
				return nil, fmt.Errorf("backend %q in group %q must use a concrete IP", addr, groupName)
			}
			normalizedAddrs = append(normalizedAddrs, net.JoinHostPort(tcpAddr.IP.String(), fmt.Sprintf("%d", tcpAddr.Port)))
		}

		interval := durationFromMS(group.Healthcheck.IntervalMS, defaultHealthInterval)
		timeout := durationFromMS(group.Healthcheck.TimeoutMS, defaultHealthTimeout)
		if timeout > interval {
			return nil, fmt.Errorf("backends[%q].healthcheck.timeout_ms must be <= interval_ms", groupName)
		}
		path := strings.TrimSpace(group.Healthcheck.Path)
		if path != "" && !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		if path == "" {
			path = defaultHealthPath
		}

		backendGroups = append(backendGroups, BackendGroupConfig{
			Name:      groupName,
			Addresses: normalizedAddrs,
			Interval:  interval,
			Timeout:   timeout,
			Path:      path,
		})
	}

	for host, groupName := range cfg.Routes {
		normalizedHost := normalizeHost(host)
		if normalizedHost == "" {
			return nil, errors.New("routes contains empty host key")
		}
		groupName = strings.TrimSpace(groupName)
		if groupName == "" {
			return nil, fmt.Errorf("routes[%q] has empty group name", host)
		}
		group, ok := cfg.Backends[groupName]
		if !ok {
			return nil, fmt.Errorf("routes[%q] references unknown group %q", host, groupName)
		}

		normalizedAddrs := make([]string, 0, len(group.Addresses))
		for _, addr := range group.Addresses {
			addr = strings.TrimSpace(addr)
			tcpAddr, _ := net.ResolveTCPAddr("tcp", addr)
			normalizedAddrs = append(normalizedAddrs, net.JoinHostPort(tcpAddr.IP.String(), fmt.Sprintf("%d", tcpAddr.Port)))
		}
		routes[normalizedHost] = normalizedAddrs
		hostToGroup[normalizedHost] = groupName
	}

	dialTimeout := durationFromMS(cfg.Timeouts.DialMS, defaultDialTimeout)
	readTimeout := durationFromMS(cfg.Timeouts.ReadMS, defaultReadTimeout)
	writeTimeout := durationFromMS(cfg.Timeouts.WriteMS, defaultWriteTimeout)
	idleTimeout := durationFromMS(cfg.Timeouts.IdleMS, defaultIdleTimeout)
	keepAlive := durationFromMS(cfg.TCP.KeepAliveMS, defaultTCPKeepAlive)
	shutdownGrace := durationFromMS(cfg.Timeouts.ShutdownGracePeriod, defaultShutdownGrace)

	return &RuntimeConfig{
		ListenAddr:          cfg.Server.ListenAddr,
		MetricsEnabled:      metricsEnabled,
		MetricsListenAddr:   metricsListenAddr,
		AutoDiscoveryEnabled: autoDiscoveryEnabled,
		MaxConnections:    cfg.Server.MaxConnections,
		MaxInflightDials:  cfg.Server.MaxInflightDials,
		DialTimeout:       dialTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		TCPKeepAlive:      keepAlive,
		ShutdownGrace:     shutdownGrace,
		Routes:            routes,
		BackendGroups:     backendGroups,
		HostToGroup:       hostToGroup,
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
