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
routes:
  "api.internal":
    - "10.0.1.10:8080"
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
}

func TestLoadRejectsNonIPBackend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
server:
  listen_addr: "0.0.0.0:1080"
routes:
  "api.internal":
    - "backend.local:8080"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatalf("Load() expected error for non-IP backend")
	}
}
