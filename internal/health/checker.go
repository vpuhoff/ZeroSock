package health

import (
	"context"
	"log"
	"net"
	"net/http"
	"time"

	"zerosock/internal/config"
	"zerosock/internal/metrics"
	"zerosock/internal/router"
)

type Checker struct {
	router      *router.Router
	groups      []config.BackendGroupConfig
	groupToHosts map[string][]string // group name -> hosts that use this group
	logger      *log.Logger
	metrics     *metrics.Collector
}

func New(r *router.Router, groups []config.BackendGroupConfig, hostToGroup map[string]string, logger *log.Logger, m *metrics.Collector) *Checker {
	groupToHosts := make(map[string][]string)
	for host, groupName := range hostToGroup {
		groupToHosts[groupName] = append(groupToHosts[groupName], host)
	}
	return &Checker{
		router:      r,
		groups:      groups,
		groupToHosts: groupToHosts,
		logger:      logger,
		metrics:     m,
	}
}

func (c *Checker) Start(ctx context.Context) {
	for i := range c.groups {
		group := &c.groups[i]
		hosts := c.groupToHosts[group.Name]
		for _, addr := range group.Addresses {
			addr := addr
			if group.Path == "" {
				go c.runL4Worker(ctx, group, addr, hosts)
			} else {
				go c.runL7Worker(ctx, group, addr, hosts)
			}
		}
	}
	<-ctx.Done()
}

func (c *Checker) runL4Worker(ctx context.Context, group *config.BackendGroupConfig, address string, hosts []string) {
	ticker := time.NewTicker(group.Interval)
	defer ticker.Stop()

	probe := func() {
		alive := c.probeL4(address, group.Timeout)
		for _, host := range hosts {
			c.metrics.ObserveHealthcheck(host, address, alive)
			changed, err := c.router.SetBackendAlive(host, address, alive)
			if err != nil {
				c.logger.Printf("health: failed to update backend state host=%s backend=%s err=%v", host, address, err)
				continue
			}
			if changed {
				if alive {
					c.logger.Printf("health: backend alive host=%s backend=%s", host, address)
				} else {
					c.logger.Printf("health: backend dead host=%s backend=%s", host, address)
				}
			}
		}
	}

	probe()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			probe()
		}
	}
}

func (c *Checker) runL7Worker(ctx context.Context, group *config.BackendGroupConfig, address string, hosts []string) {
	client := &http.Client{
		Timeout: group.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	url := "http://" + address + group.Path
	ticker := time.NewTicker(group.Interval)
	defer ticker.Stop()

	probe := func() {
		alive := c.probeL7(client, url)
		for _, host := range hosts {
			c.metrics.ObserveHealthcheck(host, address, alive)
			changed, err := c.router.SetBackendAlive(host, address, alive)
			if err != nil {
				c.logger.Printf("health: failed to update backend state host=%s backend=%s err=%v", host, address, err)
				continue
			}
			if changed {
				if alive {
					c.logger.Printf("health: backend alive host=%s backend=%s (L7 %s)", host, address, group.Path)
				} else {
					c.logger.Printf("health: backend dead host=%s backend=%s (L7 %s)", host, address, group.Path)
				}
			}
		}
	}

	probe()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			probe()
		}
	}
}

func (c *Checker) probeL7(client *http.Client, url string) bool {
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (c *Checker) probeL4(address string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
