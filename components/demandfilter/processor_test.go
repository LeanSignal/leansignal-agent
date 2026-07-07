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
	"sort"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// Mock helpers
// ---------------------------------------------------------------------------

type mockDemandProvider struct{ demands []string }

func (m *mockDemandProvider) GetDemands() []string { return m.demands }

// mockExtension satisfies both component.Component and DemandProvider so it
// can live in host.GetExtensions() and be type-asserted to DemandProvider.
type mockExtension struct{ *mockDemandProvider }

func (e *mockExtension) Start(_ context.Context, _ component.Host) error { return nil }
func (e *mockExtension) Shutdown(_ context.Context) error                { return nil }

// mockConsumer captures every ConsumeMetrics call.
type mockConsumer struct{ batches []pmetric.Metrics }

func (m *mockConsumer) ConsumeMetrics(_ context.Context, md pmetric.Metrics) error {
	m.batches = append(m.batches, md)
	return nil
}
func (m *mockConsumer) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: false}
}

func (m *mockConsumer) totalReceived() int {
	n := 0
	for _, md := range m.batches {
		n += md.MetricCount()
	}
	return n
}

func (m *mockConsumer) receivedNames() []string {
	var names []string
	for _, md := range m.batches {
		rms := md.ResourceMetrics()
		for i := 0; i < rms.Len(); i++ {
			sms := rms.At(i).ScopeMetrics()
			for j := 0; j < sms.Len(); j++ {
				ms := sms.At(j).Metrics()
				for k := 0; k < ms.Len(); k++ {
					names = append(names, ms.At(k).Name())
				}
			}
		}
	}
	sort.Strings(names)
	return names
}

// mockHost implements component.Host.
type mockHost struct {
	extensions map[component.ID]component.Component
}

func (h *mockHost) GetExtensions() map[component.ID]component.Component { return h.extensions }

// ---------------------------------------------------------------------------
// Metric builders
// ---------------------------------------------------------------------------

func addGauge(sm pmetric.ScopeMetrics, name string) {
	m := sm.Metrics().AppendEmpty()
	m.SetName(name)
	m.SetEmptyGauge().DataPoints().AppendEmpty()
}

func addMonotonicCumulativeSum(sm pmetric.ScopeMetrics, name string) {
	m := sm.Metrics().AppendEmpty()
	m.SetName(name)
	s := m.SetEmptySum()
	s.SetIsMonotonic(true)
	s.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	s.DataPoints().AppendEmpty()
}

func addNonMonotonicSum(sm pmetric.ScopeMetrics, name string) {
	m := sm.Metrics().AppendEmpty()
	m.SetName(name)
	s := m.SetEmptySum()
	s.SetIsMonotonic(false)
	s.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	s.DataPoints().AppendEmpty()
}

func addHistogram(sm pmetric.ScopeMetrics, name string) {
	m := sm.Metrics().AppendEmpty()
	m.SetName(name)
	h := m.SetEmptyHistogram()
	h.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	h.DataPoints().AppendEmpty()
}

func addSummary(sm pmetric.ScopeMetrics, name string) {
	m := sm.Metrics().AppendEmpty()
	m.SetName(name)
	m.SetEmptySummary().DataPoints().AppendEmpty()
}

// newMD builds a pmetric.Metrics with one ResourceMetrics/ScopeMetrics.
func newMD(fill func(sm pmetric.ScopeMetrics)) pmetric.Metrics {
	md := pmetric.NewMetrics()
	sm := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()
	fill(sm)
	return md
}

// ---------------------------------------------------------------------------
// Test processor helper
// ---------------------------------------------------------------------------

func newTestProc(provider DemandProvider) (*demandFilterProcessor, *mockConsumer) {
	mc := &mockConsumer{}
	p := newDemandFilterProcessor(zap.NewNop(), mc, &Config{})
	p.provider = provider
	return p, mc
}

// ---------------------------------------------------------------------------
// Construction
// ---------------------------------------------------------------------------

func TestNewDemandFilterProcessor_Defaults(t *testing.T) {
	mc := &mockConsumer{}
	p := newDemandFilterProcessor(zap.NewNop(), mc, &Config{})
	if p == nil {
		t.Fatal("expected non-nil processor")
	}
	if p.provider != nil {
		t.Error("provider should be nil before Start is called")
	}
}

func TestCapabilities(t *testing.T) {
	p, _ := newTestProc(&mockDemandProvider{})
	if !p.Capabilities().MutatesData {
		t.Error("MutatesData must be true — processor removes metrics")
	}
}

func TestShutdown(t *testing.T) {
	p, _ := newTestProc(&mockDemandProvider{})
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Start — DemandProvider discovery
// ---------------------------------------------------------------------------

func TestStart_NoDemandProviderExtension(t *testing.T) {
	mc := &mockConsumer{}
	p := newDemandFilterProcessor(zap.NewNop(), mc, &Config{})
	host := &mockHost{extensions: map[component.ID]component.Component{}}
	if err := p.Start(context.Background(), host); err != nil {
		t.Fatalf("Start returned unexpected error: %v", err)
	}
	if p.provider != nil {
		t.Error("provider should remain nil when no DemandProvider extension is registered")
	}
}

func TestStart_FindsDemandProviderExtension(t *testing.T) {
	mc := &mockConsumer{}
	p := newDemandFilterProcessor(zap.NewNop(), mc, &Config{})
	dp := &mockDemandProvider{demands: []string{"cpu_usage"}}
	ext := &mockExtension{mockDemandProvider: dp}
	extID := component.MustNewID("leansignal_edge_controller")
	host := &mockHost{extensions: map[component.ID]component.Component{extID: ext}}
	if err := p.Start(context.Background(), host); err != nil {
		t.Fatalf("Start returned unexpected error: %v", err)
	}
	if p.provider == nil {
		t.Fatal("provider should be set after a DemandProvider extension is found")
	}
	if got := p.provider.GetDemands(); len(got) != 1 || got[0] != "cpu_usage" {
		t.Errorf("unexpected demands: %v", got)
	}
}

// ---------------------------------------------------------------------------
// ConsumeMetrics — blocking behaviour
// ---------------------------------------------------------------------------

func TestConsumeMetrics_NilProvider_BlocksAll(t *testing.T) {
	mc := &mockConsumer{}
	// provider is nil: no extension registered at Start
	p := newDemandFilterProcessor(zap.NewNop(), mc, &Config{})
	md := newMD(func(sm pmetric.ScopeMetrics) { addGauge(sm, "some_metric") })
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := mc.totalReceived(); n != 0 {
		t.Errorf("want 0 forwarded, got %d", n)
	}
}

func TestConsumeMetrics_EmptyDemands_BlocksAll(t *testing.T) {
	p, mc := newTestProc(&mockDemandProvider{demands: []string{}})
	md := newMD(func(sm pmetric.ScopeMetrics) {
		addGauge(sm, "alpha")
		addGauge(sm, "beta")
	})
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := mc.totalReceived(); n != 0 {
		t.Errorf("want 0 forwarded when demand list empty, got %d", n)
	}
}

// ---------------------------------------------------------------------------
// ConsumeMetrics — filtering by metric type
// ---------------------------------------------------------------------------

func TestConsumeMetrics_AllDemanded(t *testing.T) {
	p, mc := newTestProc(&mockDemandProvider{demands: []string{"alpha", "beta"}})
	md := newMD(func(sm pmetric.ScopeMetrics) {
		addGauge(sm, "alpha")
		addGauge(sm, "beta")
	})
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := mc.totalReceived(); n != 2 {
		t.Errorf("want 2, got %d", n)
	}
}

func TestConsumeMetrics_PartialFilter(t *testing.T) {
	p, mc := newTestProc(&mockDemandProvider{demands: []string{"alpha"}})
	md := newMD(func(sm pmetric.ScopeMetrics) {
		addGauge(sm, "alpha") // demanded
		addGauge(sm, "beta")  // not demanded
		addGauge(sm, "gamma") // not demanded
	})
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := mc.totalReceived(); n != 1 {
		t.Errorf("want 1, got %d", n)
	}
	if names := mc.receivedNames(); len(names) != 1 || names[0] != "alpha" {
		t.Errorf("want [alpha], got %v", names)
	}
}

func TestConsumeMetrics_GaugeNormalization(t *testing.T) {
	// OTLP "node.cpu.usage" normalises to Prom "node_cpu_usage"
	p, mc := newTestProc(&mockDemandProvider{demands: []string{"node_cpu_usage"}})
	md := newMD(func(sm pmetric.ScopeMetrics) {
		addGauge(sm, "node.cpu.usage") // dots → underscores
		addGauge(sm, "other_metric")   // not demanded
	})
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := mc.totalReceived(); n != 1 {
		t.Errorf("want 1, got %d", n)
	}
}

func TestConsumeMetrics_MonotonicSum_TotalSuffix(t *testing.T) {
	// monotonic cumulative sum "http_requests" → Prom "http_requests_total"
	p, mc := newTestProc(&mockDemandProvider{demands: []string{"http_requests_total"}})
	md := newMD(func(sm pmetric.ScopeMetrics) { addMonotonicCumulativeSum(sm, "http_requests") })
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := mc.totalReceived(); n != 1 {
		t.Errorf("want 1, got %d", n)
	}
}

func TestConsumeMetrics_MonotonicSum_AlreadyTotalSuffix(t *testing.T) {
	// OTLP name already ends in "_total" — must not double-append
	p, mc := newTestProc(&mockDemandProvider{demands: []string{"http_requests_total"}})
	md := newMD(func(sm pmetric.ScopeMetrics) { addMonotonicCumulativeSum(sm, "http_requests_total") })
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := mc.totalReceived(); n != 1 {
		t.Errorf("want 1 (no double suffix), got %d", n)
	}
}

func TestConsumeMetrics_NonMonotonicSum_NoTotalSuffix(t *testing.T) {
	// non-monotonic sum is not a counter; Prom name is the bare base name
	p, mc := newTestProc(&mockDemandProvider{demands: []string{"current_connections"}})
	md := newMD(func(sm pmetric.ScopeMetrics) { addNonMonotonicSum(sm, "current_connections") })
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := mc.totalReceived(); n != 1 {
		t.Errorf("want 1, got %d", n)
	}
}

func TestConsumeMetrics_NonMonotonicSum_NotMatchedByTotalDemand(t *testing.T) {
	// demanding "current_connections_total" must NOT match a non-monotonic sum
	p, mc := newTestProc(&mockDemandProvider{demands: []string{"current_connections_total"}})
	md := newMD(func(sm pmetric.ScopeMetrics) { addNonMonotonicSum(sm, "current_connections") })
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := mc.totalReceived(); n != 0 {
		t.Errorf("want 0, got %d", n)
	}
}

func TestConsumeMetrics_Histogram_MatchByBucket(t *testing.T) {
	p, mc := newTestProc(&mockDemandProvider{demands: []string{"req_duration_seconds_bucket"}})
	md := newMD(func(sm pmetric.ScopeMetrics) { addHistogram(sm, "req_duration_seconds") })
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := mc.totalReceived(); n != 1 {
		t.Errorf("want 1, got %d", n)
	}
}

func TestConsumeMetrics_Histogram_MatchBySum(t *testing.T) {
	p, mc := newTestProc(&mockDemandProvider{demands: []string{"req_duration_seconds_sum"}})
	md := newMD(func(sm pmetric.ScopeMetrics) { addHistogram(sm, "req_duration_seconds") })
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := mc.totalReceived(); n != 1 {
		t.Errorf("want 1, got %d", n)
	}
}

func TestConsumeMetrics_Histogram_MatchByCount(t *testing.T) {
	p, mc := newTestProc(&mockDemandProvider{demands: []string{"req_duration_seconds_count"}})
	md := newMD(func(sm pmetric.ScopeMetrics) { addHistogram(sm, "req_duration_seconds") })
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := mc.totalReceived(); n != 1 {
		t.Errorf("want 1, got %d", n)
	}
}

func TestConsumeMetrics_Histogram_NotDemanded(t *testing.T) {
	p, mc := newTestProc(&mockDemandProvider{demands: []string{"other_metric"}})
	md := newMD(func(sm pmetric.ScopeMetrics) { addHistogram(sm, "req_duration_seconds") })
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := mc.totalReceived(); n != 0 {
		t.Errorf("want 0, got %d", n)
	}
}

func TestConsumeMetrics_Summary_MatchByBase(t *testing.T) {
	p, mc := newTestProc(&mockDemandProvider{demands: []string{"rpc_duration_seconds"}})
	md := newMD(func(sm pmetric.ScopeMetrics) { addSummary(sm, "rpc_duration_seconds") })
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := mc.totalReceived(); n != 1 {
		t.Errorf("want 1, got %d", n)
	}
}

func TestConsumeMetrics_Summary_MatchBySum(t *testing.T) {
	p, mc := newTestProc(&mockDemandProvider{demands: []string{"rpc_duration_seconds_sum"}})
	md := newMD(func(sm pmetric.ScopeMetrics) { addSummary(sm, "rpc_duration_seconds") })
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := mc.totalReceived(); n != 1 {
		t.Errorf("want 1, got %d", n)
	}
}

func TestConsumeMetrics_Summary_MatchByCount(t *testing.T) {
	p, mc := newTestProc(&mockDemandProvider{demands: []string{"rpc_duration_seconds_count"}})
	md := newMD(func(sm pmetric.ScopeMetrics) { addSummary(sm, "rpc_duration_seconds") })
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := mc.totalReceived(); n != 1 {
		t.Errorf("want 1, got %d", n)
	}
}

// ---------------------------------------------------------------------------
// ConsumeMetrics — empty containers are pruned
// ---------------------------------------------------------------------------

func TestConsumeMetrics_PrunesEmptyScopeMetrics(t *testing.T) {
	p, mc := newTestProc(&mockDemandProvider{demands: []string{"demanded"}})
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	sm1 := rm.ScopeMetrics().AppendEmpty()
	addGauge(sm1, "demanded")
	sm2 := rm.ScopeMetrics().AppendEmpty()
	addGauge(sm2, "not_demanded")
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := mc.totalReceived(); n != 1 {
		t.Errorf("want 1, got %d", n)
	}
	if n := mc.batches[0].ResourceMetrics().At(0).ScopeMetrics().Len(); n != 1 {
		t.Errorf("want 1 ScopeMetrics after pruning, got %d", n)
	}
}

func TestConsumeMetrics_PrunesEmptyResourceMetrics(t *testing.T) {
	p, mc := newTestProc(&mockDemandProvider{demands: []string{"demanded"}})
	md := pmetric.NewMetrics()
	rm1 := md.ResourceMetrics().AppendEmpty()
	addGauge(rm1.ScopeMetrics().AppendEmpty(), "demanded")
	rm2 := md.ResourceMetrics().AppendEmpty()
	addGauge(rm2.ScopeMetrics().AppendEmpty(), "not_demanded")
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := mc.totalReceived(); n != 1 {
		t.Errorf("want 1, got %d", n)
	}
	if n := mc.batches[0].ResourceMetrics().Len(); n != 1 {
		t.Errorf("want 1 ResourceMetrics after pruning, got %d", n)
	}
}

// ---------------------------------------------------------------------------
// Unit suffixes — the regression this fix addresses.
//
// hostmetrics emit dotted OTLP names with a separate unit (e.g. "system.cpu.time"
// unit "s"); the prometheusremotewrite exporter turns those into
// "system_cpu_time_seconds_total" in VictoriaMetrics, and dashboards (hence the
// demand list) reference that unit-suffixed name. The filter must reproduce the
// unit suffix or these metrics get dropped despite being demanded.
// ---------------------------------------------------------------------------

func addGaugeUnit(sm pmetric.ScopeMetrics, name, unit string) {
	m := sm.Metrics().AppendEmpty()
	m.SetName(name)
	m.SetUnit(unit)
	m.SetEmptyGauge().DataPoints().AppendEmpty()
}

func addMonotonicSumUnit(sm pmetric.ScopeMetrics, name, unit string) {
	m := sm.Metrics().AppendEmpty()
	m.SetName(name)
	m.SetUnit(unit)
	s := m.SetEmptySum()
	s.SetIsMonotonic(true)
	s.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	s.DataPoints().AppendEmpty()
}

func TestConsumeMetrics_UnitSuffix_MatchesExporterNames(t *testing.T) {
	// The real tenant-0001 demand set: dashboard PromQL references the exporter's
	// unit-suffixed names.
	p, mc := newTestProc(&mockDemandProvider{demands: []string{
		"system_cpu_time_seconds_total", // counter, unit "s"
		"system_disk_io_bytes_total",    // counter, unit "By"
		"system_network_io_bytes_total", // counter, unit "By"
		"system_filesystem_usage_bytes", // gauge, unit "By"
		"system_memory_usage_bytes",     // gauge, unit "By"
		"system_cpu_load_average_1m",    // gauge, no unit
	}})
	md := newMD(func(sm pmetric.ScopeMetrics) {
		addMonotonicSumUnit(sm, "system.cpu.time", "s")
		addMonotonicSumUnit(sm, "system.disk.io", "By")
		addMonotonicSumUnit(sm, "system.network.io", "By")
		addGaugeUnit(sm, "system.filesystem.usage", "By")
		addGaugeUnit(sm, "system.memory.usage", "By")
		addGaugeUnit(sm, "system.cpu.load_average.1m", "")
		addGaugeUnit(sm, "system.memory.utilization", "1") // NOT demanded → dropped
	})
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := mc.totalReceived(); n != 6 {
		t.Errorf("want 6 unit-suffixed metrics forwarded, got %d (names: %v)", n, mc.receivedNames())
	}
}

func TestConsumeMetrics_UnitSuffix_MissingUnitDoesNotMatch(t *testing.T) {
	// Guard against regressing to the old bug: demanding the *unit-less* name must
	// NOT match a metric that carries a unit, because the exporter never stores it
	// under that name.
	p, mc := newTestProc(&mockDemandProvider{demands: []string{"system_cpu_time_total"}})
	md := newMD(func(sm pmetric.ScopeMetrics) {
		addMonotonicSumUnit(sm, "system.cpu.time", "s") // → system_cpu_time_seconds_total
	})
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := mc.totalReceived(); n != 0 {
		t.Errorf("want 0 (unit-less demand must not match unit-bearing metric), got %d", n)
	}
}
