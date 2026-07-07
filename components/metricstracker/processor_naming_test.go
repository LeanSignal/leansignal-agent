// Copyright 2026 LeanSignal
// SPDX-License-Identifier: Apache-2.0

package leansignalmetricstracker

import (
	"context"
	"testing"

	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	metricsindex "github.com/leansignal/leansignal-agent/components/metricsindex"
)

// nameCapture collects the Prometheus metric names broadcast by the tracker.
type nameCapture struct{ names map[string]bool }

func (c *nameCapture) ReceiveTimeseriesBatch(b *metricsindex.TimeseriesBatch) {
	for _, e := range b.Data {
		c.names[e.MetricName] = true
	}
}

// End-to-end through ConsumeMetrics: gauge keeps its (normalized) name, a
// monotonic cumulative sum gets _total, and a histogram explodes into
// _bucket/_sum/_count.
func TestConsumeMetricsPromNaming(t *testing.T) {
	c := &nameCapture{names: map[string]bool{}}
	metricsindex.RegisterTimeseriesReceiver(c)
	defer metricsindex.UnregisterTimeseriesReceiver(c)

	md := pmetric.NewMetrics()
	sm := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()

	// gauge: dots normalized to underscores
	g := sm.Metrics().AppendEmpty()
	g.SetName("my.gauge")
	g.SetEmptyGauge().DataPoints().AppendEmpty()

	// monotonic cumulative sum -> counter -> _total
	s := sm.Metrics().AppendEmpty()
	s.SetName("http.requests")
	sum := s.SetEmptySum()
	sum.SetIsMonotonic(true)
	sum.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	sum.DataPoints().AppendEmpty()

	// histogram -> _bucket / _sum / _count
	h := sm.Metrics().AppendEmpty()
	h.SetName("request.latency")
	hist := h.SetEmptyHistogram()
	hist.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	dp := hist.DataPoints().AppendEmpty()
	dp.ExplicitBounds().FromRaw([]float64{0.1, 0.5})
	dp.SetSum(1.0)
	dp.SetCount(2)

	// summary -> base (quantile) / _sum / _count
	sy := sm.Metrics().AppendEmpty()
	sy.SetName("rpc.duration")
	summ := sy.SetEmptySummary()
	sdp := summ.DataPoints().AppendEmpty()
	sdp.SetSum(1.0)
	sdp.SetCount(2)
	qv := sdp.QuantileValues().AppendEmpty()
	qv.SetQuantile(0.99)
	qv.SetValue(0.1)

	// exponential histogram -> base name
	eh := sm.Metrics().AppendEmpty()
	eh.SetName("exp.hist")
	ehist := eh.SetEmptyExponentialHistogram()
	ehist.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	ehist.DataPoints().AppendEmpty()

	p := newMetricsTrackerProcessor(zap.NewNop(), nopMetricsConsumer{}, &Config{})
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"my_gauge",
		"http_requests_total",
		"request_latency_bucket",
		"request_latency_sum",
		"request_latency_count",
		"rpc_duration",
		"rpc_duration_sum",
		"rpc_duration_count",
		"exp_hist",
	} {
		if !c.names[want] {
			t.Errorf("expected broadcast to contain metric %q; got %v", want, keys(c.names))
		}
	}

	// Exercise the logging paths too (output discarded via Nop logger).
	pLog := newMetricsTrackerProcessor(zap.NewNop(), nopMetricsConsumer{}, &Config{LogMetrics: true, LogSeries: true})
	if err := pLog.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatal(err)
	}
}

// The tracker must report the exporter's unit-suffixed names up to lean-api, so
// the discovered index matches what actually lands in VictoriaMetrics.
func TestConsumeMetricsPromNaming_UnitSuffixes(t *testing.T) {
	c := &nameCapture{names: map[string]bool{}}
	metricsindex.RegisterTimeseriesReceiver(c)
	defer metricsindex.UnregisterTimeseriesReceiver(c)

	md := pmetric.NewMetrics()
	sm := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()

	// monotonic cumulative sum, unit "s" -> system_cpu_time_seconds_total
	s := sm.Metrics().AppendEmpty()
	s.SetName("system.cpu.time")
	s.SetUnit("s")
	sum := s.SetEmptySum()
	sum.SetIsMonotonic(true)
	sum.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	sum.DataPoints().AppendEmpty()

	// gauge, unit "By" -> system_memory_usage_bytes
	g := sm.Metrics().AppendEmpty()
	g.SetName("system.memory.usage")
	g.SetUnit("By")
	g.SetEmptyGauge().DataPoints().AppendEmpty()

	p := newMetricsTrackerProcessor(zap.NewNop(), nopMetricsConsumer{}, &Config{})
	if err := p.ConsumeMetrics(context.Background(), md); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"system_cpu_time_seconds_total", "system_memory_usage_bytes"} {
		if !c.names[want] {
			t.Errorf("expected broadcast to contain %q; got %v", want, keys(c.names))
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
