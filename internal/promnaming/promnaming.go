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

// Package promnaming builds Prometheus metric names for OTLP metrics using the
// exact translator (github.com/prometheus/otlptranslator) that the
// prometheusremotewrite exporter uses. Routing the metrics tracker and the
// demand filter through here guarantees the names they compute match what the
// exporter writes to VictoriaMetrics — they cannot drift apart.
package promnaming

import (
	"github.com/prometheus/otlptranslator"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

// namer mirrors the prometheusremotewrite exporter's default translation
// strategy (UnderscoreEscapingWithSuffixes): escape to legacy Prometheus names
// and append unit + type suffixes. Build only reads these fields, so a shared
// package-level value is safe for concurrent use.
var namer = otlptranslator.MetricNamer{Namespace: "", WithMetricSuffixes: true, UTF8Allowed: false}

// BaseName returns the compliant Prometheus metric name for an OTLP metric — the
// unit suffix (e.g. _seconds, _bytes) plus _total for monotonic counters —
// WITHOUT the _bucket/_sum/_count family suffixes (callers append those per
// data-point type). This is the name the prometheusremotewrite exporter writes
// to VictoriaMetrics.
func BaseName(m pmetric.Metric) string {
	name, err := namer.Build(otlptranslator.Metric{
		Name: m.Name(),
		Unit: m.Unit(),
		Type: metricType(m),
	})
	if err != nil {
		return m.Name() // fall back to the raw name rather than an empty string
	}
	return name
}

// metricType maps the OTLP metric type to the translator's type, which drives
// the unit and _total suffix rules (only monotonic cumulative sums get _total).
func metricType(m pmetric.Metric) otlptranslator.MetricType {
	switch m.Type() {
	case pmetric.MetricTypeGauge:
		return otlptranslator.MetricTypeGauge
	case pmetric.MetricTypeSum:
		s := m.Sum()
		if s.IsMonotonic() && s.AggregationTemporality() == pmetric.AggregationTemporalityCumulative {
			return otlptranslator.MetricTypeMonotonicCounter
		}
		return otlptranslator.MetricTypeNonMonotonicCounter
	case pmetric.MetricTypeHistogram:
		return otlptranslator.MetricTypeHistogram
	case pmetric.MetricTypeExponentialHistogram:
		return otlptranslator.MetricTypeExponentialHistogram
	case pmetric.MetricTypeSummary:
		return otlptranslator.MetricTypeSummary
	}
	return otlptranslator.MetricTypeUnknown
}
