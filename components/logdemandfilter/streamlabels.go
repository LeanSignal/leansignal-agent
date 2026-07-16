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

// leansignallogdemandfilter/streamlabels.go
package leansignallogdemandfilter

import (
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

// defaultServiceName is the value Loki's OTLP ingestion assigns to the
// service_name index label when the service.name resource attribute is absent.
const defaultServiceName = "unknown_service"

// promotedResourceAttributes is the single source of truth for which OTLP
// resource attributes become Loki stream (index) labels. It mirrors Loki's
// default OTLP ingestion promotion — the distributor's
// `otlp_config.default_resource_attributes_as_index_labels` default list —
// with dots replaced by underscores in the resulting label names.
//
// Both Lokis (the agent-local one and the tenant one) run this same default,
// so evaluating demand selectors against these labels matches exactly what
// either Loki indexes. Do NOT customize otlp_config on either side, and keep
// this list in sync with the pinned Loki version's default.
var promotedResourceAttributes = []string{
	"cloud.availability_zone",
	"cloud.region",
	"container.name",
	"deployment.environment",
	"deployment.environment.name",
	"k8s.cluster.name",
	"k8s.container.name",
	"k8s.cronjob.name",
	"k8s.daemonset.name",
	"k8s.deployment.name",
	"k8s.job.name",
	"k8s.namespace.name",
	"k8s.pod.name",
	"k8s.replicaset.name",
	"k8s.statefulset.name",
	"service.instance.id",
	"service.name",
	"service.namespace",
}

// promotedLabelNames[i] is the stream-label name (dots → underscores) for
// promotedResourceAttributes[i]. Computed once at init.
var promotedLabelNames = func() []string {
	names := make([]string, len(promotedResourceAttributes))
	for i, attr := range promotedResourceAttributes {
		names[i] = strings.ReplaceAll(attr, ".", "_")
	}
	return names
}()

// streamLabels computes the Loki stream (index) labels for one resource —
// i.e. the labels a demand selector is evaluated against. service_name is
// always present (defaulted to "unknown_service"), exactly as Loki does on
// OTLP ingest.
func streamLabels(attrs pcommon.Map) map[string]string {
	labels := make(map[string]string, len(promotedResourceAttributes))
	for i, attr := range promotedResourceAttributes {
		if v, ok := attrs.Get(attr); ok {
			labels[promotedLabelNames[i]] = v.AsString()
		}
	}
	if _, ok := labels["service_name"]; !ok {
		labels["service_name"] = defaultServiceName
	}
	return labels
}
