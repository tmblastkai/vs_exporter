package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/expfmt"
	"istio.io/client-go/pkg/clientset/versioned"
	"k8s.io/client-go/kubernetes"

	"vs_exporter/internal/collector"
	"vs_exporter/internal/config"
	"vs_exporter/internal/kube"
	"vs_exporter/internal/productmetrics"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	cfgKube, err := kube.BuildConfig()
	if err != nil {
		log.Fatalf("failed to build Kubernetes configuration: %v", err)
	}

	istioClient, err := versioned.NewForConfig(cfgKube)
	if err != nil {
		log.Fatalf("failed to create Istio clientset: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(cfgKube)
	if err != nil {
		log.Fatalf("failed to create Kubernetes clientset: %v", err)
	}

	vsCollector := collector.NewVirtualServiceCollector(clientset, istioClient)
	prometheus.MustRegister(vsCollector)

	store := productmetrics.NewStore()
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go vsCollector.Run(ctx, cfg.VirtualServiceInterval)

	productScraper := productmetrics.NewScraper(
		clientset,
		httpClient,
		store,
		cfg.ProductMetrics.Interval,
		cfg.ProductMetrics.Port,
		cfg.ProductMetrics.Path,
		cfg.ProductMetrics.NamespaceSelector,
		cfg.ProductMetrics.PodSelector,
		log.Default(),
	)
	go productScraper.Run(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		encoder := expfmt.NewEncoder(&buf, expfmt.FmtText)

		metricFamilies, err := prometheus.DefaultGatherer.Gather()
		if err != nil {
			log.Printf("failed to gather Prometheus metrics: %v", err)
			http.Error(w, "failed to gather metrics", http.StatusInternalServerError)
			return
		}

		for _, family := range metricFamilies {
			if err := encoder.Encode(family); err != nil {
				log.Printf("failed to encode Prometheus metrics: %v", err)
				http.Error(w, "failed to encode metrics", http.StatusInternalServerError)
				return
			}
		}

		if err := store.WriteAll(&buf); err != nil {
			log.Printf("failed to render product metrics: %v", err)
			http.Error(w, "failed to render metrics", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", productmetrics.MetricsContentType)
		if _, err := w.Write(buf.Bytes()); err != nil {
			log.Printf("failed to write metrics response: %v", err)
		}
	})

	srv := &http.Server{
		Addr:    cfg.ListenAddress,
		Handler: mux,
	}
	internalSrv := &http.Server{
		Addr:    cfg.InternalMetricsAddress,
		Handler: promhttp.Handler(),
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("error while shutting down HTTP server: %v", err)
		}
		if err := internalSrv.Shutdown(shutdownCtx); err != nil {
			log.Printf("error while shutting down internal metrics server: %v", err)
		}
	}()

	go func() {
		if err := internalSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("internal metrics server error: %v", err)
		}
	}()

	log.Printf("serving metrics at %s/metrics", cfg.ListenAddress)
	log.Printf("serving Go runtime metrics at %s/metrics", cfg.InternalMetricsAddress)

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("HTTP server error: %v", err)
	}
}
