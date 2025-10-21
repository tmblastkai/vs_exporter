package collector

import (
	"context"
	"log"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var (
	virtualServiceGVR = schema.GroupVersionResource{
		Group:    "networking.istio.io",
		Version:  "v1beta1",
		Resource: "virtualservices",
	}
	namespaceGVR = schema.GroupVersionResource{
		Version:  "v1",
		Resource: "namespaces",
	}
)

// VirtualServiceCollector periodically refreshes metrics describing Istio VirtualServices.
type VirtualServiceCollector struct {
	client dynamic.Interface
	metric *prometheus.GaugeVec
}

// NewVirtualServiceCollector constructs a VirtualServiceCollector backed by the provided dynamic client.
func NewVirtualServiceCollector(client dynamic.Interface) *VirtualServiceCollector {
	return &VirtualServiceCollector{
		client: client,
		metric: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "istio_virtual_service_info",
				Help: "Information about Istio VirtualService resources, labelled by namespace and name.",
			},
			[]string{"namespace", "virtual_service"},
		),
	}
}

// Describe implements prometheus.Collector.
func (c *VirtualServiceCollector) Describe(ch chan<- *prometheus.Desc) {
	c.metric.Describe(ch)
}

// Collect implements prometheus.Collector.
func (c *VirtualServiceCollector) Collect(ch chan<- prometheus.Metric) {
	c.metric.Collect(ch)
}

// Run refreshes VirtualService metrics until the context is cancelled.
func (c *VirtualServiceCollector) Run(ctx context.Context, interval time.Duration) {
	if err := c.update(ctx); err != nil && ctx.Err() == nil {
		log.Printf("unable to update VirtualService metrics: %v", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.update(ctx); err != nil && ctx.Err() == nil {
				log.Printf("unable to update VirtualService metrics: %v", err)
			}
		}
	}
}

func (c *VirtualServiceCollector) update(ctx context.Context) error {
	namespaces, err := c.client.Resource(namespaceGVR).List(ctx, metav1.ListOptions{
		LabelSelector: "product",
	})
	if err != nil {
		return err
	}

	c.metric.Reset()

	for _, namespace := range namespaces.Items {
		nsName := namespace.GetName()

		list, err := c.client.Resource(virtualServiceGVR).Namespace(nsName).List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}

		for _, item := range list.Items {
			name := item.GetName()
			c.metric.WithLabelValues(nsName, name).Set(1)
		}
	}

	return nil
}
