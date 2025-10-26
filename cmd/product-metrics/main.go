package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/golang/protobuf/proto"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"vs_exporter/internal/kube"
)

const (
	namespaceLabelKey = "namespace"
	productLabel      = "product"
	defaultScrapePort = 1234
	defaultMetricsURI = "/metrics"
)

type metricsStore struct {
	mu       sync.RWMutex
	families map[string]*dto.MetricFamily
}

func newMetricsStore() *metricsStore {
	return &metricsStore{
		families: make(map[string]*dto.MetricFamily),
	}
}

func (s *metricsStore) replace(all map[string]*dto.MetricFamily) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.families = all
}

func (s *metricsStore) writeAll(w io.Writer) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.families) == 0 {
		return nil
	}

	encoder := expfmt.NewEncoder(w, expfmt.FmtText)
	names := make([]string, 0, len(s.families))
	for name := range s.families {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if err := encoder.Encode(s.families[name]); err != nil {
			return fmt.Errorf("encode metric family %s: %w", name, err)
		}
	}
	return nil
}

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

	store := newMetricsStore()
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go runScraper(ctx, clientset, store, httpClient, *interval, *scrapePort, *metricsPath)

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", string(expfmt.FmtText))
		if err := store.writeAll(w); err != nil {
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

func runScraper(
	ctx context.Context,
	clientset *kubernetes.Clientset,
	store *metricsStore,
	httpClient *http.Client,
	interval time.Duration,
	port int,
	metricsPath string,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := scrapeOnce(ctx, clientset, store, httpClient, port, metricsPath); err != nil {
			log.Printf("scrape failed: %v", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func scrapeOnce(
	ctx context.Context,
	clientset *kubernetes.Clientset,
	store *metricsStore,
	httpClient *http.Client,
	port int,
	metricsPath string,
) error {
	nsList, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{LabelSelector: productLabel})
	if err != nil {
		return fmt.Errorf("list namespaces: %w", err)
	}

	newFamilies := make(map[string]*dto.MetricFamily)
	var errs []error

	for _, ns := range nsList.Items {
		pods, err := clientset.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{LabelSelector: productLabel})
		if err != nil {
			errs = append(errs, fmt.Errorf("list pods in namespace %s: %w", ns.Name, err))
			continue
		}

		for i := range pods.Items {
			pod := &pods.Items[i]
			if pod.Status.PodIP == "" {
				continue
			}
			if err := scrapePod(ctx, httpClient, pod, ns.Name, port, metricsPath, newFamilies); err != nil {
				errs = append(errs, fmt.Errorf("scrape pod %s/%s: %w", ns.Name, pod.Name, err))
			}
		}
	}

	store.replace(newFamilies)

	return errors.Join(errs...)
}

func scrapePod(
	ctx context.Context,
	httpClient *http.Client,
	pod *corev1.Pod,
	namespace string,
	port int,
	metricsPath string,
	accumulator map[string]*dto.MetricFamily,
) error {
	url := fmt.Sprintf("http://%s:%d%s", pod.Status.PodIP, port, metricsPath)

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	parser := expfmt.TextParser{}
	parsed, err := parser.TextToMetricFamilies(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("parse metrics: %w", err)
	}

	for name, family := range parsed {
		withLabel := cloneAndLabelFamily(family, namespace)
		if existing, ok := accumulator[name]; ok {
			existing.Metric = append(existing.Metric, withLabel.Metric...)
		} else {
			accumulator[name] = withLabel
		}
	}

	return nil
}

func cloneAndLabelFamily(family *dto.MetricFamily, namespace string) *dto.MetricFamily {
	clone := proto.Clone(family).(*dto.MetricFamily)
	for _, metric := range clone.Metric {
		var hasNamespace bool
		for _, label := range metric.Label {
			if label.GetName() == namespaceLabelKey {
				label.Value = proto.String(namespace)
				hasNamespace = true
				break
			}
		}
		if !hasNamespace {
			metric.Label = append(metric.Label, &dto.LabelPair{
				Name:  proto.String(namespaceLabelKey),
				Value: proto.String(namespace),
			})
		}
	}
	return clone
}
