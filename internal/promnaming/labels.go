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

// promnaming/labels.go
//
// The Prometheus LABEL view of an OTLP series — the label-name normalization,
// resource-attribute flattening (parity with the exporter's
// resource_to_telemetry_conversion) and job/instance synthesis that the
// prometheusremotewrite path applies before writing to VictoriaMetrics.
//
// This is the single source of truth shared by the metrics tracker (which
// indexes every series under exactly these labels) and the demand filter
// (which matches demanded selectors against them): the two cannot drift
// apart, so a selector written against dataplane label names always sees the
// same label view the tracker reported.
package promnaming

import (
	"strings"
	"unicode"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

// NormalizeLabelName implements (a subset of) the OpenTelemetry Collector's
// Prometheus label normalization (dots become underscores):
//   - Replace any non-alphanumeric rune with '_'
//   - If label starts with a digit, prefix "key_"
//   - If label starts with '_' (but not "__"), prefix "key" (yielding "key_<label>")
func NormalizeLabelName(label string) string {
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

// BuildSeriesLabels flattens one series' full Prometheus label set:
// normalized resource attributes first, normalized datapoint attributes
// overriding them on collision, the synthesized job/instance labels when not
// already present, and finally any extra labels (e.g. le/quantile).
func BuildSeriesLabels(
	resAttrs pcommon.Map,
	pointAttrs pcommon.Map,
	extraLabels map[string]string,
) map[string]string {
	labels := make(map[string]string, resAttrs.Len()+pointAttrs.Len()+len(extraLabels)+2)

	// Resource attributes first (data point attributes can override).
	resAttrs.Range(func(k string, v pcommon.Value) bool {
		labels[NormalizeLabelName(k)] = v.AsString()
		return true
	})

	// Data point attributes override resource attributes on collision.
	pointAttrs.Range(func(k string, v pcommon.Value) bool {
		labels[NormalizeLabelName(k)] = v.AsString()
		return true
	})

	addSynthesizedJobInstance(labels, resAttrs)

	// Extra labels (e.g. le/quantile) last.
	for k, v := range extraLabels {
		labels[NormalizeLabelName(k)] = v
	}

	return labels
}

// ResourceLabelMap flattens ONLY the resource-level part of the series label
// view: normalized resource attributes plus the synthesized job/instance
// labels (when no resource attribute already provides them). Callers that
// evaluate matchers per datapoint compute this once per ResourceMetrics and
// look up datapoint attributes first (datapoint attributes override resource
// attributes) — that lookup order makes the result equivalent to
// BuildSeriesLabels for every label name.
func ResourceLabelMap(resAttrs pcommon.Map) map[string]string {
	labels := make(map[string]string, resAttrs.Len()+2)
	resAttrs.Range(func(k string, v pcommon.Value) bool {
		labels[NormalizeLabelName(k)] = v.AsString()
		return true
	})
	addSynthesizedJobInstance(labels, resAttrs)
	return labels
}

// addSynthesizedJobInstance matches the Prometheus remote-write exporter
// behavior for job/instance: synthesized from service.* resource attributes,
// but never overriding a job/instance label already present.
func addSynthesizedJobInstance(labels map[string]string, resAttrs pcommon.Map) {
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
