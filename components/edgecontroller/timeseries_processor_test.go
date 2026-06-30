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

// leansignaledgecontroller/timeseries_processor_test.go
package leansignaledgecontroller

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"nhooyr.io/websocket/wsjson"
)

// startClientReader starts a goroutine that reads acks from the client connection
// and resolves pending commands. This mimics what readLoop does in production.
func startClientReader(ctx context.Context, ext *edgeControllerExtension) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			ext.mu.Lock()
			conn := ext.conn
			ext.mu.Unlock()

			if conn == nil {
				return
			}

			var msg Message
			if err := wsjson.Read(ctx, conn, &msg); err != nil {
				return
			}

			// Process ack messages
			if msg.Type == MessageTypeAck && msg.ID != "" {
				var ack AckPayload
				if payloadBytes, err := json.Marshal(msg.Payload); err == nil {
					json.Unmarshal(payloadBytes, &ack)
				}
				ext.resolvePending(msg.ID, ack)
			}
		}
	}()
}

func TestSendDiscoveredTimeseriesBatch_Success(t *testing.T) {
	ext := newTestExtension()
	serverConn := connectToMockBackend(t, ext)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Start client reader to process acks
	startClientReader(ctx, ext)

	// Create test items
	items := []DiscoveredTimeseriesItem{
		{HashKey: "abc123", Name: "test_metric_1", Labels: []string{"env", "prod"}},
		{HashKey: "def456", Name: "test_metric_2", Labels: []string{"region", "us-east"}},
	}

	// Start goroutine to simulate backend reading and sending ack
	go func() {
		var msg Message
		if err := wsjson.Read(ctx, serverConn, &msg); err != nil {
			return
		}

		// Verify it's a command with metrics_index_create
		if msg.Type != MessageTypeCommand {
			t.Errorf("expected command type, got %s", msg.Type)
		}

		payloadBytes, _ := json.Marshal(msg.Payload)
		var cmd CommandPayload
		json.Unmarshal(payloadBytes, &cmd)

		if cmd.Command != CmdMetricsIndexCreate {
			t.Errorf("expected command %s, got %s", CmdMetricsIndexCreate, cmd.Command)
		}

		// Send success ack
		ack := Message{
			Type:      MessageTypeAck,
			ID:        msg.ID,
			Timestamp: time.Now().UTC(),
			Payload: AckPayload{
				Status:  "success",
				Message: "created",
			},
		}
		wsjson.Write(ctx, serverConn, ack)
	}()

	// Send the batch
	err := ext.sendDiscoveredTimeseriesBatch(ctx, items)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestSendActiveTimeseriesBatch_Success(t *testing.T) {
	ext := newTestExtension()
	serverConn := connectToMockBackend(t, ext)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Start client reader to process acks
	startClientReader(ctx, ext)

	// Add some active timeseries items
	items := []KnownActiveTimeseriesItem{
		{HashKey: HashKey{0x01}, TotalSamples: 100, LastUpdate: 1700000000000},
		{HashKey: HashKey{0x02}, TotalSamples: 200, LastUpdate: 1700000000000},
	}

	// Start goroutine to simulate backend reading and sending ack
	go func() {
		var msg Message
		if err := wsjson.Read(ctx, serverConn, &msg); err != nil {
			return
		}

		// Verify it's a command with metrics_index_update
		if msg.Type != MessageTypeCommand {
			t.Errorf("expected command type, got %s", msg.Type)
		}

		payloadBytes, _ := json.Marshal(msg.Payload)
		var cmd CommandPayload
		json.Unmarshal(payloadBytes, &cmd)

		if cmd.Command != CmdMetricsIndexUpdate {
			t.Errorf("expected command %s, got %s", CmdMetricsIndexUpdate, cmd.Command)
		}

		// Send success ack
		ack := Message{
			Type:      MessageTypeAck,
			ID:        msg.ID,
			Timestamp: time.Now().UTC(),
			Payload: AckPayload{
				Status:  "success",
				Message: "updated",
			},
		}
		wsjson.Write(ctx, serverConn, ack)
	}()

	// Send the batch
	err := ext.sendActiveTimeseriesBatch(ctx, items)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestSendInactiveTimeseriesBatch_Success(t *testing.T) {
	ext := newTestExtension()
	serverConn := connectToMockBackend(t, ext)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Start client reader to process acks
	startClientReader(ctx, ext)

	// Create inactive keys
	keys := []HashKey{{0x01}, {0x02}, {0x03}}

	// Start goroutine to simulate backend reading and sending ack
	go func() {
		var msg Message
		if err := wsjson.Read(ctx, serverConn, &msg); err != nil {
			return
		}

		// Verify it's a command with metrics_index_delete
		if msg.Type != MessageTypeCommand {
			t.Errorf("expected command type, got %s", msg.Type)
		}

		payloadBytes, _ := json.Marshal(msg.Payload)
		var cmd CommandPayload
		json.Unmarshal(payloadBytes, &cmd)

		if cmd.Command != CmdMetricsIndexDelete {
			t.Errorf("expected command %s, got %s", CmdMetricsIndexDelete, cmd.Command)
		}

		// Send success ack
		ack := Message{
			Type:      MessageTypeAck,
			ID:        msg.ID,
			Timestamp: time.Now().UTC(),
			Payload: AckPayload{
				Status:  "success",
				Message: "deleted",
			},
		}
		wsjson.Write(ctx, serverConn, ack)
	}()

	// Send the batch
	err := ext.sendInactiveTimeseriesBatch(ctx, keys)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestProcessOneDiscoveredBatch_EmptyCache(t *testing.T) {
	ext := newTestExtension()
	_ = connectToMockBackend(t, ext)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Cache is empty - should return false
	processed := ext.processOneDiscoveredBatch(ctx)
	if processed {
		t.Error("expected false for empty cache")
	}
}

func TestProcessOneDiscoveredBatch_WithData(t *testing.T) {
	ext := newTestExtension()
	serverConn := connectToMockBackend(t, ext)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Start client reader to process acks
	startClientReader(ctx, ext)

	// Add some items to the discovered cache
	ext.discoveredTimeseriesCache.Add(HashKey{0x01}, &TimeseriesEntry{
		MetricName: "test_metric_1",
		Labels:     []LabelPair{{Name: "env", Value: "prod"}},
	})

	// Start goroutine to handle backend messages
	go func() {
		var msg Message
		if err := wsjson.Read(ctx, serverConn, &msg); err != nil {
			return
		}

		ack := Message{
			Type:      MessageTypeAck,
			ID:        msg.ID,
			Timestamp: time.Now().UTC(),
			Payload: AckPayload{
				Status:  "success",
				Message: "ok",
			},
		}
		wsjson.Write(ctx, serverConn, ack)
	}()

	// Should return true and process the batch
	processed := ext.processOneDiscoveredBatch(ctx)
	if !processed {
		t.Error("expected true when cache has data")
	}

	// Cache should be empty now
	if ext.discoveredTimeseriesCache.GetSize() != 0 {
		t.Errorf("expected cache size 0 after processing, got %d", ext.discoveredTimeseriesCache.GetSize())
	}
}

func TestProcessOneKnownBatch_EmptyCache(t *testing.T) {
	ext := newTestExtension()
	_ = connectToMockBackend(t, ext)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Cache is empty - should return false
	processed := ext.processOneKnownBatch(ctx)
	if processed {
		t.Error("expected false for empty cache")
	}
}

func TestTimeseriesProcessorLoop_ContextCancellation(t *testing.T) {
	ext := newTestExtension()
	_ = connectToMockBackend(t, ext)

	ctx, cancel := context.WithCancel(context.Background())

	// Start the processor
	ext.startTimeseriesProcessor(ctx)

	// Give it a moment to start
	time.Sleep(50 * time.Millisecond)

	// Cancel context
	cancel()

	// Wait for goroutine to finish
	done := make(chan struct{})
	go func() {
		ext.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success - processor stopped
	case <-time.After(1 * time.Second):
		t.Error("timeseriesProcessorLoop did not stop on context cancellation")
	}
}

func TestTimeseriesProcessor_DiscoveredPriority(t *testing.T) {
	ext := newTestExtension()
	serverConn := connectToMockBackend(t, ext)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start client reader to process acks
	startClientReader(ctx, ext)

	// Add items to both caches
	ext.discoveredTimeseriesCache.Add(HashKey{0x01}, &TimeseriesEntry{
		MetricName: "discovered_metric",
	})
	ext.knownTimeseriesCache.UpdateTimeseries(HashKey{0x02}, &TimeseriesEntry{
		MetricName: "known_metric",
		Samples:    100,
	})

	// Start goroutine to handle backend messages and track order
	receivedCommands := make(chan string, 10)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			var msg Message
			if err := wsjson.Read(ctx, serverConn, &msg); err != nil {
				return
			}

			if msg.Type == MessageTypeCommand {
				payloadBytes, _ := json.Marshal(msg.Payload)
				var cmd CommandPayload
				json.Unmarshal(payloadBytes, &cmd)
				receivedCommands <- cmd.Command

				ack := Message{
					Type:      MessageTypeAck,
					ID:        msg.ID,
					Timestamp: time.Now().UTC(),
					Payload: AckPayload{
						Status:  "success",
						Message: "ok",
					},
				}
				wsjson.Write(ctx, serverConn, ack)
			}
		}
	}()

	// Process one discovered batch - should process discovered first
	processed := ext.processOneDiscoveredBatch(ctx)
	if !processed {
		t.Error("expected discovered batch to be processed")
	}

	// Wait for command
	select {
	case cmd := <-receivedCommands:
		if cmd != CmdMetricsIndexCreate {
			t.Errorf("expected %s first, got %s", CmdMetricsIndexCreate, cmd)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for command")
	}

	// Discovered cache should be empty now
	if ext.discoveredTimeseriesCache.GetSize() != 0 {
		t.Errorf("expected discovered cache empty, got %d", ext.discoveredTimeseriesCache.GetSize())
	}
}

func TestConstants(t *testing.T) {
	// Verify constants have expected values
	if DiscoveredBatchSize != 5000 {
		t.Errorf("expected DiscoveredBatchSize=5000, got %d", DiscoveredBatchSize)
	}
	if KnownBatchSize != 30000 {
		t.Errorf("expected KnownBatchSize=30000, got %d", KnownBatchSize)
	}
	if PushBatchIntervalSeconds != 5 {
		t.Errorf("expected PushBatchIntervalSeconds=5, got %d", PushBatchIntervalSeconds)
	}
	if WaitOnErrorIntervalSeconds != 30 {
		t.Errorf("expected WaitOnErrorIntervalSeconds=30, got %d", WaitOnErrorIntervalSeconds)
	}
	if LastBackendSyncThresholdSeconds != 60 {
		t.Errorf("expected LastBackendSyncThresholdSeconds=60, got %d", LastBackendSyncThresholdSeconds)
	}
}
