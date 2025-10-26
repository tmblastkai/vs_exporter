package productmetrics

import (
	"fmt"
	"io"
	"sort"
	"sync"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

// MetricsContentType represents the HTTP content type for the exposed metrics endpoint.
const MetricsContentType = string(expfmt.FmtText)

// Store caches metric families gathered from product pods.
type Store struct {
	mu       sync.RWMutex
	families map[string]*dto.MetricFamily
}

// NewStore returns an initialized Store.
func NewStore() *Store {
	return &Store{
		families: make(map[string]*dto.MetricFamily),
	}
}

// Replace swaps the in-memory cache with the provided metric families.
func (s *Store) Replace(all map[string]*dto.MetricFamily) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.families = all
}

// WriteAll renders every cached metric family to the provided writer in text format.
func (s *Store) WriteAll(w io.Writer) error {
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
