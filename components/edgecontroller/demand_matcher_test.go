// Copyright 2026 LeanSignal
// SPDX-License-Identifier: Apache-2.0

package leansignaledgecontroller

import (
	"testing"

	"go.uber.org/zap"
)

// expandDemandNames must mirror leansignaldemandfilter.isMetricDemanded at
// name level: for every demanded name, exactly the series names the filter
// would forward must be in the expanded set.
func TestExpandDemandNames(t *testing.T) {
	tests := []struct {
		name    string
		demands []string
		want    []string // names that must be in the set
		notWant []string // names that must NOT be in the set
	}{
		{
			name:    "gauge exact match only",
			demands: []string{"node_load1"},
			want:    []string{"node_load1"},
			notWant: []string{"node_load1_bucket", "node_load"},
		},
		{
			name:    "counter demanded by _total name",
			demands: []string{"http_requests_total"},
			want:    []string{"http_requests_total"},
			notWant: []string{"http_requests"},
		},
		{
			name:    "histogram family via _bucket keeps all components",
			demands: []string{"http_duration_bucket"},
			want:    []string{"http_duration_bucket", "http_duration_sum", "http_duration_count"},
		},
		{
			name:    "histogram family via _sum keeps all components",
			demands: []string{"http_duration_sum"},
			want:    []string{"http_duration_bucket", "http_duration_sum", "http_duration_count"},
		},
		{
			name: "summary demanded by _count keeps quantile series (base name)",
			// A summary produces series base / base_sum / base_count; the filter
			// keeps the whole summary when any component is demanded.
			demands: []string{"rpc_latency_count"},
			want:    []string{"rpc_latency", "rpc_latency_sum", "rpc_latency_count"},
		},
		{
			name: "summary demanded by base keeps _sum and _count",
			// Filter: Summary matches on base -> whole family forwarded.
			demands: []string{"rpc_latency"},
			want:    []string{"rpc_latency", "rpc_latency_sum", "rpc_latency_count"},
			// Classic histograms are never matched by base name in the filter,
			// so buckets of a same-named histogram must not count as stored.
			notWant: []string{"rpc_latency_bucket"},
		},
		{
			name:    "empty and suffix-only names are safe",
			demands: []string{"", "_sum"},
			want:    []string{"_sum"},
			notWant: []string{""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set := expandDemandNames(tt.demands)
			for _, w := range tt.want {
				if _, ok := set[w]; !ok {
					t.Errorf("expandDemandNames(%v): missing %q (set: %v)", tt.demands, w, set)
				}
			}
			for _, nw := range tt.notWant {
				if _, ok := set[nw]; ok {
					t.Errorf("expandDemandNames(%v): must not contain %q", tt.demands, nw)
				}
			}
		})
	}
}

func TestCountDemanded(t *testing.T) {
	c := NewKnownTimeseriesCache(zap.NewNop())
	// Two series of a demanded histogram, one demanded gauge, one undemanded gauge.
	c.UpdateTimeseries(HashKey{1}, &TimeseriesEntry{MetricName: "http_duration_bucket", Samples: 1})
	c.UpdateTimeseries(HashKey{2}, &TimeseriesEntry{MetricName: "http_duration_count", Samples: 1})
	c.UpdateTimeseries(HashKey{3}, &TimeseriesEntry{MetricName: "up", Samples: 1})
	c.UpdateTimeseries(HashKey{4}, &TimeseriesEntry{MetricName: "node_load1", Samples: 1})

	got := c.CountDemanded(expandDemandNames([]string{"http_duration_bucket", "up"}))
	if got != 3 {
		t.Errorf("CountDemanded = %d, want 3 (2 histogram series + up)", got)
	}

	if got := c.CountDemanded(expandDemandNames(nil)); got != 0 {
		t.Errorf("CountDemanded(empty) = %d, want 0", got)
	}
}

// MetricName must be persisted when a series first enters the known cache.
func TestUpdateTimeseriesStoresMetricName(t *testing.T) {
	c := NewKnownTimeseriesCache(zap.NewNop())
	c.UpdateTimeseries(HashKey{9}, &TimeseriesEntry{MetricName: "up", Samples: 2})

	if got := c.CountDemanded(map[string]struct{}{"up": {}}); got != 1 {
		t.Fatalf("CountDemanded = %d, want 1 (MetricName not stored on create?)", got)
	}
}

func TestDiagnoseDemand(t *testing.T) {
	// Known metric names: a histogram family, a demanded gauge; NOT node_load1.
	known := map[string]struct{}{
		"http_duration_bucket": {},
		"http_duration_sum":    {},
		"http_duration_count":  {},
		"up":                   {},
	}
	// Demand a matched histogram (by _bucket), a matched gauge, and two that
	// have no known series.
	matched, missing := diagnoseDemand(
		[]string{"http_duration_bucket", "up", "does_not_exist", "node_load1"},
		known,
	)

	wantMatched := []string{"http_duration_bucket", "up"}
	wantMissing := []string{"does_not_exist", "node_load1"}
	if !equalStrings(matched, wantMatched) {
		t.Errorf("matched = %v, want %v", matched, wantMatched)
	}
	if !equalStrings(missing, wantMissing) {
		t.Errorf("missing = %v, want %v", missing, wantMissing)
	}
}

func TestMetricNameSet(t *testing.T) {
	c := NewKnownTimeseriesCache(zap.NewNop())
	c.UpdateTimeseries(HashKey{1}, &TimeseriesEntry{MetricName: "up", Samples: 1})
	c.UpdateTimeseries(HashKey{2}, &TimeseriesEntry{MetricName: "up", Samples: 1}) // dup name, different series
	c.UpdateTimeseries(HashKey{3}, &TimeseriesEntry{MetricName: "node_load1", Samples: 1})

	set := c.MetricNameSet()
	if len(set) != 2 {
		t.Fatalf("MetricNameSet size = %d, want 2 (distinct names)", len(set))
	}
	if _, ok := set["up"]; !ok {
		t.Error("expected 'up' in name set")
	}
	if _, ok := set["node_load1"]; !ok {
		t.Error("expected 'node_load1' in name set")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
