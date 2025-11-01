package config

import (
	"fmt"
	"os"
	"time"

	"sigs.k8s.io/yaml"
)

// Config 描述整體服務的設定項目。
type Config struct {
	ListenAddress          string
	InternalMetricsAddress string
	VirtualServiceInterval time.Duration
	ProductMetrics         []ProductMetricsTarget
}

// ProductMetricsTarget 敘述單一產品指標抓取器的參數。
type ProductMetricsTarget struct {
	Name              string
	Interval          time.Duration
	Port              int
	Path              string
	NamespaceSelector string
	PodSelector       string
}

type rawConfig struct {
	ListenAddress          string             `yaml:"listenAddress"`
	InternalMetricsAddress string             `yaml:"internalMetricsAddress"`
	VirtualServiceInterval string             `yaml:"virtualServiceInterval"`
	ProductMetrics         []rawProductTarget `yaml:"productMetrics"`
}

type rawProductTarget struct {
	Name              string `yaml:"name"`
	Interval          string `yaml:"interval"`
	Port              int    `yaml:"port"`
	Path              string `yaml:"path"`
	NamespaceSelector string `yaml:"namespaceSelector"`
	PodSelector       string `yaml:"podSelector"`
}

// Load 從指定路徑讀取設定。
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Config{}, fmt.Errorf("unmarshal config: %w", err)
	}

	cfg, err := convertRaw(raw)
	if err != nil {
		return Config{}, err
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func convertRaw(raw rawConfig) (Config, error) {
	cfg := Config{
		ListenAddress:          raw.ListenAddress,
		InternalMetricsAddress: raw.InternalMetricsAddress,
	}

	if raw.VirtualServiceInterval == "" {
		return Config{}, fmt.Errorf("virtualServiceInterval is required")
	}
	interval, err := time.ParseDuration(raw.VirtualServiceInterval)
	if err != nil {
		return Config{}, fmt.Errorf("parse virtualServiceInterval: %w", err)
	}
	cfg.VirtualServiceInterval = interval

	cfg.ProductMetrics = make([]ProductMetricsTarget, len(raw.ProductMetrics))
	for i, target := range raw.ProductMetrics {
		if target.Interval == "" {
			return Config{}, fmt.Errorf("productMetrics[%d].interval is required", i)
		}
		duration, err := time.ParseDuration(target.Interval)
		if err != nil {
			return Config{}, fmt.Errorf("parse productMetrics[%d].interval: %w", i, err)
		}
		cfg.ProductMetrics[i] = ProductMetricsTarget{
			Name:              target.Name,
			Interval:          duration,
			Port:              target.Port,
			Path:              target.Path,
			NamespaceSelector: target.NamespaceSelector,
			PodSelector:       target.PodSelector,
		}
	}

	return cfg, nil
}

func (c Config) validate() error {
	if c.ListenAddress == "" {
		return fmt.Errorf("listenAddress is required")
	}
	if c.InternalMetricsAddress == "" {
		return fmt.Errorf("internalMetricsAddress is required")
	}
	if c.VirtualServiceInterval <= 0 {
		return fmt.Errorf("virtualServiceInterval must be positive")
	}
	if len(c.ProductMetrics) == 0 {
		return fmt.Errorf("productMetrics must contain at least one target")
	}

	for i, target := range c.ProductMetrics {
		if target.Name == "" {
			return fmt.Errorf("productMetrics[%d].name is required", i)
		}
		if target.Interval <= 0 {
			return fmt.Errorf("productMetrics[%d].interval must be positive", i)
		}
		if target.Port <= 0 {
			return fmt.Errorf("productMetrics[%d].port must be positive", i)
		}
		if target.Path == "" {
			return fmt.Errorf("productMetrics[%d].path is required", i)
		}
		if target.NamespaceSelector == "" {
			return fmt.Errorf("productMetrics[%d].namespaceSelector is required", i)
		}
		if target.PodSelector == "" {
			return fmt.Errorf("productMetrics[%d].podSelector is required", i)
		}
	}

	return nil
}
