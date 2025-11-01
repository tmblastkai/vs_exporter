# vs_exporter

Prometheus exporter consolidating Istio VirtualService statistics and product service metrics into a single endpoint.

## Features
- Aggregates Istio VirtualService information using the official Istio clientset.
- Scrapes multiple product namespaces/pods according to configurable selectors, merges metrics with namespace labels.
- Exposes combined metrics via `/metrics` on a configurable port, with Go runtime metrics served separately.
- Configuration-driven via YAML file; supports multiple scrape targets.
- Structured logging implemented with logrus.

## Getting Started
### Prerequisites
- Go 1.21+
- Access to a Kubernetes cluster (for runtime usage)

### Installation
```bash
git clone <repo-url>
cd vs_exporter
```

### Configuration
Edit `config.yaml` (copy from `config.sample.yaml`):
```yaml
listenAddress: ":8081"
internalMetricsAddress: ":8123"
virtualServiceInterval: "5m"
productMetrics:
  - name: product-a
    interval: "5m"
    port: 1234
    path: /metrics
    namespaceSelector: product=alpha
    podSelector: product=alpha
```

### Running
```bash
go run ./cmd/vs-exporter --config=config.yaml
```

## Development
### Code Formatting
```bash
make fmt
```

> `make fmt` will also run `goimports`; the first invocation downloads the tool if needed.

### Unit Tests
```bash
make test
```

### Additional Targets
```bash
make vet        # go vet static analysis
make coverage   # generate coverage.out and show summary
make build      # produce ./bin/vs-exporter
```

### Full Workflow
```bash
make all
```

## License
MIT
