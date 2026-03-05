package health

import (
	"context"
	"log"
	"net"
	"time"

	"zerosock/internal/router"
)

type Checker struct {
	router   *router.Router
	interval time.Duration
	timeout  time.Duration
	logger   *log.Logger
}

func New(r *router.Router, interval, timeout time.Duration, logger *log.Logger) *Checker {
	return &Checker{
		router:   r,
		interval: interval,
		timeout:  timeout,
		logger:   logger,
	}
}

func (c *Checker) Start(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	// First probe immediately so dead nodes are excluded quickly.
	c.probeAll()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.probeAll()
		}
	}
}

func (c *Checker) probeAll() {
	for _, state := range c.router.Snapshot() {
		alive := c.probe(state.Address)
		changed, err := c.router.SetBackendAlive(state.Host, state.Address, alive)
		if err != nil {
			c.logger.Printf("health: failed to update backend state host=%s backend=%s err=%v", state.Host, state.Address, err)
			continue
		}
		if changed {
			if alive {
				c.logger.Printf("health: backend alive host=%s backend=%s", state.Host, state.Address)
			} else {
				c.logger.Printf("health: backend dead host=%s backend=%s", state.Host, state.Address)
			}
		}
	}
}

func (c *Checker) probe(address string) bool {
	conn, err := net.DialTimeout("tcp", address, c.timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
