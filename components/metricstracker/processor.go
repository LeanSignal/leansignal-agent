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

// leansignalmetricstracker/processor.go
package leansignalmetricstracker

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	"github.com/leansignal/leansignal-agent/internal/promnaming"
)

// metricsTrackerProcessor implements processor.Metrics.
type metricsTrackerProcessor struct {
	logger *zap.Logger
	next   consumer.Metrics

	logMetrics bool
	logSeries  bool
}

func newMetricsTrackerProcessor(
	logger *zap.Logger,
	next consumer.Metrics,
	cfg *Config,
) *metricsTrackerProcessor {
	return &metricsTrackerProcessor{
		logger:     logger,
		next:       next,
		logMetrics: cfg.LogMetrics,
		logSeries:  cfg.LogSeries,
	}
}

// Start is part of component.Component.
func (p *metricsTrackerProcessor) Start(_ context.Context, _ component.Host) error {
	return nil
}

// Shutdown is part of component.Component.
func (p *metricsTrackerProcessor) Shutdown(_ context.Context) error {
	return nil
}

// Capabilities declares whether this processor mutates data.
func (p *metricsTrackerProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: false}
}

// ConsumeMetrics is the main entry point called by the pipeline.
//
// The pipeline may call ConsumeMetrics concurrently (a single processor instance
// is shared by every receiver feeding the pipeline), so the per-batch collector
// is allocated locally on each call rather than stored on the processor. This
// keeps the method free of shared mutable state and safe for concurrent use.
func (p *metricsTrackerProcessor) ConsumeMetrics(
	ctx context.Context,
	md pmetric.Metrics,
) error {
	// Per-call collector — never shared across goroutines.
	batch := NewCollectorTimeseries(p.logger)

	// Always process all metrics to collect timeseries for broadcast
	p.trackMetrics(md, batch)

	// Broadcast the batch to all registered receivers (e.g., edge controller)
	batch.Broadcast()

	return p.next.ConsumeMetrics(ctx, md)
}

func (p *metricsTrackerProcessor) trackMetrics(md pmetric.Metrics, batch *CollectorTimeseries) {

	/*
		Otel metrics are structured like this:
		Metrics
		└─ ResourceMetrics[]
			└─ ScopeMetrics[]
				└─ Metrics[]
						└─ DataPoints[]

		So it can receive:
		  2 resources:
			Pod A
				1 scope:
				3 metrics
			Pod B
				1 scope:
				2 metrics

		And we must iterate:
			Resource A
				Scope A
					Metric 1
					Metric 2
					Metric 3
			Resource B
				Scope B
					Metric 1
					Metric 2
	*/

	// get all ResourceMetrics
	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		rm := rms.At(i)

		resAttrs := rm.Resource().Attributes()
		/*
			we are inside a resource metric, e.g.:
			resource:
				service.name="api"
				pod="my-pod"

			so we extract the resource attributes
		*/

		sms := rm.ScopeMetrics()

		/*
			  instrumentation_scope:
				name: "prometheusreceiver"
				version: "0.140.1"

			  we will look through all the metrics inside this scope
		*/

		for j := 0; j < sms.Len(); j++ {
			sm := sms.At(j)

			metrics := sm.Metrics()

			/*
				now we are inside the metrics list:
				e.g.:
					container_cpu_usage_seconds_total
					container_memory_working_set_bytes
					k8s_pod_status_ready

				we will loop through all the metrics
				and process metric and timeseries.
			*/
			for k := 0; k < metrics.Len(); k++ {
				m := metrics.At(k)
				if p.logMetrics {
					p.logMetricProm(m)
				}
				// Always process timeseries for broadcast to receivers
				p.processTimeSeriesProm(resAttrs, m, batch)
			}
		}
	}
}

func (p *metricsTrackerProcessor) logMetric(name string) {
	p.logger.Debug("METRIC", zap.String("metric_name", name))
}

// logMetricProm logs the Prometheus-style metric name(s) that would be
// produced for a given OTLP metric.
//
// Note: this is intentionally *not* a full OTLP->Prometheus translation. It's a
// best-effort approximation to match the most common exporter behavior (e.g.
// counters get a _total suffix, histograms get _bucket/_sum/_count, summaries get
// quantile series + _sum/_count).
func (p *metricsTrackerProcessor) logMetricProm(m pmetric.Metric) {
	base := promnaming.BaseName(m)
	switch m.Type() {
	case pmetric.MetricTypeGauge:
		p.logMetric(base)
	case pmetric.MetricTypeSum:
		p.logMetric(base)
	case pmetric.MetricTypeHistogram:
		p.logMetric(base + "_bucket")
		p.logMetric(base + "_sum")
		p.logMetric(base + "_count")
	case pmetric.MetricTypeExponentialHistogram:
		// ExponentialHistogram maps naturally to Prometheus native histograms.
		// A native histogram is represented as a single time series (no _bucket/_sum/_count
		// suffixes in PromQL queries).
		p.logMetric(base)
	case pmetric.MetricTypeSummary:
		// Summary quantiles are represented as the base name with a "quantile" label.
		p.logMetric(base)
		p.logMetric(base + "_sum")
		p.logMetric(base + "_count")
	}
}

func (p *metricsTrackerProcessor) processTimeSeriesProm(
	resAttrs pcommon.Map,
	m pmetric.Metric,
	batch *CollectorTimeseries,
) {
	base := promnaming.BaseName(m)

	switch m.Type() {
	case pmetric.MetricTypeGauge:
		dps := m.Gauge().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			p.processPromSeries(base, resAttrs, dps.At(i).Attributes(), nil, batch)
		}

	case pmetric.MetricTypeSum:
		dps := m.Sum().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			p.processPromSeries(base, resAttrs, dps.At(i).Attributes(), nil, batch)
		}

	case pmetric.MetricTypeHistogram:
		bucketName := base + "_bucket"
		sumName := base + "_sum"
		countName := base + "_count"

		dps := m.Histogram().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			dp := dps.At(i)
			attrs := dp.Attributes()

			// Explode classic histogram into its Prometheus representation:
			//   <name>_bucket{le="..."}
			//   <name>_sum
			//   <name>_count
			bounds := dp.ExplicitBounds()
			for b := 0; b < bounds.Len(); b++ {
				p.processPromSeries(bucketName, resAttrs, attrs, map[string]string{
					"le": float64ToPromString(bounds.At(b)),
				}, batch)
			}
			// +Inf bucket is always present in Prometheus histograms.
			p.processPromSeries(bucketName, resAttrs, attrs, map[string]string{"le": "+Inf"}, batch)

			// Some OTLP histograms may omit sum; if so, skip it to better match Prometheus.
			if hasHistogramSum(dp) {
				p.processPromSeries(sumName, resAttrs, attrs, nil, batch)
			}
			p.processPromSeries(countName, resAttrs, attrs, nil, batch)
		}

	case pmetric.MetricTypeExponentialHistogram:
		// ExponentialHistogram maps naturally to Prometheus native histograms.
		// We treat it as a *single time series* with the base metric name.
		dps := m.ExponentialHistogram().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			p.processPromSeries(base, resAttrs, dps.At(i).Attributes(), nil, batch)
		}

	case pmetric.MetricTypeSummary:
		sumName := base + "_sum"
		countName := base + "_count"

		dps := m.Summary().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			dp := dps.At(i)
			attrs := dp.Attributes()

			// Explode summary into its Prometheus representation:
			//   <name>{quantile="..."}
			//   <name>_sum
			//   <name>_count
			qvs := dp.QuantileValues()
			for q := 0; q < qvs.Len(); q++ {
				qv := qvs.At(q)
				p.processPromSeries(base, resAttrs, attrs, map[string]string{
					"quantile": float64ToPromString(qv.Quantile()),
				}, batch)
			}

			p.processPromSeries(sumName, resAttrs, attrs, nil, batch)
			p.processPromSeries(countName, resAttrs, attrs, nil, batch)
		}
	}
}

func (p *metricsTrackerProcessor) processPromSeries(
	metricName string,
	resAttrs pcommon.Map,
	pointAttrs pcommon.Map,
	extraLabels map[string]string,
	batch *CollectorTimeseries,
) {
	labels := buildPromLabelMap(resAttrs, pointAttrs, extraLabels)

	// Add timeseries to the batch for broadcast to receivers
	batch.AddTimeseries(metricName, labels)

	// Optionally log the series
	if p.logSeries {
		fp := buildPromSeriesFingerprint(metricName, labels)
		p.logger.Debug("SERIES", zap.String("series_fingerprint", fp))
	}
}

func buildPromLabelMap(
	resAttrs pcommon.Map,
	pointAttrs pcommon.Map,
	extraLabels map[string]string,
) map[string]string {
	labels := make(map[string]string, resAttrs.Len()+pointAttrs.Len()+len(extraLabels)+2)

	// Resource attributes first (data point attributes can override).
	resAttrs.Range(func(k string, v pcommon.Value) bool {
		labels[normalizePromLabelName(k)] = v.AsString()
		return true
	})

	// Data point attributes override resource attributes on collision.
	pointAttrs.Range(func(k string, v pcommon.Value) bool {
		labels[normalizePromLabelName(k)] = v.AsString()
		return true
	})

	// Match Prometheus exporter behavior for job/instance.
	// If they are already present in the datapoint attributes, we keep them.
	if _, ok := labels["job"]; !ok {
		if job := jobLabelFromResource(resAttrs); job != "" {
			labels["job"] = job
		}
	}
	if _, ok := labels["instance"]; !ok {
		if inst := instanceLabelFromResource(resAttrs); inst != "" {
			labels["instance"] = inst
		}
	}

	// Extra labels (e.g. le/quantile) last.
	for k, v := range extraLabels {
		labels[normalizePromLabelName(k)] = v
	}

	return labels
}

func jobLabelFromResource(resAttrs pcommon.Map) string {
	// Prometheus remote-write exporter uses:
	//   job      = <service.name> or <service.namespace>/<service.name>
	//   instance = <service.instance.id>
	// when generating target_info and as default labels.
	// See Prometheus OTLP guide and OTel Collector PRW exporter docs.
	//
	// If either attribute is missing, we fall back gracefully.
	svcName := getAttrAsString(resAttrs, "service.name")
	if svcName == "" {
		return ""
	}
	svcNS := getAttrAsString(resAttrs, "service.namespace")
	if svcNS == "" {
		return svcName
	}
	return svcNS + "/" + svcName
}

func instanceLabelFromResource(resAttrs pcommon.Map) string {
	if inst := getAttrAsString(resAttrs, "service.instance.id"); inst != "" {
		return inst
	}
	// Pragmatic fallback: if a service instance id is not set, use host.name if present.
	if host := getAttrAsString(resAttrs, "host.name"); host != "" {
		return host
	}
	return ""
}

func getAttrAsString(m pcommon.Map, key string) string {
	v, ok := m.Get(key)
	if !ok {
		return ""
	}
	return v.AsString()
}

func buildPromSeriesFingerprint(metric string, labels map[string]string) string {
	var b strings.Builder
	b.WriteString(metric)
	if len(labels) == 0 {
		return b.String()
	}
	b.WriteString("{")
	writeSortedStringMap(&b, labels, "=", ",")
	b.WriteString("}")
	return b.String()
}

func writeSortedStringMap(b *strings.Builder, m map[string]string, kvSep string, entrySep string) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		if i > 0 {
			b.WriteString(entrySep)
		}
		b.WriteString(k)
		b.WriteString(kvSep)
		b.WriteString(strconv.Quote(m[k]))
	}
}

func float64ToPromString(v float64) string {
	// Use the same formatting style Prometheus uses for labels like `le` and `quantile`.
	// `-1` keeps as much precision as needed without trailing zeros.
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func hasHistogramSum(dp pmetric.HistogramDataPoint) bool {
	// OTLP histogram sum is optional. pdata has historically provided HasSum(),
	// but not all versions expose it the same way. If HasSum() exists, use it.
	// Otherwise, default to true (most histograms include sum).
	//
	// This is implemented using an interface check to keep compatibility across
	// pdata versions.
	type hasSumIface interface{ HasSum() bool }
	if hs, ok := any(dp).(hasSumIface); ok {
		return hs.HasSum()
	}
	// Some pdata versions may implement HasSum on pointer receivers.
	if hs, ok := any(&dp).(hasSumIface); ok {
		return hs.HasSum()
	}
	return true
}

// normalizePromLabelName implements (a subset of) the OpenTelemetry Collector's
// Prometheus label normalization (dots become underscores):
//   - Replace any non-alphanumeric rune with '_'
//   - If label starts with a digit, prefix "key_"
//   - If label starts with '_' (but not "__"), prefix "key" (yielding "key_<label>")
func normalizePromLabelName(label string) string {
	if label == "" {
		return label
	}

	label = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return '_'
	}, label)

	if unicode.IsDigit(rune(label[0])) {
		label = "key_" + label
	} else if strings.HasPrefix(label, "_") && !strings.HasPrefix(label, "__") {
		label = "key" + label
	}

	return label
}
