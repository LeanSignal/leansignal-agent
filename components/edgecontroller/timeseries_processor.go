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
	"encoding/hex"
	"time"

	agentv1 "github.com/leansignal/leansignal-agent/proto/gen/leansignal/agent/v1"
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

// logNonSuccess logs a non-success ack for an index operation.
func (e *edgeControllerExtension) logNonSuccess(op string, ack *agentv1.Ack) {
	if ack != nil && !ack.GetSuccess() {
		e.logger.Warn("Backend returned non-success status",
			zap.String("op", op),
			zap.String("message", ack.GetMessage()),
		)
	}
}

// sendDiscoveredTimeseriesBatch sends a batch of discovered timeseries (index create).
func (e *edgeControllerExtension) sendDiscoveredTimeseriesBatch(ctx context.Context, items []DiscoveredTimeseriesItem) error {
	series := make([]*agentv1.DiscoveredSeries, 0, len(items))
	for _, it := range items {
		fp, err := hex.DecodeString(it.HashKey)
		if err != nil {
			e.logger.Warn("skipping series with invalid fingerprint", zap.String("hashKey", it.HashKey))
			continue
		}
		labels := make([]*agentv1.Label, 0, len(it.Labels)/2)
		for i := 0; i+1 < len(it.Labels); i += 2 {
			labels = append(labels, &agentv1.Label{Name: it.Labels[i], Value: it.Labels[i+1]})
		}
		series = append(series, &agentv1.DiscoveredSeries{
			Fingerprint: fp,
			MetricName:  it.Name,
			Labels:      labels,
		})
	}

	ack, err := e.sendAndWaitAck(ctx, &agentv1.AgentMessage{
		Body: &agentv1.AgentMessage_IndexCreate{IndexCreate: &agentv1.IndexCreate{Series: series}},
	})
	if err != nil {
		return err
	}
	e.logNonSuccess("index_create", ack)
	return nil
}

// sendActiveTimeseriesBatch sends a batch of active timeseries (index update).
func (e *edgeControllerExtension) sendActiveTimeseriesBatch(ctx context.Context, items []KnownActiveTimeseriesItem) error {
	series := make([]*agentv1.ActiveSeries, 0, len(items))
	for _, it := range items {
		fp := make([]byte, len(it.HashKey))
		copy(fp, it.HashKey[:])
		series = append(series, &agentv1.ActiveSeries{
			Fingerprint:  fp,
			Samples:      it.TotalSamples,
			LastUpdateMs: it.LastUpdate,
		})
	}

	ack, err := e.sendAndWaitAck(ctx, &agentv1.AgentMessage{
		Body: &agentv1.AgentMessage_IndexUpdate{IndexUpdate: &agentv1.IndexUpdate{Series: series}},
	})
	if err != nil {
		return err
	}
	e.logNonSuccess("index_update", ack)
	return nil
}

// sendInactiveTimeseriesBatch sends a batch of inactive timeseries keys (index delete).
func (e *edgeControllerExtension) sendInactiveTimeseriesBatch(ctx context.Context, keys []HashKey) error {
	fingerprints := make([][]byte, 0, len(keys))
	for _, key := range keys {
		fp := make([]byte, len(key))
		copy(fp, key[:])
		fingerprints = append(fingerprints, fp)
	}

	ack, err := e.sendAndWaitAck(ctx, &agentv1.AgentMessage{
		Body: &agentv1.AgentMessage_IndexDelete{IndexDelete: &agentv1.IndexDelete{Fingerprints: fingerprints}},
	})
	if err != nil {
		return err
	}
	e.logNonSuccess("index_delete", ack)
	return nil
}
