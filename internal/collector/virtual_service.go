package collector

import (
	"context"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	v1beta1 "istio.io/client-go/pkg/apis/networking/v1beta1"
	istio "istio.io/client-go/pkg/clientset/versioned"
)

const vsCollectorLogPrefix = "[VirtualServiceCollector]"

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
				Help: "Information about Istio VirtualService resources, labelled by namespace, virtual service, and referenced gateway.",
			},
			[]string{"namespace", "virtual_service", "gateway"},
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
		logrus.WithField("component", vsCollectorLogPrefix).Warnf("unable to update VirtualService metrics: %v", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.update(ctx); err != nil && ctx.Err() == nil {
				logrus.WithField("component", vsCollectorLogPrefix).Warnf("unable to update VirtualService metrics: %v", err)
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

	gatewayCache := make(map[string]map[string]*v1beta1.Gateway)

	for _, namespace := range namespaces.Items {
		nsName := namespace.GetName()
		if _, err := c.ensureGatewaysCached(ctx, nsName, gatewayCache); err != nil {
			return err
		}

		vsList, err := c.istioClient.NetworkingV1beta1().VirtualServices(nsName).List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}

		for _, vs := range vsList.Items {
			if vs == nil {
				continue
			}

			gateways := vs.Spec.Gateways
			if len(gateways) == 0 {
				gateways = []string{"mesh"}
			}

			for _, gatewayRef := range gateways {
				labelGateway := gatewayRef
				value := 1.0

				if gatewayRef == "" {
					value = 0
				} else if gatewayRef == "mesh" {
					// mesh gateway is virtual; assume healthy
					value = 1
				} else {
					gwNamespace := nsName
					gwName := gatewayRef

					if strings.Contains(gatewayRef, "/") {
						parts := strings.SplitN(gatewayRef, "/", 2)
						if len(parts) == 2 {
							gwNamespace = parts[0]
							gwName = parts[1]
						}
					}

					nsGateways, err := c.ensureGatewaysCached(ctx, gwNamespace, gatewayCache)
					if err != nil {
						return err
					}

					if len(nsGateways) == 0 {
						value = 0
					} else {
						gateway, ok := nsGateways[gwName]
						if !ok {
							value = 0
						} else if !hostsCompatible(vs.Spec.Hosts, gateway) {
							value = 0
						}
					}
				}

				c.metric.WithLabelValues(nsName, vs.GetName(), labelGateway).Set(value)
			}
		}
	}

	return nil
}

func (c *VirtualServiceCollector) ensureGatewaysCached(ctx context.Context, namespace string, cache map[string]map[string]*v1beta1.Gateway) (map[string]*v1beta1.Gateway, error) {
	if namespace == "" {
		return nil, nil
	}

	if gateways, ok := cache[namespace]; ok {
		return gateways, nil
	}

	list, err := c.istioClient.NetworkingV1beta1().Gateways(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	result := make(map[string]*v1beta1.Gateway, len(list.Items))
	for _, gateway := range list.Items {
		if gateway == nil {
			continue
		}
		result[gateway.GetName()] = gateway
	}

	cache[namespace] = result
	return result, nil
}

func hostsCompatible(vsHosts []string, gateway *v1beta1.Gateway) bool {
	if gateway == nil {
		return false
	}

	if len(vsHosts) == 0 {
		return true
	}

	var gatewayHosts []string
	for _, server := range gateway.Spec.Servers {
		if server == nil {
			continue
		}
		gatewayHosts = append(gatewayHosts, server.Hosts...)
	}

	if len(gatewayHosts) == 0 {
		return false
	}

	for _, vsHost := range vsHosts {
		for _, gwHost := range gatewayHosts {
			if hostMatches(gwHost, vsHost) || hostMatches(vsHost, gwHost) {
				return true
			}
		}
	}

	return false
}

func hostMatches(pattern, host string) bool {
	if pattern == "" {
		return false
	}
	if pattern == "*" || pattern == host {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := strings.TrimPrefix(pattern, "*")
		return strings.HasSuffix(host, suffix)
	}
	return false
}
