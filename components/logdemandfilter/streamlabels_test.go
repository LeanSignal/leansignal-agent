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

package leansignallogdemandfilter

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

func TestStreamLabelsPromotion(t *testing.T) {
	attrs := pcommon.NewMap()
	attrs.PutStr("service.name", "api")
	attrs.PutStr("service.namespace", "shop")
	attrs.PutStr("service.instance.id", "i-123")
	attrs.PutStr("k8s.namespace.name", "prod")
	attrs.PutStr("k8s.pod.name", "api-0")
	attrs.PutStr("deployment.environment.name", "staging")
	attrs.PutStr("cloud.availability_zone", "eu-central-1a")
	attrs.PutStr("host.name", "node-1")        // NOT promoted by Loki's default list
	attrs.PutStr("process.pid", "42")          // NOT promoted
	attrs.PutStr("leansignal.mode", "central") // NOT promoted

	labels := streamLabels(attrs)

	want := map[string]string{
		"service_name":                "api",
		"service_namespace":           "shop",
		"service_instance_id":         "i-123",
		"k8s_namespace_name":          "prod",
		"k8s_pod_name":                "api-0",
		"deployment_environment_name": "staging",
		"cloud_availability_zone":     "eu-central-1a",
	}
	if len(labels) != len(want) {
		t.Errorf("labels: got %v, want exactly %v", labels, want)
	}
	for k, v := range want {
		if labels[k] != v {
			t.Errorf("label %s: got %q want %q", k, labels[k], v)
		}
	}
	for _, k := range []string{"host_name", "host.name", "process_pid", "leansignal_mode"} {
		if _, ok := labels[k]; ok {
			t.Errorf("label %s must not be promoted", k)
		}
	}
}

func TestStreamLabelsServiceNameDefault(t *testing.T) {
	// Loki defaults service_name to "unknown_service" when service.name is absent.
	attrs := pcommon.NewMap()
	attrs.PutStr("k8s.namespace.name", "prod")

	labels := streamLabels(attrs)
	if got := labels["service_name"]; got != "unknown_service" {
		t.Errorf("service_name default: got %q want %q", got, "unknown_service")
	}
	if got := labels["k8s_namespace_name"]; got != "prod" {
		t.Errorf("k8s_namespace_name: got %q", got)
	}

	// Present service.name wins over the default.
	attrs.PutStr("service.name", "api")
	if got := streamLabels(attrs)["service_name"]; got != "api" {
		t.Errorf("service_name: got %q want api", got)
	}
}

func TestStreamLabelsEmptyResource(t *testing.T) {
	labels := streamLabels(pcommon.NewMap())
	if len(labels) != 1 || labels["service_name"] != "unknown_service" {
		t.Errorf("empty resource labels: got %v, want only the service_name default", labels)
	}
}

func TestStreamLabelsNonStringAttribute(t *testing.T) {
	// Non-string attribute values are stringified (AsString), matching OTLP→Loki.
	attrs := pcommon.NewMap()
	attrs.PutInt("k8s.deployment.name", 7) // pathological, but must not panic
	if got := streamLabels(attrs)["k8s_deployment_name"]; got != "7" {
		t.Errorf("int attr: got %q want \"7\"", got)
	}
}
