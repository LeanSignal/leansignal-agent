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

// leansignaldemandfilter/processor.go
package leansignaldemandfilter

import (
	"context"
	"strings"
	"unicode"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

// DemandProvider is the interface the filter expects the leansignal_edge_controller
// extension to satisfy.  It is defined here so that the filter owns its own
// abstraction and has no module-level dependency on the edge-controller package.
type DemandProvider interface {
	GetDemands() []string
}

// demandFilterProcessor filters OTLP metrics by the current demand list held in
// the leansignal_edge_controller extension.  Only metrics whose Prometheus-style
// name(s) appear in the demand list are forwarded to the next consumer; all
// others are dropped.
//
// The extension is discovered at Start() time via component.Host.GetExtensions().
// If no DemandProvider extension is found, or if the demand list is empty, ALL
// metrics are blocked — vms-dataplane only ever receives what is explicitly demanded.
type demandFilterProcessor struct {
	logger   *zap.Logger
	next     consumer.Metrics
	cfg      *Config
	provider DemandProvider // resolved in Start() from the registered extension
}

func newDemandFilterProcessor(
	logger *zap.Logger,
	next consumer.Metrics,
	cfg *Config,
) *demandFilterProcessor {
	return &demandFilterProcessor{
		logger: logger,
		next:   next,
		cfg:    cfg,
	}
}

// Start resolves the DemandProvider by iterating the registered extensions and
// finding the first one that satisfies the DemandProvider interface (i.e. the
// leansignal_edge_controller extension).
func (p *demandFilterProcessor) Start(_ context.Context, host component.Host) error {
	for _, ext := range host.GetExtensions() {
		if dp, ok := ext.(DemandProvider); ok {
			p.provider = dp
			p.logger.Info("demand filter: connected to DemandProvider extension")
			return nil
		}
	}
	p.logger.Warn("demand filter: no DemandProvider extension found; all metrics will be blocked")
	return nil
}

// Shutdown is part of component.Component.
func (p *demandFilterProcessor) Shutdown(_ context.Context) error {
	return nil
}

// Capabilities declares that this processor mutates data (it removes metrics).
func (p *demandFilterProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

// ConsumeMetrics filters the incoming batch by the current demand list.
//
// The demand list is read fresh on every call directly from the
// DemandTimeseriesCache via the edge-controller extension — there is no
// separate propagation mechanism.  The moment the edge-controller receives a
// demand_set command from the backend and calls UpdateDemands, the very next
// batch processed here will use the new list.
// When the demand list is empty, all metrics are blocked (nothing forwarded).
func (p *demandFilterProcessor) ConsumeMetrics(ctx context.Context, md pmetric.Metrics) error {
	// Read the demand list fresh – p.provider.GetDemands() queries the
	// DemandTimeseriesCache in the extension directly on every call.
	totalMetrics := md.MetricCount()

	if p.provider == nil {
		p.logger.Info("demand filter: no DemandProvider extension, blocking all metrics",
			zap.Int("received", totalMetrics),
			zap.Int("allowed", 0),
		)
		return nil
	}

	demands := p.provider.GetDemands()

	// Block everything when no demand list has been received yet.
	// vms-dataplane only ever receives what the backend explicitly demands.
	if len(demands) == 0 {
		p.logger.Info("demand filter: no demand list yet, blocking all metrics",
			zap.Int("received", totalMetrics),
			zap.Int("allowed", 0),
		)
		return nil
	}

	// Build an O(1) lookup set from the demand slice.
	demandSet := make(map[string]struct{}, len(demands))
	for _, d := range demands {
		demandSet[d] = struct{}{}
	}

	// Remove every metric that is not demanded.
	// RemoveIf walks the slice in-place, removing items for which the predicate
	// returns true.  We then prune empty ScopeMetrics and ResourceMetrics.
	rms := md.ResourceMetrics()
	rms.RemoveIf(func(rm pmetric.ResourceMetrics) bool {
		sms := rm.ScopeMetrics()
		sms.RemoveIf(func(sm pmetric.ScopeMetrics) bool {
			metrics := sm.Metrics()
			metrics.RemoveIf(func(m pmetric.Metric) bool {
				if p.isMetricDemanded(m, demandSet) {
					return false // keep
				}
				if p.cfg.LogFiltered {
					p.logger.Debug("demand filter: dropping metric",
						zap.String("metric", m.Name()),
					)
				}
				return true // drop
			})
			return metrics.Len() == 0
		})
		return sms.Len() == 0
	})

	allowedMetrics := md.MetricCount()
	p.logger.Info("demand filter: batch filtered",
		zap.Int("received", totalMetrics),
		zap.Int("allowed", allowedMetrics),
		zap.Int("dropped", totalMetrics-allowedMetrics),
		zap.Int("demand_size", len(demands)),
	)

	return p.next.ConsumeMetrics(ctx, md)
}

// isMetricDemanded returns true if any of the Prometheus names that would be
// produced for this OTLP metric are present in the demand set.
//
// The mapping mirrors the logic in leansignalmetricstracker.processTimeSeriesProm
// so that the names used for demand matching are identical to the names that were
// originally discovered and stored in the backend.
func (p *demandFilterProcessor) isMetricDemanded(m pmetric.Metric, demandSet map[string]struct{}) bool {
	base := normalizePromMetricName(m.Name())

	switch m.Type() {
	case pmetric.MetricTypeGauge:
		_, ok := demandSet[base]
		return ok

	case pmetric.MetricTypeSum:
		promName := base
		if isPromCounter(m) {
			promName = ensureTotalSuffix(promName)
		}
		_, ok := demandSet[promName]
		return ok

	case pmetric.MetricTypeHistogram:
		// Any of the three histogram series suffices to keep the full histogram.
		_, hasBucket := demandSet[base+"_bucket"]
		_, hasSum := demandSet[base+"_sum"]
		_, hasCount := demandSet[base+"_count"]
		return hasBucket || hasSum || hasCount

	case pmetric.MetricTypeExponentialHistogram:
		_, ok := demandSet[base]
		return ok

	case pmetric.MetricTypeSummary:
		// Any of base / _sum / _count suffices.
		_, hasBase := demandSet[base]
		_, hasSum := demandSet[base+"_sum"]
		_, hasCount := demandSet[base+"_count"]
		return hasBase || hasSum || hasCount
	}

	return false
}

// ---------------------------------------------------------------------------
// Prometheus normalization helpers
//
// These are intentionally mirrored from leansignalmetricstracker so that
// this package has no dependency on that module while still producing
// identical Prometheus names.
// ---------------------------------------------------------------------------

// normalizePromMetricName approximates the OTLP→Prometheus metric-name
// translation used by the OTel Collector's Prometheus exporter.
func normalizePromMetricName(name string) string {
	if name == "" {
		return name
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == ':' {
			return r
		}
		return '_'
	}, name)
	name = collapseUnderscores(name)
	name = strings.Trim(name, "_")
	if name == "" {
		return "_"
	}
	if unicode.IsDigit(rune(name[0])) {
		name = "_" + name
	}
	return name
}

func collapseUnderscores(s string) string {
	if !strings.Contains(s, "__") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	prevUnderscore := false
	for _, r := range s {
		if r == '_' {
			if prevUnderscore {
				continue
			}
			prevUnderscore = true
			b.WriteRune(r)
			continue
		}
		prevUnderscore = false
		b.WriteRune(r)
	}
	return b.String()
}

func ensureTotalSuffix(name string) string {
	if strings.HasSuffix(name, "_total") {
		return name
	}
	return name + "_total"
}

func isPromCounter(m pmetric.Metric) bool {
	if m.Type() != pmetric.MetricTypeSum {
		return false
	}
	s := m.Sum()
	return s.IsMonotonic() && s.AggregationTemporality() == pmetric.AggregationTemporalityCumulative
}
