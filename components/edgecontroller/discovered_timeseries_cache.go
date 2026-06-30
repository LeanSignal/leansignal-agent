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

// leansignaledgecontroller/discovered_timeseries_cache.go
package leansignaledgecontroller

import (
	"sync"

	"github.com/leansignal/leansignal-agent/components/metricsindex"
	"go.uber.org/zap"
)

// Type aliases for cleaner code
type (
	HashKey         = leansignalmetricsindex.HashKey
	TimeseriesEntry = leansignalmetricsindex.TimeseriesEntry
	TimeseriesBatch = leansignalmetricsindex.TimeseriesBatch
	LabelPair       = leansignalmetricsindex.LabelPair
)

// DiscoveredTimeseriesItem represents a single timeseries in a batch for JSON serialization.
type DiscoveredTimeseriesItem struct {
	HashKey string   `json:"hashKey"`
	Name    string   `json:"name"`
	Labels  []string `json:"labels"` // Flattened: [key1, val1, key2, val2, ...]
}

// DiscoveredTimeseriesCache manages the cache of newly discovered timeseries data.
// All operations are thread-safe.
type DiscoveredTimeseriesCache struct {
	logger *zap.Logger
	mu     sync.RWMutex
	data   map[HashKey]*TimeseriesEntry
}

// NewDiscoveredTimeseriesCache creates a new DiscoveredTimeseriesCache instance.
func NewDiscoveredTimeseriesCache(logger *zap.Logger) *DiscoveredTimeseriesCache {
	return &DiscoveredTimeseriesCache{
		logger: logger,
		data:   make(map[HashKey]*TimeseriesEntry),
	}
}

// Init initializes (or reinitializes) the internal map.
func (c *DiscoveredTimeseriesCache) Init() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = make(map[HashKey]*TimeseriesEntry)
}

// Clear clears all entries from the cache.
func (c *DiscoveredTimeseriesCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = make(map[HashKey]*TimeseriesEntry)
}

// GetSize returns the number of entries in the cache.
func (c *DiscoveredTimeseriesCache) GetSize() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.data)
}

// Add adds a timeseries entry to the cache.
func (c *DiscoveredTimeseriesCache) Add(key HashKey, entry *TimeseriesEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = entry
}

// AddBatch adds multiple timeseries entries from a TimeseriesBatch to the cache.
func (c *DiscoveredTimeseriesCache) AddBatch(batch *TimeseriesBatch) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range batch.Data {
		c.data[key] = entry
	}
}

// GetBatch returns up to batchSize entries from the cache as a JSON-serializable slice.
// Returns the batch items and their corresponding hash keys (for later purging).
func (c *DiscoveredTimeseriesCache) GetBatch(batchSize int) ([]DiscoveredTimeseriesItem, []HashKey) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Determine actual batch size
	actualSize := len(c.data)
	if batchSize < actualSize {
		actualSize = batchSize
	}

	if actualSize == 0 {
		return nil, nil
	}

	items := make([]DiscoveredTimeseriesItem, 0, actualSize)
	keys := make([]HashKey, 0, actualSize)

	count := 0
	for key, entry := range c.data {
		if count >= actualSize {
			break
		}

		// Flatten labels into [key1, val1, key2, val2, ...]
		labels := make([]string, 0, len(entry.Labels)*2)
		for _, lp := range entry.Labels {
			labels = append(labels, lp.Name, lp.Value)
		}

		items = append(items, DiscoveredTimeseriesItem{
			HashKey: key.String(),
			Name:    entry.MetricName,
			Labels:  labels,
		})
		keys = append(keys, key)
		count++
	}

	return items, keys
}

// PurgeBatch removes the specified hash keys from the cache.
func (c *DiscoveredTimeseriesCache) PurgeBatch(keys []HashKey) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, key := range keys {
		delete(c.data, key)
	}
}
