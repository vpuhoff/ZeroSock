package metrics

import (
	"strings"
	"testing"
	"time"
)

func TestNewCollectorInitializesMaps(t *testing.T) {
	c := NewCollector()
	if c == nil {
		t.Fatal("NewCollector() returned nil")
	}

	if c.connectionErrors == nil ||
		c.requestsTotal == nil ||
		c.requestsByBackend == nil ||
		c.routeFailures == nil ||
		c.backendDialFailed == nil ||
		c.relayBytes == nil ||
		c.healthchecks == nil ||
		c.backendsAlive == nil ||
		c.tcpState == nil {
		t.Fatal("NewCollector() did not initialize all maps")
	}
}

func TestCollectorNilSafety(t *testing.T) {
	var c *Collector

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("collector nil receiver panicked: %v", r)
		}
	}()

	c.IncTCPState("syn")
	c.IncConnectionAccepted()
	c.DecConnectionActive()
	c.ObserveHandshakeLatency(10 * time.Millisecond)
	c.IncRequest("fqdn")
	c.IncRequestByBackend("", "", "")
	c.IncConnectionError("handshake")
	c.IncRouteFailure("api.internal", "route_not_found")
	c.ObserveBackendDialLatency(20 * time.Millisecond)
	c.IncBackendDialFailure("api.internal", "timeout")
	c.AddRelayBytes("client_to_backend", 42)
	c.ObserveSessionDuration(30 * time.Millisecond)
	c.ObserveHealthcheck("api.internal", "10.0.0.1:443", true)

	if got := c.RenderPrometheusText(); got != "" {
		t.Fatalf("RenderPrometheusText() on nil collector = %q; want empty string", got)
	}
}

func TestCollectorRenderPrometheusText(t *testing.T) {
	c := NewCollector()

	c.IncTCPState("syn")
	c.IncTCPState("syn")
	c.IncTCPState("established")
	c.IncTCPState("fin")
	c.IncTCPState("rst")
	c.IncConnectionAccepted()
	c.IncConnectionAccepted()
	c.DecConnectionActive()
	c.ObserveHandshakeLatency(150 * time.Millisecond)
	c.IncRequest("fqdn")
	c.IncRequestByBackend("api.internal", "10.0.0.1:443", "success")
	c.IncConnectionError("relay")
	c.IncRouteFailure("api.internal", "timeout")
	c.ObserveBackendDialLatency(200 * time.Millisecond)
	c.IncBackendDialFailure("api.internal", "timeout")
	c.AddRelayBytes("client_to_backend", 128)
	c.AddRelayBytes("client_to_backend", 64)
	c.AddRelayBytes("backend_to_client", 256)
	c.AddRelayBytes("ignored", 0)
	c.ObserveSessionDuration(2 * time.Second)

	got := c.RenderPrometheusText()

	for _, want := range []string{
		`zerosock_connections_total 2`,
		`zerosock_connections_active 1`,
		`zerosock_handshake_latency_seconds_count 1`,
		`zerosock_handshake_latency_seconds_sum 0.15`,
		`zerosock_backend_dial_latency_seconds_count 1`,
		`zerosock_backend_dial_latency_seconds_sum 0.2`,
		`zerosock_session_duration_seconds_count 1`,
		`zerosock_session_duration_seconds_sum 2`,
		`zerosock_tcp_state_total{state="syn"} 2`,
		`zerosock_tcp_state_total{state="established"} 1`,
		`zerosock_tcp_state_total{state="fin"} 1`,
		`zerosock_tcp_state_total{state="rst"} 1`,
		`zerosock_requests_total{atyp="fqdn"} 1`,
		`zerosock_requests_backend_total{backend="10.0.0.1:443",host="api.internal",result="success"} 1`,
		`zerosock_connection_errors_total{stage="relay"} 1`,
		`zerosock_route_failures_total{host="api.internal",reason="timeout"} 1`,
		`zerosock_backend_dial_failures_total{host="api.internal",reason="timeout"} 1`,
		`zerosock_relay_bytes_total{direction="backend_to_client"} 256`,
		`zerosock_relay_bytes_total{direction="client_to_backend"} 192`,
	} {
		assertContains(t, got, want)
	}
}

func TestCollectorObserveHealthcheck(t *testing.T) {
	c := NewCollector()

	c.ObserveHealthcheck("quay.io", "34.238.8.142:443", true)
	c.ObserveHealthcheck("quay.io", "34.238.8.142:443", false)

	got := c.RenderPrometheusText()

	assertContains(t, got, `zerosock_healthchecks_total{backend="34.238.8.142:443",host="quay.io",result="alive"} 1`)
	assertContains(t, got, `zerosock_healthchecks_total{backend="34.238.8.142:443",host="quay.io",result="dead"} 1`)
	assertContains(t, got, `zerosock_backend_alive{backend="34.238.8.142:443",host="quay.io"} 0`)
}

func TestCollectorIncRequestByBackendDefaults(t *testing.T) {
	c := NewCollector()

	c.IncRequestByBackend("", "", "")

	got := c.RenderPrometheusText()
	assertContains(t, got, `zerosock_requests_backend_total{backend="none",host="unknown",result="unknown"} 1`)
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("output missing %q\nfull output:\n%s", want, got)
	}
}
