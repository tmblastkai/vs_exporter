package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/expfmt"
	"github.com/sirupsen/logrus"
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

	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})
	appLogger := logrus.WithField("component", "vs-exporter")

	cfg, err := config.Load(*configPath)
	if err != nil {
		appLogger.Fatalf("failed to load config: %v", err)
	}

	cfgKube, err := kube.BuildConfig()
	if err != nil {
		appLogger.Fatalf("failed to build Kubernetes configuration: %v", err)
	}

	var istioClient *versioned.Clientset
	if cfg.EnableVirtualServiceScrapeJob {
		istioClient, err = versioned.NewForConfig(cfgKube)
		if err != nil {
			appLogger.Fatalf("failed to create Istio clientset: %v", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(cfgKube)
	if err != nil {
		appLogger.Fatalf("failed to create Kubernetes clientset: %v", err)
	}

	var vsCollector *collector.VirtualServiceCollector
	if cfg.EnableVirtualServiceScrapeJob {
		vsCollector = collector.NewVirtualServiceCollector(clientset, istioClient)
		prometheus.MustRegister(vsCollector)
	}

	store := productmetrics.NewStore()
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if cfg.EnableVirtualServiceScrapeJob {
		go vsCollector.Run(ctx, cfg.VirtualServiceInterval)
	}

	if len(cfg.ProductMetrics) == 0 {
		appLogger.Warn("no product metrics targets configured; exposing only existing metrics")
	} else {
		for _, target := range cfg.ProductMetrics {
			appLogger.WithField("target", target.Name).Infof("configuring product metrics scraper interval=%s port=%d path=%s namespaceSelector=%q podSelector=%q",
				target.Interval, target.Port, target.Path, target.NamespaceSelector, target.PodSelector)

			scraperLogger := logrus.WithFields(logrus.Fields{
				"component": "product-scraper",
				"target":    target.Name,
			})

			scraper := productmetrics.NewScraper(
				target.Name,
				clientset,
				httpClient,
				store,
				target.Interval,
				target.Port,
				target.Path,
				target.NamespaceSelector,
				target.PodSelector,
				scraperLogger,
			)
			go scraper.Run(ctx)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		encoder := expfmt.NewEncoder(&buf, expfmt.FmtText)

		metricFamilies, err := prometheus.DefaultGatherer.Gather()
		if err != nil {
			appLogger.Errorf("failed to gather Prometheus metrics: %v", err)
			http.Error(w, "failed to gather metrics", http.StatusInternalServerError)
			return
		}

		for _, family := range metricFamilies {
			if err := encoder.Encode(family); err != nil {
				appLogger.Errorf("failed to encode Prometheus metrics: %v", err)
				http.Error(w, "failed to encode metrics", http.StatusInternalServerError)
				return
			}
		}

		if err := store.WriteAll(&buf); err != nil {
			appLogger.Errorf("failed to render product metrics: %v", err)
			http.Error(w, "failed to render metrics", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", productmetrics.MetricsContentType)
		if _, err := w.Write(buf.Bytes()); err != nil {
			appLogger.Warnf("failed to write metrics response: %v", err)
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
			appLogger.Warnf("error while shutting down HTTP server: %v", err)
		}
		if err := internalSrv.Shutdown(shutdownCtx); err != nil {
			appLogger.Warnf("error while shutting down internal metrics server: %v", err)
		}
	}()

	go func() {
		if err := internalSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			appLogger.Errorf("internal metrics server error: %v", err)
		}
	}()

	appLogger.Infof("serving metrics at %s/metrics", cfg.ListenAddress)
	appLogger.Infof("serving Go runtime metrics at %s/metrics", cfg.InternalMetricsAddress)

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		appLogger.Fatalf("HTTP server error: %v", err)
	}
}
