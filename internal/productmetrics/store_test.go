package productmetrics

import (
	"bytes"
	"testing"

	"github.com/golang/protobuf/proto"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

func TestStoreWriteAllMergesTargets(t *testing.T) {
	store := NewStore()

	store.Replace("alpha", map[string]*dto.MetricFamily{
		"test_metric": newGaugeFamily("test_metric", "ns-a", 1),
	})

	store.Replace("beta", map[string]*dto.MetricFamily{
		"test_metric": newGaugeFamily("test_metric", "ns-b", 2),
	})

	var buf bytes.Buffer
	if err := store.WriteAll(&buf); err != nil {
		t.Fatalf("WriteAll() error = %v", err)
	}

	parser := expfmt.TextParser{}
	families, err := parser.TextToMetricFamilies(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("failed to parse metrics output: %v", err)
	}

	family, ok := families["test_metric"]
	if !ok {
		t.Fatalf("expected family test_metric to exist")
	}

	metrics := family.GetMetric()
	if len(metrics) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(metrics))
	}

	found := map[string]float64{}
	for _, metric := range metrics {
		var ns string
		for _, label := range metric.GetLabel() {
			if label.GetName() == namespaceLabelKey {
				ns = label.GetValue()
			}
		}
		found[ns] = metric.GetGauge().GetValue()
	}

	if found["ns-a"] != 1 || found["ns-b"] != 2 {
		t.Fatalf("unexpected metric values: %+v", found)
	}
}

func TestStoreWriteAllEmpty(t *testing.T) {
	store := NewStore()
	var buf bytes.Buffer
	if err := store.WriteAll(&buf); err != nil {
		t.Fatalf("WriteAll() error = %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected empty output, got %q", buf.String())
	}
}

func newGaugeFamily(name, namespace string, value float64) *dto.MetricFamily {
	return &dto.MetricFamily{
		Name: proto.String(name),
		Type: dto.MetricType_GAUGE.Enum(),
		Metric: []*dto.Metric{
			{
				Label: []*dto.LabelPair{
					{
						Name:  proto.String(namespaceLabelKey),
						Value: proto.String(namespace),
					},
				},
				Gauge: &dto.Gauge{
					Value: proto.Float64(value),
				},
			},
		},
	}
}
