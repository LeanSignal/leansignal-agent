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

// leansignaledgecontroller/timeseries_processor.go
package leansignaledgecontroller

import (
	"context"
	"encoding/json"
	"time"

	"go.uber.org/zap"
)

// Constants for the timeseries processor
const (
	DiscoveredBatchSize             = 5000
	KnownBatchSize                  = 30000
	PushBatchIntervalSeconds        = 5
	WaitOnErrorIntervalSeconds      = 30
	LastBackendSyncThresholdSeconds = 60 // 1 minute
)

// startTimeseriesProcessor starts a goroutine that processes
// timeseries batches and sends them to the backend.
// Discovered timeseries have priority over known timeseries.
func (e *edgeControllerExtension) startTimeseriesProcessor(ctx context.Context) {
	e.wg.Add(1)
	go e.timeseriesProcessorLoop(ctx)
}

// timeseriesProcessorLoop is the main loop for processing timeseries.
// Priority: discovered > known
// Each iteration: process one batch (discovered has priority), then wait.
func (e *edgeControllerExtension) timeseriesProcessorLoop(ctx context.Context) {
	defer e.wg.Done()

	e.logger.Info("Starting timeseries processor")

	for {
		select {
		case <-ctx.Done():
			e.logger.Info("Timeseries processor stopped")
			return
		default:
		}

		// First priority: process discovered timeseries
		if !e.processOneDiscoveredBatch(ctx) {
			// No discovered items - process known batch
			e.processOneKnownBatch(ctx)
		}

		// Single wait before next iteration
		select {
		case <-ctx.Done():
			return
		case <-time.After(PushBatchIntervalSeconds * time.Second):
		}
	}
}

// processOneDiscoveredBatch processes a single batch from the discovered cache.
// Returns true if a batch was processed (or attempted), false if cache was empty.
func (e *edgeControllerExtension) processOneDiscoveredBatch(ctx context.Context) bool {
	// Get a batch from the discovered timeseries cache
	items, keys := e.discoveredTimeseriesCache.GetBatch(DiscoveredBatchSize)

	// If no items, nothing to process
	if len(items) == 0 {
		return false
	}

	e.logger.Info("Processing discovered timeseries batch",
		zap.Int("batch_size", len(items)),
		zap.Int("remaining_cache_size", e.discoveredTimeseriesCache.GetSize()),
	)

	// Send the batch to the backend
	err := e.sendDiscoveredTimeseriesBatch(ctx, items)
	if err != nil {
		e.logger.Error("Failed to send discovered timeseries batch",
			zap.Error(err),
			zap.Int("batch_size", len(items)),
		)
		// Sleep on error before retrying
		select {
		case <-ctx.Done():
		case <-time.After(WaitOnErrorIntervalSeconds * time.Second):
		}
		// Return true because we attempted to process (will retry)
		return true
	}

	// Backend responded OK - purge the processed keys from the cache
	e.discoveredTimeseriesCache.PurgeBatch(keys)
	e.logger.Info("Purged discovered timeseries batch from cache",
		zap.Int("purged_count", len(keys)),
		zap.Int("remaining_cache_size", e.discoveredTimeseriesCache.GetSize()),
	)

	return true
}

// processOneKnownBatch processes a single batch from the known cache.
// Returns true if a batch was processed (or attempted), false if cache was empty.
func (e *edgeControllerExtension) processOneKnownBatch(ctx context.Context) bool {
	// Calculate lastBackendSyncOlderThan = now - 1 minute
	lastBackendSyncOlderThan := time.Now().Unix() - LastBackendSyncThresholdSeconds

	// Get active timeseries batch
	activeItems := e.knownTimeseriesCache.GetActiveTimeseriesBatch(lastBackendSyncOlderThan, KnownBatchSize)

	// Get inactive timeseries batch
	inactiveKeys := e.knownTimeseriesCache.GetInactiveTimeseriesBatch(KnownBatchSize)

	// If no items in either batch, nothing to process
	if len(activeItems) == 0 && len(inactiveKeys) == 0 {
		return false
	}

	// Process inactive timeseries first (higher priority)
	if len(inactiveKeys) > 0 {
		e.logger.Info("Processing inactive known timeseries batch",
			zap.Int("batch_size", len(inactiveKeys)),
			zap.Int("known_cache_size", e.knownTimeseriesCache.GetSize()),
		)

		err := e.sendInactiveTimeseriesBatch(ctx, inactiveKeys)
		if err != nil {
			e.logger.Error("Failed to send inactive timeseries batch",
				zap.Error(err),
				zap.Int("batch_size", len(inactiveKeys)),
			)
			// Sleep on error before retrying
			select {
			case <-ctx.Done():
			case <-time.After(WaitOnErrorIntervalSeconds * time.Second):
			}
			return true
		}

		// Backend responded OK - delete the inactive keys from cache
		e.knownTimeseriesCache.DeleteBatch(inactiveKeys)
		e.logger.Info("Deleted inactive timeseries batch from cache",
			zap.Int("deleted_count", len(inactiveKeys)),
			zap.Int("remaining_cache_size", e.knownTimeseriesCache.GetSize()),
		)

		// Sleep between inactive and active batch processing
		if len(activeItems) > 0 {
			select {
			case <-ctx.Done():
				return true
			case <-time.After(PushBatchIntervalSeconds * time.Second):
			}
		}
	}

	// Process active timeseries
	if len(activeItems) > 0 {
		e.logger.Info("Processing active known timeseries batch",
			zap.Int("batch_size", len(activeItems)),
			zap.Int("known_cache_size", e.knownTimeseriesCache.GetSize()),
		)

		err := e.sendActiveTimeseriesBatch(ctx, activeItems)
		if err != nil {
			e.logger.Error("Failed to send active timeseries batch",
				zap.Error(err),
				zap.Int("batch_size", len(activeItems)),
			)
			// Sleep on error before retrying
			select {
			case <-ctx.Done():
			case <-time.After(WaitOnErrorIntervalSeconds * time.Second):
			}
			return true
		}

		// Backend responded OK - mark the keys as synced
		keys := make([]HashKey, len(activeItems))
		for i, item := range activeItems {
			keys[i] = item.HashKey
		}
		e.knownTimeseriesCache.MarkSyncedBatch(keys)
		e.logger.Info("Marked active timeseries batch as synced",
			zap.Int("synced_count", len(keys)),
		)
	}

	return true
}

// sendDiscoveredTimeseriesBatch sends a batch of discovered timeseries to the backend.
func (e *edgeControllerExtension) sendDiscoveredTimeseriesBatch(ctx context.Context, items []DiscoveredTimeseriesItem) error {
	// Marshal the batch items to JSON
	data, err := json.Marshal(items)
	if err != nil {
		return err
	}

	// Send the command to the backend
	ack, err := e.SendCommand(ctx, CmdMetricsIndexCreate, data)
	if err != nil {
		return err
	}

	// Check the acknowledgment status
	if ack.Status != "success" {
		e.logger.Warn("Backend returned non-success status for metrics_index_create",
			zap.String("status", ack.Status),
			zap.String("message", ack.Message),
		)
	}

	return nil
}

// sendActiveTimeseriesBatch sends a batch of active timeseries to the backend.
func (e *edgeControllerExtension) sendActiveTimeseriesBatch(ctx context.Context, items []KnownActiveTimeseriesItem) error {
	// Marshal the batch items to JSON
	data, err := json.Marshal(items)
	if err != nil {
		return err
	}

	// Send the command to the backend
	ack, err := e.SendCommand(ctx, CmdMetricsIndexUpdate, data)
	if err != nil {
		return err
	}

	// Check the acknowledgment status
	if ack.Status != "success" {
		e.logger.Warn("Backend returned non-success status for metrics_index_update",
			zap.String("status", ack.Status),
			zap.String("message", ack.Message),
		)
	}

	return nil
}

// sendInactiveTimeseriesBatch sends a batch of inactive timeseries keys to the backend for deletion.
func (e *edgeControllerExtension) sendInactiveTimeseriesBatch(ctx context.Context, keys []HashKey) error {
	// Convert HashKey to string for JSON serialization
	keyStrings := make([]string, len(keys))
	for i, key := range keys {
		keyStrings[i] = key.String()
	}

	// Marshal the keys to JSON
	data, err := json.Marshal(keyStrings)
	if err != nil {
		return err
	}

	// Send the command to the backend
	ack, err := e.SendCommand(ctx, CmdMetricsIndexDelete, data)
	if err != nil {
		return err
	}

	// Check the acknowledgment status
	if ack.Status != "success" {
		e.logger.Warn("Backend returned non-success status for metrics_index_delete",
			zap.String("status", ack.Status),
			zap.String("message", ack.Message),
		)
	}

	return nil
}
