// Copyright 2026 LeanSignal
// SPDX-License-Identifier: Apache-2.0

package leansignaledgecontroller

import (
	"context"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/zap"
)

// Instrument names as defined in telemetry.go (the raw OTel instrument names,
// which is what the SDK ManualReader reports — before any Prometheus mangling).
const (
	mKnown       = "leansignal.edgecontroller.known_timeseries_cache.size"
	mDiscovered  = "leansignal.edgecontroller.discovered_timeseries_cache.size"
	mDemand      = "leansignal.edgecontroller.demand_timeseries_cache.size"
	mPending     = "leansignal.edgecontroller.pending_backend_updates"
	mConnUp      = "leansignal.edgecontroller.connection.up"
	mAttempts    = "leansignal.edgecontroller.connection.attempts"
	mEstablished = "leansignal.edgecontroller.connection.established"
)

// newTestMeter returns a manual-reader-backed MeterProvider for assertions.
func newTestMeter() (*sdkmetric.ManualReader, *sdkmetric.MeterProvider) {
	reader := sdkmetric.NewManualReader()
	return reader, sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
}

// collectInt64 collects all int64 gauge/sum data points keyed by instrument name.
// A missing name means the instrument produced no series in that collection.
func collectInt64(t *testing.T, reader *sdkmetric.ManualReader) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	out := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch d := m.Data.(type) {
			case metricdata.Gauge[int64]:
				for _, dp := range d.DataPoints {
					out[m.Name] = dp.Value
				}
			case metricdata.Sum[int64]:
				for _, dp := range d.DataPoints {
					out[m.Name] = dp.Value
				}
			}
		}
	}
	return out
}

func wantVal(t *testing.T, got map[string]int64, name string, want int64) {
	t.Helper()
	v, ok := got[name]
	if !ok {
		t.Fatalf("metric %q not present", name)
	}
	if v != want {
		t.Fatalf("metric %q = %d, want %d", name, v, want)
	}
}

// TestRegisterMetrics_ObservesState checks the async gauges read the live cache
// sizes and stream state. Known/discovered/demand are seeded to DIFFERENT values
// so a cross-wired observation (e.g. the known gauge reading the discovered
// cache) is caught.
func TestRegisterMetrics_ObservesState(t *testing.T) {
	e := newEdgeControllerExtension(zap.NewNop(), &Config{})
	e.knownTimeseriesCache.Init()
	e.discoveredTimeseriesCache.Init()
	e.demandTimeseriesCache.Init()

	e.knownTimeseriesCache.UpdateTimeseries(HashKey{1}, &TimeseriesEntry{MetricName: "up", Samples: 1})
	e.knownTimeseriesCache.UpdateTimeseries(HashKey{2}, &TimeseriesEntry{MetricName: "go_goroutines", Samples: 1})
	e.discoveredTimeseriesCache.Add(HashKey{3}, &TimeseriesEntry{MetricName: "node_load1"})
	e.demandTimeseriesCache.UpdateDemands([]string{"a", "b", "c"}, nil, nil, nil, 42)

	reader, mp := newTestMeter()
	if err := e.registerMetrics(mp); err != nil {
		t.Fatalf("registerMetrics: %v", err)
	}

	got := collectInt64(t, reader)
	wantVal(t, got, mKnown, 2)
	wantVal(t, got, mDiscovered, 1)
	wantVal(t, got, mDemand, 3)
	wantVal(t, got, mConnUp, 0) // stream is nil
	// pending is a live passthrough of the known cache's accessor.
	wantVal(t, got, mPending, int64(e.knownTimeseriesCache.GetPendingBackendUpdates()))
}

// TestConnectionCounters checks the sync counters increment per call.
func TestConnectionCounters(t *testing.T) {
	e := newEdgeControllerExtension(zap.NewNop(), &Config{})
	reader, mp := newTestMeter()
	if err := e.registerMetrics(mp); err != nil {
		t.Fatalf("registerMetrics: %v", err)
	}

	ctx := context.Background()
	e.recordConnectionAttempt(ctx)
	e.recordConnectionAttempt(ctx)
	e.recordConnectionAttempt(ctx)
	e.recordConnectionEstablished(ctx)

	got := collectInt64(t, reader)
	wantVal(t, got, mAttempts, 3)
	wantVal(t, got, mEstablished, 1)
}

// TestConnectionUpGauge_Connected uses the in-process control server to bring the
// stream up, then asserts the gauge (and connectedState) report 1.
func TestConnectionUpGauge_Connected(t *testing.T) {
	_, e, cleanup := startAgentAgainst(t, []string{"up"})
	defer cleanup()

	if got := e.connectedState(); got != 1 {
		t.Fatalf("connectedState = %d, want 1", got)
	}

	reader, mp := newTestMeter()
	if err := e.registerMetrics(mp); err != nil {
		t.Fatalf("registerMetrics: %v", err)
	}
	got := collectInt64(t, reader)
	wantVal(t, got, mConnUp, 1)
}

// TestConnectedState_Disconnected: a fresh extension has no stream.
func TestConnectedState_Disconnected(t *testing.T) {
	e := newEdgeControllerExtension(zap.NewNop(), &Config{})
	if got := e.connectedState(); got != 0 {
		t.Fatalf("connectedState = %d, want 0", got)
	}
}

// TestMetricRecorders_NilSafe: when metrics were never registered (e.g. a nil
// MeterProvider in tests), the recorders and unregister must be no-ops.
func TestMetricRecorders_NilSafe(t *testing.T) {
	e := newEdgeControllerExtension(zap.NewNop(), &Config{})
	// e.metrics is nil.
	e.recordConnectionAttempt(context.Background())
	e.recordConnectionEstablished(context.Background())
	e.unregisterMetrics()
	e.unregisterMetrics() // idempotent
}

// TestStart_RegistersMetrics covers the factory→Start wiring: when a
// MeterProvider is set (as the factory does from set.MeterProvider), Start
// registers the instruments and Shutdown tears them down cleanly. Uses an
// unreachable endpoint with long intervals so the connection loop stays idle.
func TestStart_RegistersMetrics(t *testing.T) {
	e := newEdgeControllerExtension(zap.NewNop(), &Config{
		Endpoint:          "127.0.0.1:1", // unreachable, fails fast
		ReconnectInterval: time.Hour,     // don't spin after the first failure
		PingInterval:      time.Hour,
	})
	reader, mp := newTestMeter()
	e.meterProvider = mp

	if err := e.Start(context.Background(), nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = e.Shutdown(context.Background()) }()

	if e.metrics == nil {
		t.Fatal("expected metrics registered after Start")
	}
	if _, ok := collectInt64(t, reader)[mConnUp]; !ok {
		t.Fatal("connection_up gauge not observable after Start")
	}
}

// TestUnregisterMetrics_StopsGauges: after unregister the async callback no longer
// runs, so the observable gauges produce no series on a subsequent collection.
func TestUnregisterMetrics_StopsGauges(t *testing.T) {
	e := newEdgeControllerExtension(zap.NewNop(), &Config{})
	e.knownTimeseriesCache.Init()
	e.discoveredTimeseriesCache.Init()
	e.demandTimeseriesCache.Init()
	e.knownTimeseriesCache.UpdateTimeseries(HashKey{1}, &TimeseriesEntry{MetricName: "up", Samples: 1})

	reader, mp := newTestMeter()
	if err := e.registerMetrics(mp); err != nil {
		t.Fatalf("registerMetrics: %v", err)
	}
	if got := collectInt64(t, reader); got[mKnown] != 1 {
		t.Fatalf("known gauge before unregister = %d, want 1", got[mKnown])
	}

	e.unregisterMetrics()
	if _, ok := collectInt64(t, reader)[mKnown]; ok {
		t.Fatalf("known gauge still observed after unregister")
	}
}
