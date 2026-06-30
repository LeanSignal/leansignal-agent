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

package leansignalmetricstracker

import (
	"testing"

	"github.com/leansignal/leansignal-agent/components/metricsindex"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

func TestNewCollectorTimeseries(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ct := NewCollectorTimeseries(logger)

	if ct == nil {
		t.Fatal("expected non-nil CollectorTimeseries")
	}
	if ct.data == nil {
		t.Fatal("expected non-nil data map")
	}
	if ct.Len() != 0 {
		t.Errorf("expected empty map, got %d entries", ct.Len())
	}
}

func TestCollectorTimeseries_Init(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ct := NewCollectorTimeseries(logger)

	// Add some data
	ct.AddTimeseries("test_metric", map[string]string{"label": "value"})
	if ct.Len() != 1 {
		t.Errorf("expected 1 entry, got %d", ct.Len())
	}

	// Init should clear the data
	ct.Init()
	if ct.Len() != 0 {
		t.Errorf("expected 0 entries after Init, got %d", ct.Len())
	}
}

func TestCollectorTimeseries_Clear(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ct := NewCollectorTimeseries(logger)

	// Add some data
	ct.AddTimeseries("test_metric", map[string]string{"label": "value"})
	ct.AddTimeseries("another_metric", map[string]string{"foo": "bar"})
	if ct.Len() != 2 {
		t.Errorf("expected 2 entries, got %d", ct.Len())
	}

	// Clear should remove all data
	ct.Clear()
	if ct.Len() != 0 {
		t.Errorf("expected 0 entries after Clear, got %d", ct.Len())
	}
}

func TestCollectorTimeseries_AddTimeseries_NewEntry(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ct := NewCollectorTimeseries(logger)

	ct.AddTimeseries("http_requests_total", map[string]string{
		"method":      "GET",
		"status_code": "200",
	})

	if ct.Len() != 1 {
		t.Errorf("expected 1 entry, got %d", ct.Len())
	}

	// Verify the entry exists with correct data
	for _, entry := range ct.data {
		if entry.MetricName != "http_requests_total" {
			t.Errorf("expected metric name 'http_requests_total', got '%s'", entry.MetricName)
		}
		if entry.Samples != 1 {
			t.Errorf("expected 1 sample, got %d", entry.Samples)
		}
		// Labels should be sorted alphabetically: method, status_code
		if len(entry.Labels) != 2 {
			t.Errorf("expected 2 labels, got %d", len(entry.Labels))
		}
		if entry.Labels[0].Name != "method" || entry.Labels[0].Value != "GET" {
			t.Errorf("expected first label 'method=GET', got '%s=%s'", entry.Labels[0].Name, entry.Labels[0].Value)
		}
		if entry.Labels[1].Name != "status_code" || entry.Labels[1].Value != "200" {
			t.Errorf("expected second label 'status_code=200', got '%s=%s'", entry.Labels[1].Name, entry.Labels[1].Value)
		}
	}
}

func TestCollectorTimeseries_AddTimeseries_IncrementSamples(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ct := NewCollectorTimeseries(logger)

	attrs := map[string]string{
		"service_name": "checkout",
		"http_method":  "GET",
	}

	// Add the same timeseries 5 times
	for i := 0; i < 5; i++ {
		ct.AddTimeseries("request_duration", attrs)
	}

	if ct.Len() != 1 {
		t.Errorf("expected 1 unique entry, got %d", ct.Len())
	}

	// Verify sample count
	for _, entry := range ct.data {
		if entry.Samples != 5 {
			t.Errorf("expected 5 samples, got %d", entry.Samples)
		}
	}
}

func TestCollectorTimeseries_AddTimeseries_DifferentLabelsAreDifferentSeries(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ct := NewCollectorTimeseries(logger)

	ct.AddTimeseries("http_requests_total", map[string]string{"status_code": "200"})
	ct.AddTimeseries("http_requests_total", map[string]string{"status_code": "404"})
	ct.AddTimeseries("http_requests_total", map[string]string{"status_code": "500"})

	if ct.Len() != 3 {
		t.Errorf("expected 3 unique entries, got %d", ct.Len())
	}
}

func TestCollectorTimeseries_AddTimeseries_NoLabels(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ct := NewCollectorTimeseries(logger)

	ct.AddTimeseries("up", nil)
	ct.AddTimeseries("up", map[string]string{})

	// Both should hash to the same fingerprint
	if ct.Len() != 1 {
		t.Errorf("expected 1 entry for metric with no labels, got %d", ct.Len())
	}

	for _, entry := range ct.data {
		fingerprint := buildFingerprintFromEntry(entry)
		if fingerprint != "up" {
			t.Errorf("expected fingerprint 'up', got '%s'", fingerprint)
		}
		if entry.Samples != 2 {
			t.Errorf("expected 2 samples, got %d", entry.Samples)
		}
	}
}

func TestCollectorTimeseries_LabelOrderDoesNotMatter(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ct := NewCollectorTimeseries(logger)

	// Add with labels in different order - should be the same series
	ct.AddTimeseries("test_metric", map[string]string{
		"a": "1",
		"b": "2",
		"c": "3",
	})
	ct.AddTimeseries("test_metric", map[string]string{
		"c": "3",
		"a": "1",
		"b": "2",
	})

	if ct.Len() != 1 {
		t.Errorf("expected 1 entry (same labels in different order), got %d", ct.Len())
	}

	for _, entry := range ct.data {
		if entry.Samples != 2 {
			t.Errorf("expected 2 samples, got %d", entry.Samples)
		}
		// Fingerprint should have labels in alphabetical order
		expectedFingerprint := `test_metric{a="1",b="2",c="3"}`
		fingerprint := buildFingerprintFromEntry(entry)
		if fingerprint != expectedFingerprint {
			t.Errorf("expected fingerprint '%s', got '%s'", expectedFingerprint, fingerprint)
		}
	}
}

func TestCollectorTimeseries_PrintTimeseries(t *testing.T) {
	// Use a test logger that captures output
	logger := zaptest.NewLogger(t)
	ct := NewCollectorTimeseries(logger)

	ct.AddTimeseries("metric_a", map[string]string{"label": "value"})
	ct.AddTimeseries("metric_b", nil)

	// This should not panic and should log the entries
	ct.PrintTimeseries()
}

func TestBuildFingerprintString(t *testing.T) {
	tests := []struct {
		name       string
		metric     string
		attributes map[string]string
		expected   string
	}{
		{
			name:       "no labels",
			metric:     "up",
			attributes: nil,
			expected:   "up",
		},
		{
			name:       "empty labels",
			metric:     "up",
			attributes: map[string]string{},
			expected:   "up",
		},
		{
			name:   "single label",
			metric: "http_requests_total",
			attributes: map[string]string{
				"method": "GET",
			},
			expected: `http_requests_total{method="GET"}`,
		},
		{
			name:   "multiple labels sorted",
			metric: "request_duration",
			attributes: map[string]string{
				"service_name": "checkout",
				"http_method":  "GET",
				"http_route":   "/cart",
				"status_code":  "200",
				"le":           "+Inf",
			},
			expected: `request_duration{http_method="GET",http_route="/cart",le="+Inf",service_name="checkout",status_code="200"}`,
		},
		{
			name:   "special characters in values",
			metric: "test_metric",
			attributes: map[string]string{
				"path": "/api/v1/users",
				"msg":  `hello "world"`,
			},
			expected: `test_metric{msg="hello \"world\"",path="/api/v1/users"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildFingerprintString(tt.metric, tt.attributes)
			if result != tt.expected {
				t.Errorf("expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestExtractSortedLabelPairs(t *testing.T) {
	tests := []struct {
		name       string
		attributes map[string]string
		expected   []leansignalmetricsindex.LabelPair
	}{
		{
			name:       "nil attributes",
			attributes: nil,
			expected:   nil,
		},
		{
			name:       "empty attributes",
			attributes: map[string]string{},
			expected:   nil,
		},
		{
			name: "sorted by key",
			attributes: map[string]string{
				"z_label": "z_value",
				"a_label": "a_value",
				"m_label": "m_value",
			},
			expected: []leansignalmetricsindex.LabelPair{
				{Name: "a_label", Value: "a_value"},
				{Name: "m_label", Value: "m_value"},
				{Name: "z_label", Value: "z_value"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractSortedLabelPairs(tt.attributes)
			if len(result) != len(tt.expected) {
				t.Errorf("expected %d pairs, got %d", len(tt.expected), len(result))
				return
			}
			for i, p := range result {
				if p.Name != tt.expected[i].Name || p.Value != tt.expected[i].Value {
					t.Errorf("at index %d: expected '%s=%s', got '%s=%s'", i, tt.expected[i].Name, tt.expected[i].Value, p.Name, p.Value)
				}
			}
		})
	}
}

func TestUint64ToBytes(t *testing.T) {
	tests := []struct {
		input    uint64
		expected []byte
	}{
		{0, []byte{0, 0, 0, 0, 0, 0, 0, 0}},
		{1, []byte{0, 0, 0, 0, 0, 0, 0, 1}},
		{256, []byte{0, 0, 0, 0, 0, 0, 1, 0}},
		{0xFFFFFFFFFFFFFFFF, []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}},
	}

	for _, tt := range tests {
		result := uint64ToBytes(tt.input)
		if len(result) != 8 {
			t.Errorf("expected 8 bytes, got %d", len(result))
			continue
		}
		for i, b := range result {
			if b != tt.expected[i] {
				t.Errorf("for input %d at byte %d: expected 0x%02X, got 0x%02X", tt.input, i, tt.expected[i], b)
			}
		}
	}
}

func TestBytesToHex(t *testing.T) {
	tests := []struct {
		input    []byte
		expected string
	}{
		{[]byte{}, ""},
		{[]byte{0x00}, "00"},
		{[]byte{0xFF}, "ff"},
		{[]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF}, "0123456789abcdef"},
	}

	for _, tt := range tests {
		result := bytesToHex(tt.input)
		if result != tt.expected {
			t.Errorf("for input %v: expected '%s', got '%s'", tt.input, tt.expected, result)
		}
	}
}

// Benchmark for AddTimeseries
func BenchmarkAddTimeseries(b *testing.B) {
	logger := zap.NewNop()
	ct := NewCollectorTimeseries(logger)

	attrs := map[string]string{
		"service_name": "checkout",
		"http_method":  "GET",
		"http_route":   "/cart",
		"status_code":  "200",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ct.AddTimeseries("http_requests_total", attrs)
	}
}

// Benchmark for building fingerprint string
func BenchmarkBuildFingerprintString(b *testing.B) {
	attrs := map[string]string{
		"service_name": "checkout",
		"http_method":  "GET",
		"http_route":   "/cart",
		"status_code":  "200",
		"le":           "+Inf",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buildFingerprintString("request_duration_bucket", attrs)
	}
}
