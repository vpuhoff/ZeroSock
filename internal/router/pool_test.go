package router

import (
	"testing"
)

func TestPickRoundRobin(t *testing.T) {
	r, err := New(map[string][]string{
		"api.internal": {"10.0.0.1:80", "10.0.0.2:80", "10.0.0.3:80"},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got := make([]string, 0, 6)
	for i := 0; i < 6; i++ {
		addr, err := r.Pick("api.internal")
		if err != nil {
			t.Fatalf("Pick() error = %v", err)
		}
		got = append(got, addr)
	}

	want := []string{
		"10.0.0.1:80", "10.0.0.2:80", "10.0.0.3:80",
		"10.0.0.1:80", "10.0.0.2:80", "10.0.0.3:80",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("round robin mismatch at %d: got=%s want=%s", i, got[i], want[i])
		}
	}
}

func TestPickSkipsDeadBackend(t *testing.T) {
	r, err := New(map[string][]string{
		"api.internal": {"10.0.0.1:80", "10.0.0.2:80"},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	changed, err := r.SetBackendAlive("api.internal", "10.0.0.1:80", false)
	if err != nil {
		t.Fatalf("SetBackendAlive() error = %v", err)
	}
	if !changed {
		t.Fatalf("SetBackendAlive() expected changed=true")
	}

	for i := 0; i < 3; i++ {
		addr, err := r.Pick("api.internal")
		if err != nil {
			t.Fatalf("Pick() error = %v", err)
		}
		if addr != "10.0.0.2:80" {
			t.Fatalf("Pick() expected alive backend, got=%s", addr)
		}
	}
}

func TestPickNoAliveBackends(t *testing.T) {
	r, err := New(map[string][]string{
		"api.internal": {"10.0.0.1:80"},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := r.SetBackendAlive("api.internal", "10.0.0.1:80", false); err != nil {
		t.Fatalf("SetBackendAlive() error = %v", err)
	}

	_, err = r.Pick("api.internal")
	if err == nil {
		t.Fatalf("Pick() expected error")
	}
	if err != ErrNoAliveBackends {
		t.Fatalf("Pick() wrong error: %v", err)
	}
}

func TestHostForBackendAddr(t *testing.T) {
	r, err := New(map[string][]string{
		"api.internal": {"10.0.1.10:8080", "10.0.1.11:8080"},
		"db.internal":  {"10.0.2.1:5432"},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	for _, tt := range []struct {
		addr   string
		wantHost string
		wantOK bool
	}{
		{"10.0.1.10:8080", "api.internal", true},
		{"10.0.1.11:8080", "api.internal", true},
		{"10.0.2.1:5432", "db.internal", true},
		{"10.0.1.10:8080", "api.internal", true},
		{"192.168.1.1:80", "", false},
		{"10.0.1.10:9999", "", false},
	} {
		host, ok := r.HostForBackendAddr(tt.addr)
		if ok != tt.wantOK || host != tt.wantHost {
			t.Errorf("HostForBackendAddr(%q) = %q, %v; want %q, %v", tt.addr, host, ok, tt.wantHost, tt.wantOK)
		}
	}
}

func TestHostForBackendAddrThenPick(t *testing.T) {
	r, err := New(map[string][]string{
		"svc": {"10.0.0.1:80", "10.0.0.2:80"},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	// Client sends IPv4 10.0.0.1:80 — resolve to host "svc", then Pick("svc") can return 10.0.0.1 or 10.0.0.2
	host, ok := r.HostForBackendAddr("10.0.0.1:80")
	if !ok || host != "svc" {
		t.Fatalf("HostForBackendAddr(10.0.0.1:80) = %q, %v; want svc, true", host, ok)
	}
	addr, err := r.Pick(host)
	if err != nil {
		t.Fatalf("Pick(svc) error = %v", err)
	}
	if addr != "10.0.0.1:80" && addr != "10.0.0.2:80" {
		t.Errorf("Pick(svc) = %s; want 10.0.0.1:80 or 10.0.0.2:80", addr)
	}
}
