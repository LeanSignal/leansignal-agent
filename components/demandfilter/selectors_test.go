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

package leansignaldemandfilter

import (
	"context"
	"fmt"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// Mock helpers (selector mode)
// ---------------------------------------------------------------------------

// mockSelectorProvider satisfies both DemandProvider and MetricSelectorProvider.
type mockSelectorProvider struct {
	demands   []string
	selectors []string
}

func (m *mockSelectorProvider) GetDemands() []string         { return m.demands }
func (m *mockSelectorProvider) GetMetricSelectors() []string { return m.selectors }

// mockSelectorExtension lives in host.GetExtensions() and is type-assertable
// to both provider interfaces.
type mockSelectorExtension struct{ *mockSelectorProvider }

func (e *mockSelectorExtension) Start(_ context.Context, _ component.Host) error { return nil }
func (e *mockSelectorExtension) Shutdown(_ context.Context) error                { return nil }

func newSelectorTestProc(provider *mockSelectorProvider) (*demandFilterProcessor, *mockConsumer) {
	mc := &mockConsumer{}
	p := newDemandFilterProcessor(zap.NewNop(), mc, &Config{})
	p.provider = provider
	p.selectorProvider = provider
	return p, mc
}

// countDatapoints walks every forwarded batch and counts datapoints.
func (m *mockConsumer) totalDatapoints() int {
	n := 0
	for _, md := range m.batches {
		rms := md.ResourceMetrics()
		for i := 0; i < rms.Len(); i++ {
			sms := rms.At(i).ScopeMetrics()
			for j := 0; j < sms.Len(); j++ {
				ms := sms.At(j).Metrics()
				for k := 0; k < ms.Len(); k++ {
					mtr := ms.At(k)
					switch mtr.Type() {
					case pmetric.MetricTypeGauge:
						n += mtr.Gauge().DataPoints().Len()
					case pmetric.MetricTypeSum:
						n += mtr.Sum().DataPoints().Len()
					case pmetric.MetricTypeHistogram:
						n += mtr.Histogram().DataPoints().Len()
					case pmetric.MetricTypeExponentialHistogram:
						n += mtr.ExponentialHistogram().DataPoints().Len()
					case pmetric.MetricTypeSummary:
						n += mtr.Summary().DataPoints().Len()
					}
				}
			}
		}
	}
	return n
}

// newResourceMD builds a batch with one ResourceMetrics carrying the given
// resource attributes.
func newResourceMD(resAttrs map[string]string, fill func(sm pmetric.ScopeMetrics)) pmetric.Metrics {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	for k, v := range resAttrs {
		rm.Resource().Attributes().PutStr(k, v)
	}
	fill(rm.ScopeMetrics().AppendEmpty())
	return md
}

// addGaugeDPs appends a gauge whose datapoints carry the given attribute maps.
func addGaugeDPs(sm pmetric.ScopeMetrics, name string, dpAttrs ...map[string]string) {
	m := sm.Metrics().AppendEmpty()
	m.SetName(name)
	g := m.SetEmptyGauge()
	for _, attrs := range dpAttrs {
		dp := g.DataPoints().AppendEmpty()
		for k, v := range attrs {
			dp.Attributes().PutStr(k, v)
		}
	}
}

// addHistogramDPs appends a histogram whose datapoints carry the given
// attribute maps.
func addHistogramDPs(sm pmetric.ScopeMetrics, name string, dpAttrs ...map[string]string) {
	m := sm.Metrics().AppendEmpty()
	m.SetName(name)
	h := m.SetEmptyHistogram()
	h.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	for _, attrs := range dpAttrs {
		dp := h.DataPoints().AppendEmpty()
		for k, v := range attrs {
			dp.Attributes().PutStr(k, v)
		}
	}
}

// addSummaryDPs appends a summary whose datapoints carry the given attribute maps.
func addSummaryDPs(sm pmetric.ScopeMetrics, name string, dpAttrs ...map[string]string) {
	m := sm.Metrics().AppendEmpty()
	m.SetName(name)
	s := m.SetEmptySummary()
	for _, attrs := range dpAttrs {
		dp := s.DataPoints().AppendEmpty()
		for k, v := range attrs {
			dp.Attributes().PutStr(k, v)
		}
	}
}

// ---------------------------------------------------------------------------
// Start — provider discovery (backward compatible)
// ---------------------------------------------------------------------------

func TestStart_FindsMetricSelectorProvider(t *testing.T) {
	mc := &mockConsumer{}
	p := newDemandFilterProcessor(zap.NewNop(), mc, &Config{})
	ext := &mockSelectorExtension{&mockSelectorProvider{selectors: []string{`{__name__="up"}`}}}
	host := &mockHost{extensions: map[component.ID]component.Component{
		component.MustNewID("leansignal_edge_controller"): ext,
	}}
	if err := p.Start(context.Background(), host); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if p.provider == nil {
		t.Fatal("provider must be resolved")
	}
	if p.selectorProvider == nil {
		t.Fatal("selectorProvider must be resolved when the extension satisfies MetricSelectorProvider")
	}
}

func TestStart_NameOnlyProviderKeepsSelectorProviderNil(t *testing.T) {
	mc := &mockConsumer{}
	p := newDemandFilterProcessor(zap.NewNop(), mc, &Config{})
	ext := &mockExtension{&mockDemandProvider{demands: []string{"up"}}}
	host := &mockHost{extensions: map[component.ID]component.Component{
		component.MustNewID("leansignal_edge_controller"): ext,
	}}
	if err := p.Start(context.Background(), host); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if p.provider == nil {
		t.Fatal("provider must be resolved")
	}
	if p.selectorProvider != nil {
		t.Fatal("selectorProvider must stay nil for a name-only extension")
	}
}

// ---------------------------------------------------------------------------
// Selector mode — matching
// ---------------------------------------------------------------------------

func TestSelectorMode_ExactNameWithLabelMatcher(t *testing.T) {
	p, mc := newSelectorTestProc(&mockSelectorProvider{
		selectors: []string{`{__name__="cpu_usage",mode="idle"}`},
	})
	md := newResourceMD(nil, func(sm pmetric.ScopeMetrics) {
		addGaugeDPs(sm, "cpu_usage",
			map[string]string{"mode": "idle"},
			map[string]string{"mode": "user"},
			map[string]string{"mode": "system"},
		)
		addGaugeDPs(sm, "mem_usage", map[string]string{"mode": "idle"})
	})
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatal(err)
	}
	if got := mc.totalDatapoints(); got != 1 {
		t.Errorf("want 1 datapoint (mode=idle), got %d", got)
	}
	if names := mc.receivedNames(); len(names) != 1 || names[0] != "cpu_usage" {
		t.Errorf("want [cpu_usage], got %v", names)
	}
}

func TestSelectorMode_RegexName(t *testing.T) {
	p, mc := newSelectorTestProc(&mockSelectorProvider{
		selectors: []string{`{__name__=~"node_.*"}`},
	})
	md := newResourceMD(nil, func(sm pmetric.ScopeMetrics) {
		addGaugeDPs(sm, "node_load1", map[string]string{})
		addGaugeDPs(sm, "node_load5", map[string]string{})
		addGaugeDPs(sm, "process_cpu", map[string]string{})
	})
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatal(err)
	}
	names := mc.receivedNames()
	if len(names) != 2 || names[0] != "node_load1" || names[1] != "node_load5" {
		t.Errorf("want [node_load1 node_load5], got %v", names)
	}
}

func TestSelectorMode_LabelOps(t *testing.T) {
	mkMD := func() pmetric.Metrics {
		return newResourceMD(nil, func(sm pmetric.ScopeMetrics) {
			addGaugeDPs(sm, "m",
				map[string]string{"mode": "idle", "cpu": "0"},
				map[string]string{"mode": "user", "cpu": "1"},
				map[string]string{"mode": "system", "cpu": "3"},
			)
		})
	}
	cases := []struct {
		selector string
		wantDPs  int
	}{
		{`{__name__="m",mode="idle"}`, 1},
		{`{__name__="m",mode!="idle"}`, 2},
		{`{__name__="m",cpu=~"0|3"}`, 2},
		{`{__name__="m",mode!~"i.*"}`, 2},
		{`{__name__="m",mode!="idle",cpu=~"0|3"}`, 1}, // AND within a selector
		{`{__name__="m",mode="wired"}`, 0},
		{`{__name__="m",absent=""}`, 3},  // = "" matches absent label
		{`{__name__="m",absent!=""}`, 0}, // != "" fails on absent label
	}
	for _, c := range cases {
		t.Run(c.selector, func(t *testing.T) {
			p, mc := newSelectorTestProc(&mockSelectorProvider{selectors: []string{c.selector}})
			if err := p.ConsumeMetrics(context.Background(), mkMD()); err != nil {
				t.Fatal(err)
			}
			if got := mc.totalDatapoints(); got != c.wantDPs {
				t.Errorf("%s: want %d datapoints, got %d", c.selector, c.wantDPs, got)
			}
		})
	}
}

func TestSelectorMode_UnionAcrossSelectors(t *testing.T) {
	p, mc := newSelectorTestProc(&mockSelectorProvider{
		selectors: []string{
			`{__name__="m",mode="idle"}`,
			`{__name__="m",mode="user"}`,
		},
	})
	md := newResourceMD(nil, func(sm pmetric.ScopeMetrics) {
		addGaugeDPs(sm, "m",
			map[string]string{"mode": "idle"},
			map[string]string{"mode": "user"},
			map[string]string{"mode": "system"},
		)
	})
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatal(err)
	}
	if got := mc.totalDatapoints(); got != 2 {
		t.Errorf("want 2 datapoints (idle ∪ user), got %d", got)
	}
}

// Resource attributes are part of the series label view: flattened
// (dots→underscores) and job/instance synthesized from service.*.
func TestSelectorMode_ResourceAttrMatchers(t *testing.T) {
	resAttrs := map[string]string{
		"service.name":  "api",
		"host.name":     "web-1",
		"k8s.pod.name":  "pod-42",
		"src.dotted.at": "v",
	}
	cases := []struct {
		selector string
		wantDPs  int
	}{
		{`{__name__="m",k8s_pod_name="pod-42"}`, 2},   // flattened resource attr
		{`{__name__="m",k8s_pod_name="nope"}`, 0},     //
		{`{__name__="m",job="api"}`, 2},               // synthesized job from service.name
		{`{__name__="m",instance="web-1"}`, 2},        // synthesized instance falls back to host.name
		{`{__name__="m",src_dotted_at="v"}`, 2},       // dot normalization on resource attrs
		{`{__name__="m",k8s_pod_name!="pod-42"}`, 0},  // negation sees resource attrs
		{`{__name__="m",mode="idle",job="api"}`, 1},   // dp attr AND resource attr
		{`{__name__="m",mode="idle",job="other"}`, 0}, //
		{`{__name__="m",k8s_pod_name=~"pod-.*"}`, 2},  // regex over resource attr
		{`{__name__="m",k8s_pod_name!~"pod-.*"}`, 0},  //
	}
	for _, c := range cases {
		t.Run(c.selector, func(t *testing.T) {
			p, mc := newSelectorTestProc(&mockSelectorProvider{selectors: []string{c.selector}})
			md := newResourceMD(resAttrs, func(sm pmetric.ScopeMetrics) {
				addGaugeDPs(sm, "m",
					map[string]string{"mode": "idle"},
					map[string]string{"mode": "user"},
				)
			})
			if err := p.ConsumeMetrics(context.Background(), md); err != nil {
				t.Fatal(err)
			}
			if got := mc.totalDatapoints(); got != c.wantDPs {
				t.Errorf("%s: want %d datapoints, got %d", c.selector, c.wantDPs, got)
			}
		})
	}
}

// Datapoint attributes override resource attributes on collision — the same
// precedence the tracker and the remote-write exporter apply.
func TestSelectorMode_DatapointOverridesResource(t *testing.T) {
	p, mc := newSelectorTestProc(&mockSelectorProvider{
		selectors: []string{`{__name__="m",env="dp"}`},
	})
	md := newResourceMD(map[string]string{"env": "res"}, func(sm pmetric.ScopeMetrics) {
		addGaugeDPs(sm, "m",
			map[string]string{"env": "dp"},
			map[string]string{}, // inherits env=res → must not match
		)
	})
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatal(err)
	}
	if got := mc.totalDatapoints(); got != 1 {
		t.Errorf("want 1 datapoint (dp attr overrides resource attr), got %d", got)
	}
}

// le matchers are IGNORED on histograms: the whole histogram is forwarded when
// the remaining matchers match (le only materializes at remote-write time).
func TestSelectorMode_LeIgnoredOnHistogram(t *testing.T) {
	p, mc := newSelectorTestProc(&mockSelectorProvider{
		selectors: []string{`{__name__="req_duration_bucket",le="0.5",method="GET"}`},
	})
	md := newResourceMD(nil, func(sm pmetric.ScopeMetrics) {
		addHistogramDPs(sm, "req_duration",
			map[string]string{"method": "GET"},
			map[string]string{"method": "POST"},
		)
	})
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatal(err)
	}
	if got := mc.totalDatapoints(); got != 1 {
		t.Errorf("want 1 histogram datapoint (method=GET kept whole, le ignored), got %d", got)
	}
}

// quantile matchers are equally ignored on summaries.
func TestSelectorMode_QuantileIgnoredOnSummary(t *testing.T) {
	p, mc := newSelectorTestProc(&mockSelectorProvider{
		selectors: []string{`{__name__="rpc_lat",quantile="0.99"}`},
	})
	md := newResourceMD(nil, func(sm pmetric.ScopeMetrics) {
		addSummaryDPs(sm, "rpc_lat", map[string]string{})
	})
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatal(err)
	}
	if got := mc.totalDatapoints(); got != 1 {
		t.Errorf("want the whole summary kept (quantile ignored), got %d datapoints", got)
	}
}

// _bucket/_sum/_count expansion: a selector on any component series keeps the
// histogram (family semantics identical to name mode).
func TestSelectorMode_HistogramFamilyExpansion(t *testing.T) {
	for _, sel := range []string{
		`{__name__="req_duration_bucket"}`,
		`{__name__="req_duration_sum"}`,
		`{__name__="req_duration_count"}`,
	} {
		t.Run(sel, func(t *testing.T) {
			p, mc := newSelectorTestProc(&mockSelectorProvider{selectors: []string{sel}})
			md := newResourceMD(nil, func(sm pmetric.ScopeMetrics) {
				addHistogramDPs(sm, "req_duration", map[string]string{})
			})
			if err := p.ConsumeMetrics(context.Background(), md); err != nil {
				t.Fatal(err)
			}
			if got := mc.totalDatapoints(); got != 1 {
				t.Errorf("%s: want histogram kept via family expansion, got %d datapoints", sel, got)
			}
		})
	}

	// The base name must NOT match a classic histogram (name-mode parity).
	p, mc := newSelectorTestProc(&mockSelectorProvider{selectors: []string{`{__name__="req_duration"}`}})
	md := newResourceMD(nil, func(sm pmetric.ScopeMetrics) {
		addHistogramDPs(sm, "req_duration", map[string]string{})
	})
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatal(err)
	}
	if got := mc.totalDatapoints(); got != 0 {
		t.Errorf("base-name selector must not match a classic histogram, got %d datapoints", got)
	}
}

// Regex-name selectors expand over the family names too.
func TestSelectorMode_RegexNameMatchesHistogramComponent(t *testing.T) {
	p, mc := newSelectorTestProc(&mockSelectorProvider{
		selectors: []string{`{__name__=~".*_duration_bucket"}`},
	})
	md := newResourceMD(nil, func(sm pmetric.ScopeMetrics) {
		addHistogramDPs(sm, "req_duration", map[string]string{})
	})
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatal(err)
	}
	if got := mc.totalDatapoints(); got != 1 {
		t.Errorf("regex over _bucket family name must keep the histogram, got %d datapoints", got)
	}
}

// Empty metrics/scopes/resources left behind by datapoint pruning are dropped.
func TestSelectorMode_PrunesEmptyContainers(t *testing.T) {
	p, mc := newSelectorTestProc(&mockSelectorProvider{
		selectors: []string{`{__name__="kept",mode="idle"}`},
	})
	md := pmetric.NewMetrics()
	rm1 := md.ResourceMetrics().AppendEmpty()
	addGaugeDPs(rm1.ScopeMetrics().AppendEmpty(), "kept", map[string]string{"mode": "idle"})
	rm2 := md.ResourceMetrics().AppendEmpty()
	addGaugeDPs(rm2.ScopeMetrics().AppendEmpty(), "kept", map[string]string{"mode": "user"})
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatal(err)
	}
	if n := mc.batches[0].ResourceMetrics().Len(); n != 1 {
		t.Errorf("want 1 ResourceMetrics after pruning, got %d", n)
	}
	if got := mc.totalDatapoints(); got != 1 {
		t.Errorf("want 1 datapoint, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// Mode selection / fail-closed
// ---------------------------------------------------------------------------

// Old server: empty selector list + non-empty names → name-level filtering
// exactly as today (whole metric forwarded regardless of labels).
func TestSelectorMode_EmptySelectorsFallsBackToNames(t *testing.T) {
	p, mc := newSelectorTestProc(&mockSelectorProvider{
		demands:   []string{"cpu_usage"},
		selectors: nil,
	})
	md := newResourceMD(nil, func(sm pmetric.ScopeMetrics) {
		addGaugeDPs(sm, "cpu_usage",
			map[string]string{"mode": "idle"},
			map[string]string{"mode": "user"},
		)
		addGaugeDPs(sm, "not_demanded", map[string]string{})
	})
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatal(err)
	}
	if got := mc.totalDatapoints(); got != 2 {
		t.Errorf("name fallback must forward ALL datapoints of the demanded metric, got %d", got)
	}
	if names := mc.receivedNames(); len(names) != 1 || names[0] != "cpu_usage" {
		t.Errorf("want [cpu_usage], got %v", names)
	}
}

// Selectors take precedence over names when both are present.
func TestSelectorMode_SelectorsPreferredOverNames(t *testing.T) {
	p, mc := newSelectorTestProc(&mockSelectorProvider{
		demands:   []string{"cpu_usage", "other"},
		selectors: []string{`{__name__="cpu_usage",mode="idle"}`},
	})
	md := newResourceMD(nil, func(sm pmetric.ScopeMetrics) {
		addGaugeDPs(sm, "cpu_usage",
			map[string]string{"mode": "idle"},
			map[string]string{"mode": "user"},
		)
		addGaugeDPs(sm, "other", map[string]string{})
	})
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatal(err)
	}
	if got := mc.totalDatapoints(); got != 1 {
		t.Errorf("selector mode must win over the names list, got %d datapoints", got)
	}
}

// Both lists empty → block all (fail-closed, unchanged).
func TestSelectorMode_BothEmptyBlocksAll(t *testing.T) {
	p, mc := newSelectorTestProc(&mockSelectorProvider{})
	md := newResourceMD(nil, func(sm pmetric.ScopeMetrics) {
		addGaugeDPs(sm, "anything", map[string]string{})
	})
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatal(err)
	}
	if got := mc.totalReceived(); got != 0 {
		t.Errorf("want 0 forwarded when demand empty, got %d", got)
	}
}

// A non-empty selector list where every selector is unparseable blocks all
// (fail-closed, mirroring the log/trace filters) — even when names exist.
func TestSelectorMode_AllUnparseableBlocksAll(t *testing.T) {
	p, mc := newSelectorTestProc(&mockSelectorProvider{
		demands:   []string{"cpu_usage"},
		selectors: []string{`not a selector`, `{broken`},
	})
	md := newResourceMD(nil, func(sm pmetric.ScopeMetrics) {
		addGaugeDPs(sm, "cpu_usage", map[string]string{})
	})
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatal(err)
	}
	if got := mc.totalReceived(); got != 0 {
		t.Errorf("want 0 forwarded when all selectors unparseable, got %d", got)
	}
}

// One bad selector must not poison the rest of the demand set.
func TestSelectorMode_BadSelectorSkipped(t *testing.T) {
	p, mc := newSelectorTestProc(&mockSelectorProvider{
		selectors: []string{`{broken`, `{__name__="ok_metric"}`},
	})
	md := newResourceMD(nil, func(sm pmetric.ScopeMetrics) {
		addGaugeDPs(sm, "ok_metric", map[string]string{})
	})
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatal(err)
	}
	if got := mc.totalDatapoints(); got != 1 {
		t.Errorf("parseable selector must still apply, got %d datapoints", got)
	}
}

// The compiled-selector cache is invalidated when the demand list changes.
func TestSelectorMode_CacheInvalidationOnDemandChange(t *testing.T) {
	provider := &mockSelectorProvider{selectors: []string{`{__name__="m",mode="idle"}`}}
	p, mc := newSelectorTestProc(provider)

	mkMD := func() pmetric.Metrics {
		return newResourceMD(nil, func(sm pmetric.ScopeMetrics) {
			addGaugeDPs(sm, "m",
				map[string]string{"mode": "idle"},
				map[string]string{"mode": "user"},
			)
		})
	}

	if err := p.ConsumeMetrics(context.Background(), mkMD()); err != nil {
		t.Fatal(err)
	}
	if got := mc.totalDatapoints(); got != 1 {
		t.Fatalf("first demand: want 1 datapoint, got %d", got)
	}

	// Demand change: the very next batch must use the new selectors.
	provider.selectors = []string{`{__name__="m",mode="user"}`, `{__name__="m",mode="system"}`}
	mc.batches = nil
	if err := p.ConsumeMetrics(context.Background(), mkMD()); err != nil {
		t.Fatal(err)
	}
	if got := mc.totalDatapoints(); got != 1 {
		t.Fatalf("after demand change: want 1 datapoint (mode=user), got %d", got)
	}

	// And the cache key must have moved on.
	if p.selCachedKey != `{__name__="m",mode="user"}`+"\x00"+`{__name__="m",mode="system"}` {
		t.Errorf("selector cache key not updated: %q", p.selCachedKey)
	}
}

// ---------------------------------------------------------------------------
// Benchmark — realistic gateway batch: 50 metrics × 20 datapoints, 5 selectors
// ---------------------------------------------------------------------------

func benchBatch() pmetric.Metrics {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	ra := rm.Resource().Attributes()
	ra.PutStr("service.name", "hostmetrics")
	ra.PutStr("service.instance.id", "web-1:9100")
	ra.PutStr("host.name", "web-1")
	ra.PutStr("os.type", "linux")
	ra.PutStr("k8s.node.name", "node-a")
	sm := rm.ScopeMetrics().AppendEmpty()

	modes := []string{"idle", "user", "system", "iowait", "steal"}
	for i := 0; i < 50; i++ {
		m := sm.Metrics().AppendEmpty()
		m.SetName(fmt.Sprintf("node_metric_%02d", i))
		g := m.SetEmptyGauge()
		for d := 0; d < 20; d++ {
			dp := g.DataPoints().AppendEmpty()
			dp.SetDoubleValue(float64(d))
			dp.Attributes().PutStr("cpu", fmt.Sprintf("%d", d%4))
			dp.Attributes().PutStr("mode", modes[d%len(modes)])
		}
	}
	return md
}

var benchSelectors = []string{
	`{__name__="node_metric_00",mode!="idle"}`,
	`{__name__="node_metric_07",cpu=~"0|3"}`,
	`{__name__="node_metric_13",mode="idle",cpu="0"}`,
	`{__name__="node_metric_21"}`,
	`{__name__=~"node_metric_4.",mode="user"}`,
}

type discardConsumer struct{}

func (discardConsumer) ConsumeMetrics(context.Context, pmetric.Metrics) error { return nil }
func (discardConsumer) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: false}
}

// BenchmarkConsumeMetricsSelectorMode measures selector-mode filtering on a
// realistic batch. Each iteration clones the base batch (the filter mutates
// in place); subtract BenchmarkBatchCloneBaseline for the pure filter cost.
func BenchmarkConsumeMetricsSelectorMode(b *testing.B) {
	p := newDemandFilterProcessor(zap.NewNop(), discardConsumer{}, &Config{})
	provider := &mockSelectorProvider{selectors: benchSelectors}
	p.provider = provider
	p.selectorProvider = provider

	base := benchBatch()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		md := pmetric.NewMetrics()
		base.CopyTo(md)
		if err := p.ConsumeMetrics(ctx, md); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkConsumeMetricsNameMode is the pre-existing name-level path on the
// same batch, for comparison.
func BenchmarkConsumeMetricsNameMode(b *testing.B) {
	p := newDemandFilterProcessor(zap.NewNop(), discardConsumer{}, &Config{})
	p.provider = &mockDemandProvider{demands: []string{
		"node_metric_00", "node_metric_07", "node_metric_13", "node_metric_21", "node_metric_42",
	}}

	base := benchBatch()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		md := pmetric.NewMetrics()
		base.CopyTo(md)
		if err := p.ConsumeMetrics(ctx, md); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkBatchCloneBaseline measures the per-iteration CopyTo overhead alone.
func BenchmarkBatchCloneBaseline(b *testing.B) {
	base := benchBatch()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		md := pmetric.NewMetrics()
		base.CopyTo(md)
	}
}
