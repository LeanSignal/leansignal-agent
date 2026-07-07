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

// leansignalmetricsindex/promname.go
package leansignalmetricsindex

import (
	"github.com/prometheus/otlptranslator"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

// promNamer is the single authority for OTLP->Prometheus metric-name
// translation. It is configured to match the prometheusremotewrite exporter's
// default behaviour (add_metric_suffixes = true, i.e.
// UnderscoreEscapingWithSuffixes) so that the name derived here is byte-for-byte
// identical to the name the exporter writes to VictoriaMetrics.
//
// Both the metrics tracker (which reports the discovered index up to lean-api)
// and the demand filter (which matches incoming metrics against the demand list)
// go through this one function. That is deliberate: the demand list is built
// from dashboard PromQL, i.e. from the *exporter's* names, so anything matching
// against it must reproduce those exact names — units included. Rolling our own
// approximation is what caused unit-bearing metrics (e.g.
// system_cpu_time_seconds_total) to be silently dropped despite being demanded.
var promNamer = otlptranslator.NewMetricNamer("", otlptranslator.UnderscoreEscapingWithSuffixes)

// PromMetricBaseName returns the Prometheus metric name the prometheusremotewrite
// exporter produces for the given OTLP metric. It includes the unit suffix
// (e.g. _seconds, _bytes) and the _total suffix for monotonic counters, but does
// NOT append the per-series suffixes _bucket / _sum / _count for histograms and
// summaries — the exporter appends those per emitted series, and callers do the
// same on top of this base name.
func PromMetricBaseName(m pmetric.Metric) string {
	name, err := promNamer.Build(translatorMetric(m))
	if err != nil {
		// Build only errors when normalization collapses to an empty or
		// all-underscore name. Fall back to the raw OTLP name so matching
		// degrades gracefully instead of panicking on a pathological metric.
		return m.Name()
	}
	return name
}

// translatorMetric maps a pdata metric to the translator's Metric descriptor.
// It mirrors prometheus/prometheus'
// storage/remote/otlptranslator/prometheusremotewrite.TranslatorMetricFromOtelMetric
// (inlined here to avoid depending on the full prometheus module for 15 lines).
func translatorMetric(m pmetric.Metric) otlptranslator.Metric {
	tm := otlptranslator.Metric{
		Name: m.Name(),
		Unit: m.Unit(),
		Type: otlptranslator.MetricTypeUnknown,
	}
	switch m.Type() {
	case pmetric.MetricTypeGauge:
		tm.Type = otlptranslator.MetricTypeGauge
	case pmetric.MetricTypeSum:
		if m.Sum().IsMonotonic() {
			tm.Type = otlptranslator.MetricTypeMonotonicCounter
		} else {
			tm.Type = otlptranslator.MetricTypeNonMonotonicCounter
		}
	case pmetric.MetricTypeHistogram:
		tm.Type = otlptranslator.MetricTypeHistogram
	case pmetric.MetricTypeExponentialHistogram:
		tm.Type = otlptranslator.MetricTypeExponentialHistogram
	case pmetric.MetricTypeSummary:
		tm.Type = otlptranslator.MetricTypeSummary
	}
	return tm
}
