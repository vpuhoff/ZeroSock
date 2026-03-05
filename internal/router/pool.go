package router

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
)

var (
	ErrRouteNotFound   = errors.New("route not found")
	ErrNoAliveBackends = errors.New("no alive backends")
)

type backend struct {
	address string
	alive   uint32
}

type pool struct {
	backends []*backend
	rr       uint64
}

type Router struct {
	pools map[string]*pool
}

type BackendState struct {
	Host    string
	Address string
	Alive   bool
}

func New(routes map[string][]string) (*Router, error) {
	if len(routes) == 0 {
		return nil, errors.New("empty routes")
	}

	pools := make(map[string]*pool, len(routes))
	for host, addresses := range routes {
		hostKey := normalizeHost(host)
		if hostKey == "" {
			return nil, errors.New("empty route host")
		}
		if len(addresses) == 0 {
			return nil, fmt.Errorf("route %q has no backends", host)
		}

		bks := make([]*backend, 0, len(addresses))
		for _, addr := range addresses {
			b := &backend{address: addr}
			atomic.StoreUint32(&b.alive, 1)
			bks = append(bks, b)
		}

		pools[hostKey] = &pool{backends: bks}
	}

	return &Router{pools: pools}, nil
}

func (r *Router) Pick(host string) (string, error) {
	p, ok := r.pools[normalizeHost(host)]
	if !ok {
		return "", ErrRouteNotFound
	}

	n := len(p.backends)
	start := atomic.AddUint64(&p.rr, 1) - 1
	for i := 0; i < n; i++ {
		idx := int((start + uint64(i)) % uint64(n))
		b := p.backends[idx]
		if atomic.LoadUint32(&b.alive) == 1 {
			return b.address, nil
		}
	}

	return "", ErrNoAliveBackends
}

func (r *Router) SetBackendAlive(host, address string, alive bool) (bool, error) {
	p, ok := r.pools[normalizeHost(host)]
	if !ok {
		return false, ErrRouteNotFound
	}

	for _, b := range p.backends {
		if b.address != address {
			continue
		}
		var newVal uint32
		if alive {
			newVal = 1
		}
		prev := atomic.SwapUint32(&b.alive, newVal)
		return prev != newVal, nil
	}
	return false, fmt.Errorf("backend %q not found for host %q", address, host)
}

func (r *Router) Snapshot() []BackendState {
	states := make([]BackendState, 0)
	for host, p := range r.pools {
		for _, b := range p.backends {
			states = append(states, BackendState{
				Host:    host,
				Address: b.address,
				Alive:   atomic.LoadUint32(&b.alive) == 1,
			})
		}
	}
	return states
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimSuffix(host, ".")
	return host
}
