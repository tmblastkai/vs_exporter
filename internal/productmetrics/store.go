package productmetrics

import (
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/golang/protobuf/proto"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

// MetricsContentType represents the HTTP content type for the exposed metrics endpoint.
const MetricsContentType = string(expfmt.FmtText)

// Store caches metric families gathered from product pods, grouped by scraping target.
type Store struct {
	mu      sync.RWMutex
	targets map[string]map[string]*dto.MetricFamily
}

// NewStore returns an initialized Store.
func NewStore() *Store {
	return &Store{
		targets: make(map[string]map[string]*dto.MetricFamily),
	}
}

// Replace updates the cached metric families for a specific scraping target.
func (s *Store) Replace(target string, all map[string]*dto.MetricFamily) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.targets[target] = all
}

// WriteAll renders every cached metric family to the provided writer in text format.
func (s *Store) WriteAll(w io.Writer) error {
	combined := s.snapshot()
	if len(combined) == 0 {
		return nil
	}

	encoder := expfmt.NewEncoder(w, expfmt.FmtText)
	names := make([]string, 0, len(combined))
	for name := range combined {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if err := encoder.Encode(combined[name]); err != nil {
			return fmt.Errorf("encode metric family %s: %w", name, err)
		}
	}

	return nil
}

func (s *Store) snapshot() map[string]*dto.MetricFamily {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.targets) == 0 {
		return nil
	}

	result := make(map[string]*dto.MetricFamily)
	for _, families := range s.targets {
		for name, family := range families {
			familyClone := proto.Clone(family).(*dto.MetricFamily)
			if existing, ok := result[name]; ok {
				existing.Metric = append(existing.Metric, familyClone.Metric...)
			} else {
				result[name] = familyClone
			}
		}
	}

	return result
}
