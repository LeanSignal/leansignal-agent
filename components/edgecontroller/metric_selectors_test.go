// Copyright 2026 LeanSignal
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

// Metric-selector demand (DemandSet.metric_selectors): cache plumbing plus the
// selector-aware (name-level) ping stat and diagnosis.
package leansignaledgecontroller

import (
	"reflect"
	"sort"
	"testing"

	"go.uber.org/zap"

	agentv1 "github.com/leansignal/leansignal-agent/proto/gen/leansignal/agent/v1"
)

// A pushed DemandSet carrying metric selectors lands in the demand cache and
// is served by GetMetricSelectors (which the metrics demand filter reads).
func TestHandleServerMessageDemandSetMetricSelectors(t *testing.T) {
	e := newEdgeControllerExtension(zap.NewNop(), &Config{})

	sel := []string{
		`{__name__="node_cpu_seconds_total",mode!="idle"}`,
		`{__name__=~"node_.*",cpu="0"}`,
	}
	e.handleServerMessage(&agentv1.ServerMessage{
		Body: &agentv1.ServerMessage_DemandSet{DemandSet: &agentv1.DemandSet{
			Metrics:         []string{"node_cpu_seconds_total"},
			MetricSelectors: sel,
			Hash:            321,
		}},
	})

	if got := e.GetMetricSelectors(); !reflect.DeepEqual(got, sel) {
		t.Fatalf("GetMetricSelectors() = %v, want %v", got, sel)
	}
	// The names list remains available for the fallback path.
	if got := e.GetDemands(); len(got) != 1 || got[0] != "node_cpu_seconds_total" {
		t.Fatalf("GetDemands() = %v, want [node_cpu_seconds_total]", got)
	}
}

// GetMetricSelectors is empty until a demand arrives (fail-closed default),
// and the snapshot copies are defensive.
func TestMetricSelectorsCacheDefaultsAndCopies(t *testing.T) {
	e := newEdgeControllerExtension(zap.NewNop(), &Config{})
	if got := e.GetMetricSelectors(); len(got) != 0 {
		t.Fatalf("GetMetricSelectors() = %v, want empty", got)
	}

	input := []string{`{__name__="up"}`}
	e.demandTimeseriesCache.UpdateDemands(nil, nil, nil, input, 1)
	input[0] = `{__name__="mutated"}` // caller mutation must not leak in

	got := e.GetMetricSelectors()
	if len(got) != 1 || got[0] != `{__name__="up"}` {
		t.Fatalf("GetMetricSelectors() = %v, want the original selector", got)
	}
	got[0] = "mutated-out" // returned-slice mutation must not leak back
	if again := e.GetMetricSelectors(); again[0] != `{__name__="up"}` {
		t.Fatalf("cache mutated through returned slice: %v", again)
	}
}

// With metric selectors demanded, Ping.demanded_known_cache_size counts known
// series whose NAME is matched by any selector (family expansion included).
// Label matchers cannot be evaluated here — the known cache retains no
// per-series labels (see the TODO in demand_matcher.go) — so the count is
// name-level.
func TestBuildPingSelectorAware(t *testing.T) {
	e := newEdgeControllerExtension(zap.NewNop(), &Config{})

	e.knownTimeseriesCache.UpdateTimeseries(HashKey{1}, &TimeseriesEntry{MetricName: "node_cpu_seconds_total", Samples: 1})
	e.knownTimeseriesCache.UpdateTimeseries(HashKey{2}, &TimeseriesEntry{MetricName: "http_duration_bucket", Samples: 1})
	e.knownTimeseriesCache.UpdateTimeseries(HashKey{3}, &TimeseriesEntry{MetricName: "http_duration_sum", Samples: 1})
	e.knownTimeseriesCache.UpdateTimeseries(HashKey{4}, &TimeseriesEntry{MetricName: "node_load1", Samples: 1})

	e.handleServerMessage(&agentv1.ServerMessage{
		Body: &agentv1.ServerMessage_DemandSet{DemandSet: &agentv1.DemandSet{
			Metrics: []string{"node_cpu_seconds_total", "http_duration_bucket"},
			MetricSelectors: []string{
				`{__name__="node_cpu_seconds_total",mode="idle"}`, // label matcher ignored at name level
				`{__name__=~"http_.*_bucket"}`,                    // regex name; family keeps _sum too
			},
			Hash: 7,
		}},
	})

	ping := e.buildPing()
	// node_cpu_seconds_total + http_duration_bucket + http_duration_sum (family sibling).
	if ping.GetDemandedKnownCacheSize() != 3 {
		t.Errorf("DemandedKnownCacheSize = %d, want 3", ping.GetDemandedKnownCacheSize())
	}
	if ping.GetDemandCacheSize() != 2 {
		t.Errorf("DemandCacheSize = %d, want 2 (names list)", ping.GetDemandCacheSize())
	}
}

// buildDiagnosis reports the selector list when selectors are demanded,
// partitioned into matched/missing against known series names.
func TestBuildDiagnosisSelectorAware(t *testing.T) {
	e := newEdgeControllerExtension(zap.NewNop(), &Config{})

	e.knownTimeseriesCache.UpdateTimeseries(HashKey{1}, &TimeseriesEntry{MetricName: "up", Samples: 1})
	e.knownTimeseriesCache.UpdateTimeseries(HashKey{2}, &TimeseriesEntry{MetricName: "node_load1", Samples: 1})

	matchedSel := `{__name__="up",job="api"}`
	missingSel := `{__name__="does_not_exist"}`
	e.handleServerMessage(&agentv1.ServerMessage{
		Body: &agentv1.ServerMessage_DemandSet{DemandSet: &agentv1.DemandSet{
			Metrics:         []string{"up", "does_not_exist"},
			MetricSelectors: []string{matchedSel, missingSel},
			Hash:            11,
		}},
	})

	d := e.buildDiagnosis()
	if len(d.demand) != 2 || d.demand[0] != matchedSel {
		t.Errorf("demand = %v, want the selector list", d.demand)
	}
	if len(d.matched) != 1 || d.matched[0] != matchedSel {
		t.Errorf("matched = %v, want [%s]", d.matched, matchedSel)
	}
	if len(d.missing) != 1 || d.missing[0] != missingSel {
		t.Errorf("missing = %v, want [%s]", d.missing, missingSel)
	}
	if d.demandedSeries != 1 {
		t.Errorf("demandedSeries = %d, want 1 (up)", d.demandedSeries)
	}
}

// Without metric selectors nothing changes: names path exactly as before.
func TestBuildPingWithoutSelectorsUsesNames(t *testing.T) {
	e := newEdgeControllerExtension(zap.NewNop(), &Config{})
	e.knownTimeseriesCache.UpdateTimeseries(HashKey{1}, &TimeseriesEntry{MetricName: "up", Samples: 1})
	e.demandTimeseriesCache.UpdateDemands([]string{"up"}, nil, nil, nil, 1)
	if got := e.buildPing().GetDemandedKnownCacheSize(); got != 1 {
		t.Errorf("DemandedKnownCacheSize = %d, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// demand_matcher selector helpers
// ---------------------------------------------------------------------------

func TestFamilyNameVariants(t *testing.T) {
	cases := []struct {
		name string
		want []string
	}{
		{"up", []string{"up", "up_bucket", "up_sum", "up_count"}},
		{"h_bucket", []string{"h_bucket", "h_bucket", "h_sum", "h_count"}}, // no bare base for _bucket
		{"h_sum", []string{"h_sum", "h_bucket", "h_sum", "h_count", "h"}},
		{"h_count", []string{"h_count", "h_bucket", "h_sum", "h_count", "h"}},
		{"_sum", []string{"_sum", "_sum_bucket", "_sum_sum", "_sum_count"}}, // suffix-only name is not stripped
	}
	for _, c := range cases {
		if got := familyNameVariants(c.name); !reflect.DeepEqual(got, c.want) {
			t.Errorf("familyNameVariants(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSelectorDemandedNames(t *testing.T) {
	known := map[string]struct{}{
		"up":                   {},
		"node_load1":           {},
		"http_duration_bucket": {},
		"http_duration_sum":    {},
		"rpc_latency":          {}, // summary base
	}

	cases := []struct {
		name      string
		selectors []string
		want      []string
	}{
		{"exact name", []string{`{__name__="up"}`}, []string{"up"}},
		{"label matchers ignored at name level", []string{`{__name__="up",job="x"}`}, []string{"up"}},
		{"regex name", []string{`{__name__=~"node_.*"}`}, []string{"node_load1"}},
		{"bucket demand keeps family", []string{`{__name__="http_duration_bucket"}`},
			[]string{"http_duration_bucket", "http_duration_sum"}},
		{"summary base demand keeps base", []string{`{__name__="rpc_latency"}`}, []string{"rpc_latency"}},
		{"unparseable demands nothing", []string{`{broken`}, nil},
		{"union across selectors", []string{`{__name__="up"}`, `{__name__="node_load1"}`},
			[]string{"node_load1", "up"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := selectorDemandedNames(c.selectors, known)
			var gotList []string
			for n := range got {
				gotList = append(gotList, n)
			}
			sort.Strings(gotList)
			want := c.want
			sort.Strings(want)
			if !reflect.DeepEqual(gotList, want) {
				t.Errorf("selectorDemandedNames(%v) = %v, want %v", c.selectors, gotList, want)
			}
		})
	}
}

func TestDiagnoseDemandSelectors(t *testing.T) {
	known := map[string]struct{}{"up": {}, "h_bucket": {}}
	matched, missing := diagnoseDemandSelectors([]string{
		`{__name__="up"}`,
		`{__name__="h_sum"}`, // family sibling of known h_bucket → matched
		`{__name__="absent_metric"}`,
		`{broken`,
	}, known)

	wantMatched := []string{`{__name__="h_sum"}`, `{__name__="up"}`}
	wantMissing := []string{`{__name__="absent_metric"}`, `{broken`}
	if !reflect.DeepEqual(matched, wantMatched) {
		t.Errorf("matched = %v, want %v", matched, wantMatched)
	}
	if !reflect.DeepEqual(missing, wantMissing) {
		t.Errorf("missing = %v, want %v", missing, wantMissing)
	}
}
