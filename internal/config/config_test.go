package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadValidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
listenAddress: ":8090"
internalMetricsAddress: ":9000"
virtualServiceInterval: "1m"
productMetrics:
  - name: product-a
    interval: "30s"
    port: 8080
    path: /metrics
    namespaceSelector: product=a
    podSelector: app=product-a
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.ListenAddress != ":8090" {
		t.Fatalf("expected listen address \":8090\", got %q", cfg.ListenAddress)
	}
	if cfg.InternalMetricsAddress != ":9000" {
		t.Fatalf("expected internal metrics address \":9000\", got %q", cfg.InternalMetricsAddress)
	}
	if cfg.VirtualServiceInterval != time.Minute {
		t.Fatalf("expected virtual service interval 1m, got %s", cfg.VirtualServiceInterval)
	}
	if len(cfg.ProductMetrics) != 1 {
		t.Fatalf("expected one product metrics target, got %d", len(cfg.ProductMetrics))
	}
	target := cfg.ProductMetrics[0]
	if target.Name != "product-a" {
		t.Fatalf("unexpected target name %q", target.Name)
	}
	if target.Interval != 30*time.Second {
		t.Fatalf("unexpected target interval %s", target.Interval)
	}
	if target.Port != 8080 {
		t.Fatalf("unexpected target port %d", target.Port)
	}
	if target.Path != "/metrics" {
		t.Fatalf("unexpected target path %q", target.Path)
	}
	if target.NamespaceSelector != "product=a" {
		t.Fatalf("unexpected namespace selector %q", target.NamespaceSelector)
	}
	if target.PodSelector != "app=product-a" {
		t.Fatalf("unexpected pod selector %q", target.PodSelector)
	}
}

func TestLoadInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
listenAddress: ":8090"
internalMetricsAddress: ":9000"
virtualServiceInterval: "0s"
productMetrics: []
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatalf("expected error when loading invalid config, got nil")
	}
}
