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

// leansignaledgecontroller/demand_timeseries_cache.go
package leansignaledgecontroller

import (
	"sync"
	"time"

	"go.uber.org/zap"
)

// DemandTimeseriesCache holds the ordered list of demanded timeseries strings
// and the timestamp of the last update. All operations are thread-safe.
type DemandTimeseriesCache struct {
	logger     *zap.Logger
	mu         sync.RWMutex
	timeseries []string
	demandHash uint64           // content hash of the applied demand set (0 = none)
	LastUpdate int64            // unix timestamp (seconds) of the last update
	timeFunc   func() time.Time // injectable for testing
}

// DemandTimeseriesSnapshot is the value type returned by GetDemands.
type DemandTimeseriesSnapshot struct {
	Timeseries []string
	DemandHash uint64
	LastUpdate int64
}

// NewDemandTimeseriesCache creates a new DemandTimeseriesCache instance.
func NewDemandTimeseriesCache(logger *zap.Logger) *DemandTimeseriesCache {
	return &DemandTimeseriesCache{
		logger:   logger,
		timeFunc: time.Now,
	}
}

// Init initialises (or re-initialises) the cache, clearing any existing data.
func (c *DemandTimeseriesCache) Init() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timeseries = nil
	c.demandHash = 0
	c.LastUpdate = 0
}

// GetDemands returns a snapshot of the current ordered timeseries list and the
// timestamp (seconds) of the last update. The returned slice is a defensive copy
// so callers cannot mutate the internal state.
func (c *DemandTimeseriesCache) GetDemands() DemandTimeseriesSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cp := make([]string, len(c.timeseries))
	copy(cp, c.timeseries)

	return DemandTimeseriesSnapshot{
		Timeseries: cp,
		DemandHash: c.demandHash,
		LastUpdate: c.LastUpdate,
	}
}

// GetSize returns the number of demanded timeseries currently held.
func (c *DemandTimeseriesCache) GetSize() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.timeseries)
}

// setTimeFunc sets a custom time function for testing purposes.
func (c *DemandTimeseriesCache) setTimeFunc(fn func() time.Time) {
	c.timeFunc = fn
}

// UpdateDemands replaces the ordered timeseries list with the provided slice,
// stores the server's content hash of it, and records the current time as
// LastUpdate. The internal state stores a copy of the provided slice so
// subsequent mutations by the caller are safe.
//
// Note: the leansignal_demand_filter processor reads GetDemands() live on every
// scrape batch — there is no separate notification channel.  The new list will
// be active for the very next metrics batch that arrives after this call returns.
func (c *DemandTimeseriesCache) UpdateDemands(timeseries []string, hash uint64) {
	cp := make([]string, len(timeseries))
	copy(cp, timeseries)

	c.mu.Lock()
	defer c.mu.Unlock()

	c.timeseries = cp
	c.demandHash = hash
	c.LastUpdate = c.timeFunc().Unix()

	c.logger.Info("demand list updated",
		zap.Int("demanded_metrics_count", len(cp)),
		zap.Uint64("demand_hash", hash),
	)
}
