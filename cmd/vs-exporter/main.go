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
	"vs_exporter/internal/kube"
	"vs_exporter/internal/productmetrics"
)

const (
	// defaultListenAddress 定義 HTTP 伺服器的預設監聽位址與協定。
	defaultListenAddress = ":8081"
	// defaultScrapePort 是產品微服務 POD 對外暴露的預設 metrics 端口。
	defaultScrapePort = 1234
	// defaultMetricsPath 是產品微服務預設的 metrics HTTP path。
	defaultMetricsPath = "/metrics"
	// internalMetricsAddress 是 Go runtime/promhttp 預設指標的額外曝光端口。
	internalMetricsAddress = ":8123"
	// defaultNamespaceSelector 定義用來挑選目標 namespace 的預設 label selector。
	defaultNamespaceSelector = "product"
	// defaultPodSelector 定義用來挑選目標 pod 的預設 label selector。
	defaultPodSelector = "product"
)

func main() {
	// 讀取 CLI 參數，允許使用者在部署時覆蓋預設設定。
	listenAddress := flag.String("listen-address", defaultListenAddress, "Address to listen on for HTTP requests")
	vsInterval := flag.Duration("interval", 5*time.Minute, "Interval between VirtualService metric refreshes")
	productInterval := flag.Duration("product-interval", 5*time.Minute, "Interval between product metric refreshes")
	productPort := flag.Int("scrape-port", defaultScrapePort, "Pod port to scrape product metrics from")
	productMetricsPath := flag.String("metrics-path", defaultMetricsPath, "Pod product metrics HTTP path")
	namespaceSelector := flag.String("namespace-selector", defaultNamespaceSelector, "Label selector used to find namespaces with product metrics pods")
	podSelector := flag.String("pod-selector", defaultPodSelector, "Label selector used to find product metrics pods inside each namespace")
	flag.Parse()

	// 建立 Kubernetes REST Config，優先採用 in-cluster 設定，否則回退到 kubeconfig。
	cfg, err := kube.BuildConfig()
	if err != nil {
		log.Fatalf("failed to build Kubernetes configuration: %v", err)
	}

	// 建立操作 Istio VirtualService 所需的 typed client。
	istioClient, err := versioned.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("failed to create Istio clientset: %v", err)
	}

	// 建立核心 clientset 以操作核心資源 (Namespace / Pod) 供產品 metrics 抓取使用。
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("failed to create Kubernetes clientset: %v", err)
	}

	// 註冊 VirtualService collector 到 Prometheus default registry。
	vsCollector := collector.NewVirtualServiceCollector(clientset, istioClient)
	prometheus.MustRegister(vsCollector)

	// 建立產品 metrics 暫存 store 與 HTTP 客戶端。
	store := productmetrics.NewStore()
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	// 使用 NotifyContext 監聽 SIGINT/SIGTERM，方便進行優雅關閉。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 啟動 VirtualService collector 週期性刷新。
	go vsCollector.Run(ctx, *vsInterval)

	// 建立並啟動產品 metrics 抓取器，每個週期會抓取含 product label 的 POD 暴露的 metrics。
	productScraper := productmetrics.NewScraper(
		clientset,
		httpClient,
		store,
		*productInterval,
		*productPort,
		*productMetricsPath,
		*namespaceSelector,
		*podSelector,
		log.Default(),
	)
	go productScraper.Run(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		// 先將 Prometheus 內建 registry 的 metrics 序列化到暫存 buffer。
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
			// 將產品 metrics 追加到 buffer，如果失敗直接回傳 500。
			log.Printf("failed to render product metrics: %v", err)
			http.Error(w, "failed to render metrics", http.StatusInternalServerError)
			return
		}

		// 一次性輸出合併後的 metrics 給 Prometheus server。
		w.Header().Set("Content-Type", productmetrics.MetricsContentType)
		if _, err := w.Write(buf.Bytes()); err != nil {
			log.Printf("failed to write metrics response: %v", err)
		}
	})

	srv := &http.Server{
		Addr:    *listenAddress,
		Handler: mux,
	}
	internalSrv := &http.Server{
		Addr:    internalMetricsAddress,
		Handler: promhttp.Handler(),
	}

	go func() {
		// 等待 context 結束時，發起優雅關閉，避免中斷中的請求。
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

	log.Printf("serving metrics at %s/metrics", *listenAddress)
	log.Printf("serving Go runtime metrics at %s/metrics", internalMetricsAddress)

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("HTTP server error: %v", err)
	}
}
