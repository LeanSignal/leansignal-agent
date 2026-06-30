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

// leansignalmetricstracker/collector_timeseries.go
package leansignalmetricstracker

import (
	"sort"
	"strconv"
	"strings"

	"github.com/leansignal/leansignal-agent/components/metricsindex"
	"github.com/zeebo/xxh3"
	"go.uber.org/zap"
)

// Type aliases for cleaner code
type (
	HashKey         = leansignalmetricsindex.HashKey
	TimeseriesEntry = leansignalmetricsindex.TimeseriesEntry
	TimeseriesBatch = leansignalmetricsindex.TimeseriesBatch
	LabelPair       = leansignalmetricsindex.LabelPair
)

// CollectorTimeseries manages a batch of timeseries keyed by xxhash128 fingerprint.
type CollectorTimeseries struct {
	logger *zap.Logger
	data   map[HashKey]*TimeseriesEntry
}

// NewCollectorTimeseries creates a new CollectorTimeseries instance.
func NewCollectorTimeseries(logger *zap.Logger) *CollectorTimeseries {
	return &CollectorTimeseries{
		logger: logger,
		data:   make(map[HashKey]*TimeseriesEntry),
	}
}

// Init initializes (or reinitializes) the internal map.
func (c *CollectorTimeseries) Init() {
	c.data = make(map[HashKey]*TimeseriesEntry)
}

// Clear clears the internal map.
func (c *CollectorTimeseries) Clear() {
	c.data = make(map[HashKey]*TimeseriesEntry)
}

// AddTimeseries adds a timeseries to the batch or increments its sample count if it already exists.
func (c *CollectorTimeseries) AddTimeseries(metric string, attributes map[string]string) {
	// Build fingerprint string: metric_name{label1="v1",label2="v2",...}
	fingerprint := buildFingerprintString(metric, attributes)

	// Calculate xxhash128 of the fingerprint
	hash := xxh3.Hash128([]byte(fingerprint))
	var hashKey HashKey
	// xxh3.Uint128 has Hi and Lo fields
	copy(hashKey[:8], uint64ToBytes(hash.Hi))
	copy(hashKey[8:], uint64ToBytes(hash.Lo))

	if entry, exists := c.data[hashKey]; exists {
		// Increment sample count
		entry.Samples++
	} else {
		// Create new entry
		c.data[hashKey] = &TimeseriesEntry{
			MetricName: metric,
			Labels:     extractSortedLabelPairs(attributes),
			Samples:    1,
		}
	}
}

// PrintTimeseries logs all timeseries in the format: xxhash128 , fingerprint , samples
func (c *CollectorTimeseries) PrintTimeseries() {
	for hashKey, entry := range c.data {
		hashHex := bytesToHex(hashKey[:])
		fingerprint := buildFingerprintFromEntry(entry)
		c.logger.Debug(hashHex + " , " + fingerprint + " , " + strconv.Itoa(entry.Samples))
	}
}

// Broadcast sends the current batch to all registered receivers.
// It creates a deep copy of the data so receivers can safely process it
// independently of the collector's internal state.
func (c *CollectorTimeseries) Broadcast() {
	// Deep copy the map and entries so receiver owns the memory
	dataCopy := make(map[HashKey]*TimeseriesEntry, len(c.data))
	for k, v := range c.data {
		// Copy the entry struct
		entryCopy := &TimeseriesEntry{
			MetricName: v.MetricName,
			Labels:     make([]LabelPair, len(v.Labels)),
			Samples:    v.Samples,
		}
		copy(entryCopy.Labels, v.Labels)
		dataCopy[k] = entryCopy
	}

	batch := &TimeseriesBatch{
		Data: dataCopy,
	}
	leansignalmetricsindex.BroadcastTimeseriesBatch(batch)
}

// buildFingerprintFromEntry constructs a Prometheus-style fingerprint from a TimeseriesEntry.
func buildFingerprintFromEntry(entry *TimeseriesEntry) string {
	if len(entry.Labels) == 0 {
		return entry.MetricName
	}

	var b strings.Builder
	b.WriteString(entry.MetricName)
	b.WriteString("{")

	for i, lp := range entry.Labels {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(lp.Name)
		b.WriteString("=")
		b.WriteString(strconv.Quote(lp.Value))
	}

	b.WriteString("}")
	return b.String()
}

// Len returns the number of unique timeseries in the batch.
func (c *CollectorTimeseries) Len() int {
	return len(c.data)
}

// buildFingerprintString creates a Prometheus-style fingerprint string:
// metric_name{label1="v1",label2="v2",...}
func buildFingerprintString(metric string, attributes map[string]string) string {
	if len(attributes) == 0 {
		return metric
	}

	var b strings.Builder
	b.WriteString(metric)
	b.WriteString("{")

	// Sort keys alphabetically
	keys := make([]string, 0, len(attributes))
	for k := range attributes {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(strconv.Quote(attributes[k]))
	}

	b.WriteString("}")
	return b.String()
}

// extractSortedLabelPairs extracts label pairs sorted by label name alphabetically.
func extractSortedLabelPairs(attributes map[string]string) []LabelPair {
	if len(attributes) == 0 {
		return nil
	}

	keys := make([]string, 0, len(attributes))
	for k := range attributes {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	pairs := make([]LabelPair, len(keys))
	for i, k := range keys {
		pairs[i] = LabelPair{Name: k, Value: attributes[k]}
	}
	return pairs
}

// uint64ToBytes converts a uint64 to a byte slice (big-endian).
func uint64ToBytes(v uint64) []byte {
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = byte(v & 0xff)
		v >>= 8
	}
	return b
}

// bytesToHex converts a byte slice to a hex string.
func bytesToHex(b []byte) string {
	const hexChars = "0123456789abcdef"
	result := make([]byte, len(b)*2)
	for i, v := range b {
		result[i*2] = hexChars[v>>4]
		result[i*2+1] = hexChars[v&0x0f]
	}
	return string(result)
}
