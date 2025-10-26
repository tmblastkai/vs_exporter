package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/client-go/kubernetes"

	"vs_exporter/internal/kube"
	"vs_exporter/internal/productmetrics"
)

const (
	defaultScrapePort = 1234
	defaultMetricsURI = "/metrics"
)

func main() {
	listenAddress := flag.String("listen-address", ":5678", "Address to expose aggregated metrics")
	interval := flag.Duration("interval", 5*time.Minute, "Interval between product metric refreshes")
	scrapePort := flag.Int("scrape-port", defaultScrapePort, "Pod port to scrape metrics from")
	metricsPath := flag.String("metrics-path", defaultMetricsURI, "Pod metrics HTTP path")
	flag.Parse()

	cfg, err := kube.BuildConfig()
	if err != nil {
		log.Fatalf("failed to build Kubernetes configuration: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("failed to create Kubernetes clientset: %v", err)
	}

	store := productmetrics.NewStore()
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	scraper := productmetrics.NewScraper(clientset, httpClient, store, *interval, *scrapePort, *metricsPath, log.Default())
	go scraper.Run(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", productmetrics.MetricsContentType)
		if err := store.WriteAll(w); err != nil {
			log.Printf("failed to render metrics: %v", err)
			http.Error(w, "failed to render metrics", http.StatusInternalServerError)
		}
	})

	server := &http.Server{
		Addr:    *listenAddress,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("error shutting down metrics server: %v", err)
		}
	}()

	log.Printf("serving aggregated metrics at %s/metrics", *listenAddress)

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("HTTP server error: %v", err)
	}
}
