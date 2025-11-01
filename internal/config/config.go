package config

import (
	"fmt"
	"os"
	"time"

	"sigs.k8s.io/yaml"
)

const (
	defaultListenAddress          = ":8081"
	defaultInternalMetricsAddress = ":8123"
	defaultScrapePort             = 1234
	defaultMetricsPath            = "/metrics"
	defaultNamespaceSelector      = "product"
	defaultPodSelector            = "product"
	defaultVirtualServiceInterval = 5 * time.Minute
	defaultProductInterval        = 5 * time.Minute
)

// Config 描述整體服務的設定項目。
type Config struct {
	ListenAddress          string               `yaml:"listenAddress"`
	InternalMetricsAddress string               `yaml:"internalMetricsAddress"`
	VirtualServiceInterval time.Duration        `yaml:"virtualServiceInterval"`
	ProductMetrics         ProductMetricsConfig `yaml:"productMetrics"`
}

// ProductMetricsConfig 敘述產品指標抓取相關的參數。
type ProductMetricsConfig struct {
	Interval          time.Duration `yaml:"interval"`
	Port              int           `yaml:"port"`
	Path              string        `yaml:"path"`
	NamespaceSelector string        `yaml:"namespaceSelector"`
	PodSelector       string        `yaml:"podSelector"`
}

// Load 從指定路徑讀取設定並補齊預設值。
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("unmarshal config: %w", err)
	}

	cfg.applyDefaults()
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.ListenAddress == "" {
		c.ListenAddress = defaultListenAddress
	}
	if c.InternalMetricsAddress == "" {
		c.InternalMetricsAddress = defaultInternalMetricsAddress
	}
	if c.VirtualServiceInterval == 0 {
		c.VirtualServiceInterval = defaultVirtualServiceInterval
	}

	if c.ProductMetrics.Interval == 0 {
		c.ProductMetrics.Interval = defaultProductInterval
	}
	if c.ProductMetrics.Port == 0 {
		c.ProductMetrics.Port = defaultScrapePort
	}
	if c.ProductMetrics.Path == "" {
		c.ProductMetrics.Path = defaultMetricsPath
	}
	if c.ProductMetrics.NamespaceSelector == "" {
		c.ProductMetrics.NamespaceSelector = defaultNamespaceSelector
	}
	if c.ProductMetrics.PodSelector == "" {
		c.ProductMetrics.PodSelector = defaultPodSelector
	}
}
