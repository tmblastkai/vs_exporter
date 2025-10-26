package productmetrics

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/golang/protobuf/proto"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	namespaceLabelKey = "namespace"
	productLabel      = "product"
	requestTimeout    = 10 * time.Second
)

// Scraper periodically gathers metrics from product pods and updates the provided store.
type Scraper struct {
	clientset   kubernetes.Interface
	httpClient  *http.Client
	store       *Store
	interval    time.Duration
	port        int
	metricsPath string
	logger      Logger
}

// Logger captures log messages emitted by the scraper loop.
type Logger interface {
	Printf(format string, v ...interface{})
}

type nopLogger struct{}

func (nopLogger) Printf(string, ...interface{}) {}

// NewScraper constructs a Scraper responsible for discovering labelled pods and
// aggregating their exposed Prometheus metrics.
func NewScraper(
	clientset kubernetes.Interface,
	httpClient *http.Client,
	store *Store,
	interval time.Duration,
	port int,
	metricsPath string,
	logger Logger,
) *Scraper {
	if logger == nil {
		logger = nopLogger{}
	}
	return &Scraper{
		clientset:   clientset,
		httpClient:  httpClient,
		store:       store,
		interval:    interval,
		port:        port,
		metricsPath: metricsPath,
		logger:      logger,
	}
}

// Run executes the scrape loop until the context is cancelled.
func (s *Scraper) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		if err := s.ScrapeOnce(ctx); err != nil {
			s.logger.Printf("scrape failed: %v", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ScrapeOnce discovers labelled pods and refreshes the stored metrics.
func (s *Scraper) ScrapeOnce(ctx context.Context) error {
	nsList, err := s.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{LabelSelector: productLabel})
	if err != nil {
		return fmt.Errorf("list namespaces: %w", err)
	}

	newFamilies := make(map[string]*dto.MetricFamily)
	var errs []error

	for _, ns := range nsList.Items {
		pods, err := s.clientset.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{LabelSelector: productLabel})
		if err != nil {
			errs = append(errs, fmt.Errorf("list pods in namespace %s: %w", ns.Name, err))
			continue
		}

		for i := range pods.Items {
			pod := &pods.Items[i]
			if pod.Status.PodIP == "" {
				continue
			}
			if err := s.scrapePod(ctx, pod, ns.Name, newFamilies); err != nil {
				errs = append(errs, fmt.Errorf("scrape pod %s/%s: %w", ns.Name, pod.Name, err))
			}
		}
	}

	s.store.Replace(newFamilies)

	return errors.Join(errs...)
}

func (s *Scraper) scrapePod(
	ctx context.Context,
	pod *corev1.Pod,
	namespace string,
	accumulator map[string]*dto.MetricFamily,
) error {
	url := fmt.Sprintf("http://%s:%d%s", pod.Status.PodIP, s.port, s.metricsPath)

	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
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
