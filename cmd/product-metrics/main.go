package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"sort"
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

func main() {
	listenAddress := flag.String("listen-address", ":1234", "Address to listen on for HTTP requests")
	scrapePort := flag.Int("scrape-port", 1234, "Pod port to scrape metrics from")
	scrapePath := flag.String("scrape-path", "/metrics", "Pod path to scrape metrics from")
	scrapeTimeout := flag.Duration("scrape-timeout", 5*time.Second, "Timeout for scraping metrics from a single pod")
	flag.Parse()

	cfg, err := kube.BuildConfig()
	if err != nil {
		log.Fatalf("failed to build Kubernetes configuration: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("failed to create Kubernetes clientset: %v", err)
	}

	handler := &aggregatorHandler{
		clientset:         clientset,
		httpClient:        &http.Client{},
		scrapePort:        *scrapePort,
		scrapePath:        *scrapePath,
		scrapeTimeout:     *scrapeTimeout,
		namespaceSelector: "product",
		podSelector:       "product",
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", handler)

	server := &http.Server{
		Addr:    *listenAddress,
		Handler: mux,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("error while shutting down HTTP server: %v", err)
		}
	}()

	log.Printf("serving aggregated pod metrics at %s/metrics", *listenAddress)

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("HTTP server error: %v", err)
	}
}

type aggregatorHandler struct {
	clientset         kubernetes.Interface
	httpClient        *http.Client
	scrapePort        int
	scrapePath        string
	scrapeTimeout     time.Duration
	namespaceSelector string
	podSelector       string
}

func (h *aggregatorHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	namespaces, err := h.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{LabelSelector: h.namespaceSelector})
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to list namespaces: %v", err), http.StatusInternalServerError)
		return
	}

	merged := make(map[string]*dto.MetricFamily)

	for _, ns := range namespaces.Items {
		pods, err := h.clientset.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{LabelSelector: h.podSelector})
		if err != nil {
			log.Printf("failed to list pods in namespace %s: %v", ns.Name, err)
			continue
		}

		for _, pod := range pods.Items {
			if !isScrapablePod(&pod) {
				continue
			}

			families, err := h.scrapePodMetrics(ctx, &pod)
			if err != nil {
				log.Printf("failed to scrape metrics from pod %s/%s: %v", pod.Namespace, pod.Name, err)
				continue
			}

			appendFamilies(merged, families, pod.Namespace)
		}
	}

	w.Header().Set("Content-Type", `text/plain; version=0.0.4; charset=utf-8`)

	if len(merged) == 0 {
		// No metrics available, return an empty body with a 200 so scrapes continue but alert on emptiness if desired.
		return
	}

	names := make([]string, 0, len(merged))
	for name := range merged {
		names = append(names, name)
	}
	sort.Strings(names)

	encoder := expfmt.NewEncoder(w, expfmt.FmtText)
	for _, name := range names {
		if err := encoder.Encode(merged[name]); err != nil {
			log.Printf("failed to encode metric family %s: %v", name, err)
		}
	}
}

func (h *aggregatorHandler) scrapePodMetrics(parentCtx context.Context, pod *corev1.Pod) (map[string]*dto.MetricFamily, error) {
	if pod.Status.PodIP == "" {
		return nil, fmt.Errorf("pod does not have an assigned IP")
	}

	url := fmt.Sprintf("http://%s:%d%s", pod.Status.PodIP, h.scrapePort, h.scrapePath)

	ctx, cancel := context.WithTimeout(parentCtx, h.scrapeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	parser := expfmt.TextParser{}
	return parser.TextToMetricFamilies(resp.Body)
}

func isScrapablePod(pod *corev1.Pod) bool {
	if pod.DeletionTimestamp != nil {
		return false
	}
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	return pod.Status.PodIP != ""
}

func appendFamilies(dst map[string]*dto.MetricFamily, src map[string]*dto.MetricFamily, namespace string) {
	for name, family := range src {
		cloned := proto.Clone(family).(*dto.MetricFamily)

		for _, metric := range cloned.GetMetric() {
			ensureLabel(metric, "namespace", namespace)
		}

		if existing, ok := dst[name]; ok {
			existing.Metric = append(existing.Metric, cloned.Metric...)
		} else {
			dst[name] = cloned
		}
	}
}

func ensureLabel(metric *dto.Metric, labelName, value string) {
	for _, label := range metric.GetLabel() {
		if label.GetName() == labelName {
			label.Value = proto.String(value)
			return
		}
	}

	metric.Label = append(metric.Label, &dto.LabelPair{
		Name:  proto.String(labelName),
		Value: proto.String(value),
	})
}
