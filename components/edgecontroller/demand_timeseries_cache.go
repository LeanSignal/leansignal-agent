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

// leansignaledgecontroller/demand_timeseries_cache.go
package leansignaledgecontroller

import (
	"github.com/leansignal/leansignal-agent/components/tracedemand"
	"sync"
	"time"

	"go.uber.org/zap"
)

// DemandTimeseriesCache holds the ordered list of demanded timeseries strings,
// the demanded LogQL stream selectors, the demanded trace resource selectors,
// the demanded metric series selectors, and the timestamp of the last update.
// All operations are thread-safe.
type DemandTimeseriesCache struct {
	logger          *zap.Logger
	mu              sync.RWMutex
	timeseries      []string
	logSelectors    []string // normalized LogQL stream selectors
	traceSelectors  []string // normalized trace resource selectors
	metricSelectors []string // normalized metric series selectors
	demandHash      uint64   // content hash of the applied demand set (0 = none)
	LastUpdate      int64    // unix timestamp (seconds) of the last update
	traceRoutes     []tracedemand.Route
	timeFunc        func() time.Time // injectable for testing
}

// UpdateTraceRoutes replaces the per-rule trace routing table. Called from the
// same demand_set command as UpdateDemands; kept separate so an older server
// that sends no routes simply clears it (and the filter falls back to the
// tenant-wide org) instead of changing UpdateDemands' signature.
func (c *DemandTimeseriesCache) UpdateTraceRoutes(routes []tracedemand.Route) {
	cp := make([]tracedemand.Route, len(routes))
	copy(cp, routes)

	c.mu.Lock()
	c.traceRoutes = cp
	c.mu.Unlock()
}

// DemandTimeseriesSnapshot is the value type returned by GetDemands.
type DemandTimeseriesSnapshot struct {
	Timeseries      []string
	LogSelectors    []string
	TraceSelectors  []string
	MetricSelectors []string
	// TraceRoutes pairs each trace selector with the filter demanding it, so
	// spans can be pushed to that rule's own Tempo org. Empty when the server
	// predates per-rule trace routing — the filter then falls back to
	// TraceSelectors and the tenant-wide org.
	TraceRoutes []tracedemand.Route
	DemandHash  uint64
	LastUpdate  int64
}

// NewDemandTimeseriesCache creates a new DemandTimeseriesCache instance.
func NewDemandTimeseriesCache(logger *zap.Logger) *DemandTimeseriesCache {
	return &DemandTimeseriesCache{
		logger:   logger,
		timeFunc: time.Now,
	}
}

// Init initialises (or re-initialises) the cache, clearing any existing data.
func (c *DemandTimeseriesCache) Init() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timeseries = nil
	c.logSelectors = nil
	c.traceSelectors = nil
	c.metricSelectors = nil
	c.demandHash = 0
	c.LastUpdate = 0
}

// GetDemands returns a snapshot of the current ordered timeseries list, the log
// stream selectors, the trace resource selectors, and the timestamp (seconds)
// of the last update. The returned slices are defensive copies so callers
// cannot mutate the internal state.
func (c *DemandTimeseriesCache) GetDemands() DemandTimeseriesSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cp := make([]string, len(c.timeseries))
	copy(cp, c.timeseries)
	lcp := make([]string, len(c.logSelectors))
	copy(lcp, c.logSelectors)
	tcp := make([]string, len(c.traceSelectors))
	copy(tcp, c.traceSelectors)
	mcp := make([]string, len(c.metricSelectors))
	copy(mcp, c.metricSelectors)
	rcp := make([]tracedemand.Route, len(c.traceRoutes))
	copy(rcp, c.traceRoutes)

	return DemandTimeseriesSnapshot{
		Timeseries:      cp,
		LogSelectors:    lcp,
		TraceSelectors:  tcp,
		MetricSelectors: mcp,
		TraceRoutes:     rcp,
		DemandHash:      c.demandHash,
		LastUpdate:      c.LastUpdate,
	}
}

// GetSize returns the number of demanded timeseries currently held.
func (c *DemandTimeseriesCache) GetSize() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.timeseries)
}

// setTimeFunc sets a custom time function for testing purposes.
func (c *DemandTimeseriesCache) setTimeFunc(fn func() time.Time) {
	c.timeFunc = fn
}

// UpdateDemands replaces the ordered timeseries list, the LogQL stream
// selector list, the trace resource selector list, and the metric series
// selector list with the provided slices, stores the server's content hash of
// them (covering all four lists), and records the current time as LastUpdate.
// The internal state stores copies of the provided slices so subsequent
// mutations by the caller are safe.
//
// Note: the leansignal_demand_filter / leansignal_log_demand_filter /
// leansignal_trace_demand_filter processors read
// GetDemands()/GetLogDemands()/GetTraceDemands()/GetMetricSelectors() live on
// every batch — there is no separate notification channel.  The new lists
// will be active for the very next batch that arrives after this call returns.
func (c *DemandTimeseriesCache) UpdateDemands(timeseries, logSelectors, traceSelectors, metricSelectors []string, hash uint64) {
	cp := make([]string, len(timeseries))
	copy(cp, timeseries)
	lcp := make([]string, len(logSelectors))
	copy(lcp, logSelectors)
	tcp := make([]string, len(traceSelectors))
	copy(tcp, traceSelectors)
	mcp := make([]string, len(metricSelectors))
	copy(mcp, metricSelectors)

	c.mu.Lock()
	defer c.mu.Unlock()

	c.timeseries = cp
	c.logSelectors = lcp
	c.traceSelectors = tcp
	c.metricSelectors = mcp
	c.demandHash = hash
	c.LastUpdate = c.timeFunc().Unix()

	c.logger.Info("demand list updated",
		zap.Int("demanded_metrics_count", len(cp)),
		zap.Int("demanded_log_selectors_count", len(lcp)),
		zap.Int("demanded_trace_selectors_count", len(tcp)),
		zap.Int("demanded_metric_selectors_count", len(mcp)),
		zap.Uint64("demand_hash", hash),
	)
}
