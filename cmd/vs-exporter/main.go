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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/client-go/dynamic"

	"vs_exporter/internal/collector"
	"vs_exporter/internal/kube"
)

func main() {
	listenAddress := flag.String("listen-address", ":8080", "Address to listen on for HTTP requests")
	interval := flag.Duration("interval", 5*time.Minute, "Interval between VirtualService metric refreshes")
	flag.Parse()

	cfg, err := kube.BuildConfig()
	if err != nil {
		log.Fatalf("failed to build Kubernetes configuration: %v", err)
	}

	dynamicClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("failed to create dynamic Kubernetes client: %v", err)
	}

	vsCollector := collector.NewVirtualServiceCollector(dynamicClient)
	prometheus.MustRegister(vsCollector)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go vsCollector.Run(ctx, *interval)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	srv := &http.Server{
		Addr:    *listenAddress,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("error while shutting down HTTP server: %v", err)
		}
	}()

	log.Printf("serving metrics at %s/metrics", *listenAddress)

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("HTTP server error: %v", err)
	}
}
