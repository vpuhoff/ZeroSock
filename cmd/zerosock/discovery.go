package main

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync"

	"zerosock/internal/config"
)

// Discoverer resolves unknown hosts, adds them to config, and triggers reload.
type Discoverer interface {
	// Discover tries to add host to config. For FQDN: resolves to IPs. For ip:port: adds as-is.
	// Returns true if route was added and reload was triggered, false if already exists or error.
	Discover(host string, port uint16) (added bool, err error)
}

type discoverer struct {
	configPath   string
	reloadFn     func() error
	logger       *log.Logger
	mu           sync.Mutex
	discovering  map[string]struct{}
}

func newDiscoverer(configPath string, reloadFn func() error, logger *log.Logger) *discoverer {
	return &discoverer{
		configPath:  configPath,
		reloadFn:    reloadFn,
		logger:      logger,
		discovering: make(map[string]struct{}),
	}
}

func (d *discoverer) Discover(host string, port uint16) (added bool, err error) {
	key := fmt.Sprintf("%s:%d", host, port)
	d.mu.Lock()
	if _, inProgress := d.discovering[key]; inProgress {
		d.mu.Unlock()
		return false, nil
	}
	d.discovering[key] = struct{}{}
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		delete(d.discovering, key)
		d.mu.Unlock()
	}()

	addresses, err := d.resolve(host, port)
	if err != nil {
		return false, err
	}
	if len(addresses) == 0 {
		return false, fmt.Errorf("no addresses for %s", host)
	}

	cfg, err := config.LoadRaw(d.configPath)
	if err != nil {
		return false, fmt.Errorf("load config: %w", err)
	}

	routeHost := host
	if ip := net.ParseIP(strings.TrimSpace(host)); ip != nil {
		routeHost = fmt.Sprintf("%s:%d", ip.String(), port)
	}

	added, err = config.AddAutoDiscoveredRoute(cfg, routeHost, addresses)
	if err != nil {
		return false, err
	}
	if !added {
		return false, nil
	}

	if err := config.Save(d.configPath, cfg); err != nil {
		return false, fmt.Errorf("save config: %w", err)
	}

	if err := d.reloadFn(); err != nil {
		return false, fmt.Errorf("reload after discovery: %w", err)
	}
	if d.logger != nil {
		d.logger.Printf("auto-discovery: added %q -> %d addresses", routeHost, len(addresses))
	}
	return true, nil
}

func (d *discoverer) resolve(host string, port uint16) ([]string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, fmt.Errorf("empty host")
	}

	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
		return []string{fmt.Sprintf("%s:%d", ip.String(), port)}, nil
	}

	ips, err := net.LookupHost(host)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", host, err)
	}

	addrs := make([]string, 0, len(ips))
	for _, ip := range ips {
		parsed := net.ParseIP(ip)
		if parsed == nil || parsed.To4() == nil {
			continue
		}
		addrs = append(addrs, fmt.Sprintf("%s:%d", parsed.String(), port))
	}
	return addrs, nil
}
