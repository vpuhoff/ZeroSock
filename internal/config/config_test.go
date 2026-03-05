package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadValidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
server:
  listen_addr: "0.0.0.0:1080"
healthcheck:
  interval_ms: 5000
  timeout_ms: 2000
backends:
  api-pool:
    addresses:
      - "10.0.1.10:8080"
    healthcheck: {}
routes:
  "api.internal": "api-pool"
`

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.ListenAddr != "0.0.0.0:1080" {
		t.Fatalf("ListenAddr mismatch: %s", cfg.ListenAddr)
	}
	if len(cfg.Routes["api.internal"]) != 1 {
		t.Fatalf("route backend count mismatch")
	}
	if cfg.Routes["api.internal"][0] != "10.0.1.10:8080" {
		t.Fatalf("backend mismatch: %s", cfg.Routes["api.internal"][0])
	}
	if g := cfg.HostToGroup["api.internal"]; g != "api-pool" {
		t.Fatalf("HostToGroup mismatch: %s", g)
	}
	if len(cfg.BackendGroups) != 1 || cfg.BackendGroups[0].Name != "api-pool" {
		t.Fatalf("BackendGroups mismatch")
	}
}

func TestLoadTwoHostsSameGroup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
server:
  listen_addr: "0.0.0.0:1080"
backends:
  pool:
    addresses:
      - "10.0.1.10:8080"
      - "10.0.1.11:8080"
    healthcheck: {}
routes:
  "api.internal": "pool"
  "api.example.com": "pool"
`

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if len(cfg.Routes["api.internal"]) != 2 || len(cfg.Routes["api.example.com"]) != 2 {
		t.Fatalf("both hosts must have same two backends")
	}
	if cfg.HostToGroup["api.internal"] != "pool" || cfg.HostToGroup["api.example.com"] != "pool" {
		t.Fatalf("HostToGroup mismatch")
	}
}

func TestLoadRejectsUnknownGroupInRoutes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
server:
  listen_addr: "0.0.0.0:1080"
backends:
  pool:
    addresses:
      - "10.0.1.10:8080"
    healthcheck: {}
routes:
  "api.internal": "pool"
  "other": "nonexistent"
`

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatalf("Load() expected error for unknown group in routes")
	}
}

func TestLoadRejectsNonIPBackend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
server:
  listen_addr: "0.0.0.0:1080"
backends:
  api-pool:
    addresses:
      - "backend.local:8080"
    healthcheck: {}
routes:
  "api.internal": "api-pool"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatalf("Load() expected error for non-IP backend")
	}
}
