package collector

import (
	"context"
	"log"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	istio "istio.io/client-go/pkg/clientset/versioned"
)

// VirtualServiceCollector periodically refreshes metrics describing Istio VirtualServices.
type VirtualServiceCollector struct {
	kubeClient  kubernetes.Interface
	istioClient istio.Interface
	metric      *prometheus.GaugeVec
	updateCount prometheus.Counter
}

// NewVirtualServiceCollector constructs a VirtualServiceCollector backed by typed Kubernetes and Istio clients.
func NewVirtualServiceCollector(kubeClient kubernetes.Interface, istioClient istio.Interface) *VirtualServiceCollector {
	return &VirtualServiceCollector{
		kubeClient:  kubeClient,
		istioClient: istioClient,
		metric: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "istio_virtual_service_info",
				Help: "Information about Istio VirtualService resources, labelled by namespace and name.",
			},
			[]string{"namespace", "virtual_service"},
		),
		updateCount: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "platform_virtualservice_metrics_update",
				Help: "Total number of VirtualService metric refresh attempts.",
			},
		),
	}
}

// Describe implements prometheus.Collector.
func (c *VirtualServiceCollector) Describe(ch chan<- *prometheus.Desc) {
	c.metric.Describe(ch)
	c.updateCount.Describe(ch)
}

// Collect implements prometheus.Collector.
func (c *VirtualServiceCollector) Collect(ch chan<- prometheus.Metric) {
	c.metric.Collect(ch)
	c.updateCount.Collect(ch)
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
	c.updateCount.Inc()

	namespaces, err := c.kubeClient.CoreV1().Namespaces().List(ctx, metav1.ListOptions{
		LabelSelector: "product",
	})
	if err != nil {
		return err
	}

	c.metric.Reset()

	for _, namespace := range namespaces.Items {
		nsName := namespace.GetName()

		list, err := c.istioClient.NetworkingV1beta1().VirtualServices(nsName).List(ctx, metav1.ListOptions{})
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
