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

// leansignaledgecontroller/known_timeseries_cache.go
package leansignaledgecontroller

import (
	"sync"
	"time"

	"go.uber.org/zap"
)

// HoursLog is the number of hours to keep in the ring buffer.
const HoursLog = 8

// TargetBackendUpdateMinutes is the target interval (in minutes) for syncing timeseries to the backend.
// Timeseries not synced within this interval are considered pending.
const TargetBackendUpdateMinutes = 1

// KnownTimeseriesEntry represents a known timeseries with its metadata and hourly samples.
type KnownTimeseriesEntry struct {
	MetricName      string          // Prometheus-normalised metric name of the series
	LastUpdate      int64           // unix timestamp (seconds) of last sample update
	LastBackendSync int64           // unix timestamp (seconds) of last backend sync
	lastHour        int64           // unix hour (timestamp / 3600) when ring buffer was last updated
	head            uint8           // current position in ring buffer (0 to HoursLog-1)
	samples         [HoursLog]int64 // ring buffer of samples per hour
}

// KnownActiveTimeseriesItem represents an active timeseries for batch operations.
type KnownActiveTimeseriesItem struct {
	HashKey      HashKey `json:"hashKey"`
	TotalSamples int64   `json:"samples"`    // sum of all hourly samples
	LastUpdate   int64   `json:"lastUpdate"` // unix timestamp milliseconds
}

// KnownTimeseriesCache manages the cache of known timeseries data.
// All operations are thread-safe.
type KnownTimeseriesCache struct {
	logger   *zap.Logger
	mu       sync.RWMutex
	data     map[HashKey]*KnownTimeseriesEntry
	timeFunc func() time.Time // for testing - allows injecting time
}

// NewKnownTimeseriesCache creates a new KnownTimeseriesCache instance.
func NewKnownTimeseriesCache(logger *zap.Logger) *KnownTimeseriesCache {
	return &KnownTimeseriesCache{
		logger:   logger,
		data:     make(map[HashKey]*KnownTimeseriesEntry),
		timeFunc: time.Now,
	}
}

// Init initializes (or reinitializes) the internal map.
func (c *KnownTimeseriesCache) Init() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = make(map[HashKey]*KnownTimeseriesEntry)
}

// Clear clears all entries from the cache.
func (c *KnownTimeseriesCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = make(map[HashKey]*KnownTimeseriesEntry)
}

// GetSize returns the number of entries in the cache.
func (c *KnownTimeseriesCache) GetSize() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.data)
}

// GetPendingBackendUpdates returns the count of timeseries that have not been
// synced to the backend within the TargetBackendUpdateMinutes interval.
func (c *KnownTimeseriesCache) GetPendingBackendUpdates() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	threshold := c.timeFunc().Unix() - (TargetBackendUpdateMinutes * 60)
	count := 0
	for _, entry := range c.data {
		if entry.LastBackendSync < threshold {
			count++
		}
	}
	return count
}

// CountDemanded returns the number of known series whose metric name is in the
// demanded set (as produced by expandDemandNames) — i.e. the series the demand
// filter forwards to the dataplane VM.
func (c *KnownTimeseriesCache) CountDemanded(demanded map[string]struct{}) int {
	if len(demanded) == 0 {
		return 0
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	count := 0
	for _, entry := range c.data {
		if _, ok := demanded[entry.MetricName]; ok {
			count++
		}
	}
	return count
}

// MetricNameSet returns the set of distinct metric names across all known series.
func (c *KnownTimeseriesCache) MetricNameSet() map[string]struct{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	set := make(map[string]struct{})
	for _, entry := range c.data {
		set[entry.MetricName] = struct{}{}
	}
	return set
}

// IsTimeseriesKnown returns true if the timeseries with the given key exists in the cache.
func (c *KnownTimeseriesCache) IsTimeseriesKnown(key HashKey) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, exists := c.data[key]
	return exists
}

// UpdateTimeseries updates or adds a timeseries entry in the cache.
// It increments the samples for the current hour with entry.Samples
// and sets LastUpdate to the current timestamp.
// If adding a new entry, all samples are initialized to 0 before adding.
func (c *KnownTimeseriesCache) UpdateTimeseries(key HashKey, entry *TimeseriesEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.timeFunc()
	nowUnix := now.Unix()
	currentHour := nowUnix / 3600

	existing, exists := c.data[key]
	if !exists {
		// Create new entry with all samples at 0
		newEntry := &KnownTimeseriesEntry{
			MetricName:      entry.MetricName,
			LastUpdate:      nowUnix,
			LastBackendSync: 0,
			lastHour:        currentHour,
			head:            uint8(currentHour % HoursLog),
			samples:         [HoursLog]int64{},
		}
		// Add samples to current hour slot
		newEntry.samples[newEntry.head] = int64(entry.Samples)
		c.data[key] = newEntry
		return
	}

	// Update existing entry
	c.advanceRingBuffer(existing, currentHour)
	existing.samples[existing.head] += int64(entry.Samples)
	existing.LastUpdate = nowUnix
}

// advanceRingBuffer advances the ring buffer to the current hour,
// zeroing out any slots that have become stale.
// Must be called with lock held.
func (c *KnownTimeseriesCache) advanceRingBuffer(entry *KnownTimeseriesEntry, currentHour int64) {
	if currentHour <= entry.lastHour {
		// No time has passed or time went backwards
		return
	}

	hoursPassed := currentHour - entry.lastHour

	if hoursPassed >= HoursLog {
		// All slots are stale, clear everything
		for i := range entry.samples {
			entry.samples[i] = 0
		}
		entry.head = uint8(currentHour % HoursLog)
	} else {
		// Advance head and clear new slots
		for i := int64(0); i < hoursPassed; i++ {
			entry.head = (entry.head + 1) % HoursLog
			entry.samples[entry.head] = 0
		}
	}

	entry.lastHour = currentHour
}

// GetActiveTimeseriesBatch returns up to batchSize entries that:
// 1. Have LastBackendSync older than lastBackendSyncOlderThan
// 2. Have at least one non-zero sample (are active)
// Returns a slice of KnownActiveTimeseriesItem with HashKey and total samples sum.
func (c *KnownTimeseriesCache) GetActiveTimeseriesBatch(lastBackendSyncOlderThan int64, batchSize int) []KnownActiveTimeseriesItem {
	c.mu.Lock()
	defer c.mu.Unlock()

	if batchSize <= 0 {
		return nil
	}

	now := c.timeFunc()
	currentHour := now.Unix() / 3600

	items := make([]KnownActiveTimeseriesItem, 0, batchSize)

	for key, entry := range c.data {
		if len(items) >= batchSize {
			break
		}

		// Advance ring buffer to current hour before checking samples
		c.advanceRingBuffer(entry, currentHour)

		// Check if LastBackendSync is old enough
		if entry.LastBackendSync >= lastBackendSyncOlderThan {
			continue
		}

		// Calculate total samples
		totalSamples := c.sumSamples(entry)
		if totalSamples == 0 {
			continue // Skip inactive entries
		}

		items = append(items, KnownActiveTimeseriesItem{
			HashKey:      key,
			TotalSamples: totalSamples,
			LastUpdate:   entry.LastUpdate * 1000, // convert seconds to milliseconds
		})
	}

	return items
}

// GetInactiveTimeseriesBatch returns up to batchSize HashKeys of timeseries
// that are inactive (all hourly samples are 0).
func (c *KnownTimeseriesCache) GetInactiveTimeseriesBatch(batchSize int) []HashKey {
	c.mu.Lock()
	defer c.mu.Unlock()

	if batchSize <= 0 {
		return nil
	}

	now := c.timeFunc()
	currentHour := now.Unix() / 3600

	keys := make([]HashKey, 0, batchSize)

	for key, entry := range c.data {
		if len(keys) >= batchSize {
			break
		}

		// Advance ring buffer to current hour before checking samples
		c.advanceRingBuffer(entry, currentHour)

		// Check if all samples are zero
		if c.sumSamples(entry) == 0 {
			keys = append(keys, key)
		}
	}

	return keys
}

// DeleteBatch removes the specified hash keys from the cache.
func (c *KnownTimeseriesCache) DeleteBatch(keys []HashKey) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, key := range keys {
		delete(c.data, key)
	}
}

// MarkSynced updates the LastBackendSync timestamp for a specific key.
func (c *KnownTimeseriesCache) MarkSynced(key HashKey) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, exists := c.data[key]; exists {
		entry.LastBackendSync = c.timeFunc().Unix()
	}
}

// MarkSyncedBatch updates the LastBackendSync timestamp for multiple keys.
func (c *KnownTimeseriesCache) MarkSyncedBatch(keys []HashKey) {
	c.mu.Lock()
	defer c.mu.Unlock()

	nowUnix := c.timeFunc().Unix()
	for _, key := range keys {
		if entry, exists := c.data[key]; exists {
			entry.LastBackendSync = nowUnix
		}
	}
}

// sumSamples returns the sum of all samples in the ring buffer.
// Must be called with lock held.
func (c *KnownTimeseriesCache) sumSamples(entry *KnownTimeseriesEntry) int64 {
	var total int64
	for _, s := range entry.samples {
		total += s
	}
	return total
}

// setTimeFunc sets a custom time function for testing purposes.
func (c *KnownTimeseriesCache) setTimeFunc(fn func() time.Time) {
	c.timeFunc = fn
}
