package metrics

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Collector struct {
	connectionsTotal  uint64
	connectionsActive int64

	mu                sync.Mutex
	connectionErrors  map[string]uint64
	requestsTotal     map[string]uint64
	routeFailures     map[string]uint64
	backendDialFailed map[string]uint64
	relayBytes        map[string]uint64
	healthchecks      map[string]uint64
	backendsAlive     map[string]bool

	handshakeCount uint64
	handshakeSumNs uint64
	dialCount      uint64
	dialSumNs      uint64
	sessionCount   uint64
	sessionSumNs   uint64
}

func NewCollector() *Collector {
	return &Collector{
		connectionErrors:  make(map[string]uint64),
		requestsTotal:     make(map[string]uint64),
		routeFailures:     make(map[string]uint64),
		backendDialFailed: make(map[string]uint64),
		relayBytes:        make(map[string]uint64),
		healthchecks:      make(map[string]uint64),
		backendsAlive:     make(map[string]bool),
	}
}

func (c *Collector) IncConnectionAccepted() {
	if c == nil {
		return
	}
	atomic.AddUint64(&c.connectionsTotal, 1)
	atomic.AddInt64(&c.connectionsActive, 1)
}

func (c *Collector) DecConnectionActive() {
	if c == nil {
		return
	}
	atomic.AddInt64(&c.connectionsActive, -1)
}

func (c *Collector) ObserveHandshakeLatency(d time.Duration) {
	if c == nil {
		return
	}
	atomic.AddUint64(&c.handshakeCount, 1)
	atomic.AddUint64(&c.handshakeSumNs, uint64(d.Nanoseconds()))
}

func (c *Collector) IncRequest(atyp string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.requestsTotal[atyp]++
	c.mu.Unlock()
}

func (c *Collector) IncConnectionError(stage string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.connectionErrors[stage]++
	c.mu.Unlock()
}

func (c *Collector) IncRouteFailure(host, reason string) {
	if c == nil {
		return
	}
	key := fmt.Sprintf("host=%s,reason=%s", host, reason)
	c.mu.Lock()
	c.routeFailures[key]++
	c.mu.Unlock()
}

func (c *Collector) ObserveBackendDialLatency(d time.Duration) {
	if c == nil {
		return
	}
	atomic.AddUint64(&c.dialCount, 1)
	atomic.AddUint64(&c.dialSumNs, uint64(d.Nanoseconds()))
}

func (c *Collector) IncBackendDialFailure(host, reason string) {
	if c == nil {
		return
	}
	key := fmt.Sprintf("host=%s,reason=%s", host, reason)
	c.mu.Lock()
	c.backendDialFailed[key]++
	c.mu.Unlock()
}

func (c *Collector) AddRelayBytes(direction string, n int64) {
	if c == nil || n <= 0 {
		return
	}
	c.mu.Lock()
	c.relayBytes[direction] += uint64(n)
	c.mu.Unlock()
}

func (c *Collector) ObserveSessionDuration(d time.Duration) {
	if c == nil {
		return
	}
	atomic.AddUint64(&c.sessionCount, 1)
	atomic.AddUint64(&c.sessionSumNs, uint64(d.Nanoseconds()))
}

func (c *Collector) ObserveHealthcheck(host, backend string, alive bool) {
	if c == nil {
		return
	}
	result := "dead"
	if alive {
		result = "alive"
	}

	key := fmt.Sprintf("host=%s,backend=%s,result=%s", host, backend, result)
	aliveKey := fmt.Sprintf("host=%s,backend=%s", host, backend)

	c.mu.Lock()
	c.healthchecks[key]++
	c.backendsAlive[aliveKey] = alive
	c.mu.Unlock()
}

func (c *Collector) RenderPrometheusText() string {
	if c == nil {
		return ""
	}

	var b strings.Builder
	writeCounter(&b, "zerosock_connections_total", float64(atomic.LoadUint64(&c.connectionsTotal)))
	writeGauge(&b, "zerosock_connections_active", float64(atomic.LoadInt64(&c.connectionsActive)))

	handCount := atomic.LoadUint64(&c.handshakeCount)
	handSum := atomic.LoadUint64(&c.handshakeSumNs)
	writeCounter(&b, "zerosock_handshake_latency_seconds_count", float64(handCount))
	writeCounter(&b, "zerosock_handshake_latency_seconds_sum", nsToSec(handSum))

	dialCount := atomic.LoadUint64(&c.dialCount)
	dialSum := atomic.LoadUint64(&c.dialSumNs)
	writeCounter(&b, "zerosock_backend_dial_latency_seconds_count", float64(dialCount))
	writeCounter(&b, "zerosock_backend_dial_latency_seconds_sum", nsToSec(dialSum))

	sessionCount := atomic.LoadUint64(&c.sessionCount)
	sessionSum := atomic.LoadUint64(&c.sessionSumNs)
	writeCounter(&b, "zerosock_session_duration_seconds_count", float64(sessionCount))
	writeCounter(&b, "zerosock_session_duration_seconds_sum", nsToSec(sessionSum))

	c.mu.Lock()
	defer c.mu.Unlock()

	writeLabelCounters(&b, "zerosock_connection_errors_total", "stage", c.connectionErrors)
	writeLabelCounters(&b, "zerosock_requests_total", "atyp", c.requestsTotal)
	writeKVLabelCounters(&b, "zerosock_route_failures_total", c.routeFailures)
	writeKVLabelCounters(&b, "zerosock_backend_dial_failures_total", c.backendDialFailed)
	writeLabelCounters(&b, "zerosock_relay_bytes_total", "direction", c.relayBytes)

	healthKeys := make([]string, 0, len(c.healthchecks))
	for k := range c.healthchecks {
		healthKeys = append(healthKeys, k)
	}
	sort.Strings(healthKeys)
	for _, k := range healthKeys {
		parts := parseKVLabels(k)
		b.WriteString(fmt.Sprintf("zerosock_healthchecks_total{%s} %d\n", formatLabels(parts), c.healthchecks[k]))
	}

	aliveKeys := make([]string, 0, len(c.backendsAlive))
	for k := range c.backendsAlive {
		aliveKeys = append(aliveKeys, k)
	}
	sort.Strings(aliveKeys)
	for _, k := range aliveKeys {
		parts := parseKVLabels(k)
		val := 0
		if c.backendsAlive[k] {
			val = 1
		}
		b.WriteString(fmt.Sprintf("zerosock_backend_alive{%s} %d\n", formatLabels(parts), val))
	}

	return b.String()
}

func writeCounter(b *strings.Builder, name string, value float64) {
	b.WriteString(fmt.Sprintf("%s %g\n", name, value))
}

func writeGauge(b *strings.Builder, name string, value float64) {
	b.WriteString(fmt.Sprintf("%s %g\n", name, value))
}

func writeLabelCounters(b *strings.Builder, metric, label string, m map[string]uint64) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString(fmt.Sprintf("%s{%s=%q} %d\n", metric, label, k, m[k]))
	}
}

func writeKVLabelCounters(b *strings.Builder, metric string, m map[string]uint64) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		labels := parseKVLabels(k)
		b.WriteString(fmt.Sprintf("%s{%s} %d\n", metric, formatLabels(labels), m[k]))
	}
}

func nsToSec(ns uint64) float64 {
	return float64(ns) / float64(time.Second)
}

func parseKVLabels(s string) map[string]string {
	out := make(map[string]string)
	parts := strings.Split(s, ",")
	for _, part := range parts {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		out[kv[0]] = kv[1]
	}
	return out
}

func formatLabels(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	chunks := make([]string, 0, len(keys))
	for _, k := range keys {
		chunks = append(chunks, fmt.Sprintf("%s=%q", k, m[k]))
	}
	return strings.Join(chunks, ",")
}
