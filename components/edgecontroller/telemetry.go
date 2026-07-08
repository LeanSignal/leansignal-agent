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

// leansignaledgecontroller/telemetry.go
//
// The edge controller reports a handful of its own metrics through the
// collector's internal MeterProvider. Because they are emitted on the same
// provider as the otelcol_* metrics, they surface on the
// service.telemetry.metrics endpoint (127.0.0.1:8888) and — via the
// prometheus/internal receiver wired into the metrics/all pipeline — land in
// the local VM (and become demandable like any other series).
//
// Prometheus names (component instruments keep their own name; only the
// collector's built-in core metrics carry the otelcol_ prefix):
//
//	leansignal_edgecontroller_known_timeseries_cache_size
//	leansignal_edgecontroller_discovered_timeseries_cache_size
//	leansignal_edgecontroller_demand_timeseries_cache_size
//	leansignal_edgecontroller_pending_backend_updates
//	leansignal_edgecontroller_connection_up
//	leansignal_edgecontroller_connection_attempts_total
//	leansignal_edgecontroller_connection_established_total
package leansignaledgecontroller

import (
	"context"

	"go.opentelemetry.io/otel/metric"
)

// meterScope is the instrumentation scope for this component's self-metrics.
const meterScope = "github.com/leansignal/leansignal-agent/components/edgecontroller"

// controllerMetrics holds the edge controller's own OTel instruments.
type controllerMetrics struct {
	connectionAttempts     metric.Int64Counter
	connectionsEstablished metric.Int64Counter
	// reg is the async-gauge callback registration, unregistered on Shutdown.
	reg metric.Registration
}

// registerMetrics creates the edge-controller instruments on the given meter
// provider and wires the async gauges to observe live cache and stream state.
// Safe to call once, from Start. A nil provider (e.g. in unit tests) means the
// caller skips this entirely.
func (e *edgeControllerExtension) registerMetrics(mp metric.MeterProvider) error {
	meter := mp.Meter(meterScope)
	m := &controllerMetrics{}

	// Both counters are born on their first real increment (standard Prometheus
	// counter semantics); an Add(ctx, 0) seed is NOT exported by the SDK, so we
	// don't attempt one. Use the connection.up gauge (always present) to observe
	// the current stream state before the first (re)connect.
	var err error
	if m.connectionAttempts, err = meter.Int64Counter(
		"leansignal.edgecontroller.connection.attempts",
		metric.WithDescription("Total gRPC control-stream dial attempts; each reconnect increments this."),
	); err != nil {
		return err
	}
	if m.connectionsEstablished, err = meter.Int64Counter(
		"leansignal.edgecontroller.connection.established",
		metric.WithDescription("Total times the gRPC control stream was successfully (re)established."),
	); err != nil {
		return err
	}

	known, err := meter.Int64ObservableGauge(
		"leansignal.edgecontroller.known_timeseries_cache.size",
		metric.WithDescription("Current number of entries in the KnownTimeseriesCache."),
	)
	if err != nil {
		return err
	}
	discovered, err := meter.Int64ObservableGauge(
		"leansignal.edgecontroller.discovered_timeseries_cache.size",
		metric.WithDescription("Current number of entries in the DiscoveredTimeseriesCache."),
	)
	if err != nil {
		return err
	}
	demand, err := meter.Int64ObservableGauge(
		"leansignal.edgecontroller.demand_timeseries_cache.size",
		metric.WithDescription("Current number of demanded metric names in the DemandTimeseriesCache."),
	)
	if err != nil {
		return err
	}
	pending, err := meter.Int64ObservableGauge(
		"leansignal.edgecontroller.pending_backend_updates",
		metric.WithDescription("Known-cache entries not yet acknowledged by the backend."),
	)
	if err != nil {
		return err
	}
	connectionUp, err := meter.Int64ObservableGauge(
		"leansignal.edgecontroller.connection.up",
		metric.WithDescription("1 when the gRPC control stream is currently connected, else 0."),
	)
	if err != nil {
		return err
	}

	m.reg, err = meter.RegisterCallback(
		func(_ context.Context, o metric.Observer) error {
			o.ObserveInt64(known, int64(e.knownTimeseriesCache.GetSize()))
			o.ObserveInt64(discovered, int64(e.discoveredTimeseriesCache.GetSize()))
			o.ObserveInt64(demand, int64(e.demandTimeseriesCache.GetSize()))
			o.ObserveInt64(pending, int64(e.knownTimeseriesCache.GetPendingBackendUpdates()))
			o.ObserveInt64(connectionUp, e.connectedState())
			return nil
		},
		known, discovered, demand, pending, connectionUp,
	)
	if err != nil {
		return err
	}

	e.metrics = m
	return nil
}

// unregisterMetrics tears down the async-gauge callback. Safe to call when
// metrics were never registered.
func (e *edgeControllerExtension) unregisterMetrics() {
	if e.metrics != nil && e.metrics.reg != nil {
		_ = e.metrics.reg.Unregister()
	}
}

// recordConnectionAttempt counts one control-stream dial attempt.
func (e *edgeControllerExtension) recordConnectionAttempt(ctx context.Context) {
	if e.metrics != nil && e.metrics.connectionAttempts != nil {
		e.metrics.connectionAttempts.Add(ctx, 1)
	}
}

// recordConnectionEstablished counts one successfully (re)established stream.
func (e *edgeControllerExtension) recordConnectionEstablished(ctx context.Context) {
	if e.metrics != nil && e.metrics.connectionsEstablished != nil {
		e.metrics.connectionsEstablished.Add(ctx, 1)
	}
}

// connectedState returns 1 if the control stream is currently open, else 0.
func (e *edgeControllerExtension) connectedState() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stream != nil {
		return 1
	}
	return 0
}
