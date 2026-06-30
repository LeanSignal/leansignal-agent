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

// leansignaledgecontroller/discovered_timeseries_cache_test.go
package leansignaledgecontroller

import (
	"sync"
	"testing"

	"go.uber.org/zap"
)

func TestNewDiscoveredTimeseriesCache(t *testing.T) {
	logger := zap.NewNop()
	cache := NewDiscoveredTimeseriesCache(logger)

	if cache == nil {
		t.Fatal("expected non-nil cache")
	}
	if cache.GetSize() != 0 {
		t.Errorf("expected empty cache, got size %d", cache.GetSize())
	}
}

func TestDiscoveredTimeseriesCache_Init(t *testing.T) {
	logger := zap.NewNop()
	cache := NewDiscoveredTimeseriesCache(logger)

	// Add some data
	key := HashKey{0x01, 0x02, 0x03}
	cache.Add(key, &TimeseriesEntry{MetricName: "test"})

	if cache.GetSize() != 1 {
		t.Errorf("expected size 1, got %d", cache.GetSize())
	}

	// Init should clear
	cache.Init()
	if cache.GetSize() != 0 {
		t.Errorf("expected size 0 after Init, got %d", cache.GetSize())
	}
}

func TestDiscoveredTimeseriesCache_Clear(t *testing.T) {
	logger := zap.NewNop()
	cache := NewDiscoveredTimeseriesCache(logger)

	// Add some data
	key := HashKey{0x01, 0x02, 0x03}
	cache.Add(key, &TimeseriesEntry{MetricName: "test"})

	if cache.GetSize() != 1 {
		t.Errorf("expected size 1, got %d", cache.GetSize())
	}

	// Clear should remove all
	cache.Clear()
	if cache.GetSize() != 0 {
		t.Errorf("expected size 0 after Clear, got %d", cache.GetSize())
	}
}

func TestDiscoveredTimeseriesCache_Add(t *testing.T) {
	logger := zap.NewNop()
	cache := NewDiscoveredTimeseriesCache(logger)

	key1 := HashKey{0x01}
	key2 := HashKey{0x02}

	cache.Add(key1, &TimeseriesEntry{MetricName: "metric1"})
	if cache.GetSize() != 1 {
		t.Errorf("expected size 1, got %d", cache.GetSize())
	}

	cache.Add(key2, &TimeseriesEntry{MetricName: "metric2"})
	if cache.GetSize() != 2 {
		t.Errorf("expected size 2, got %d", cache.GetSize())
	}

	// Adding same key should overwrite
	cache.Add(key1, &TimeseriesEntry{MetricName: "metric1_updated"})
	if cache.GetSize() != 2 {
		t.Errorf("expected size 2 after overwrite, got %d", cache.GetSize())
	}
}

func TestDiscoveredTimeseriesCache_AddBatch(t *testing.T) {
	logger := zap.NewNop()
	cache := NewDiscoveredTimeseriesCache(logger)

	batch := &TimeseriesBatch{
		Data: map[HashKey]*TimeseriesEntry{
			{0x01}: {MetricName: "metric1"},
			{0x02}: {MetricName: "metric2"},
			{0x03}: {MetricName: "metric3"},
		},
	}

	cache.AddBatch(batch)
	if cache.GetSize() != 3 {
		t.Errorf("expected size 3, got %d", cache.GetSize())
	}
}

func TestDiscoveredTimeseriesCache_GetBatch_EmptyCache(t *testing.T) {
	logger := zap.NewNop()
	cache := NewDiscoveredTimeseriesCache(logger)

	items, keys := cache.GetBatch(10)

	if items != nil {
		t.Errorf("expected nil items, got %v", items)
	}
	if keys != nil {
		t.Errorf("expected nil keys, got %v", keys)
	}
}

func TestDiscoveredTimeseriesCache_GetBatch_LessThanBatchSize(t *testing.T) {
	logger := zap.NewNop()
	cache := NewDiscoveredTimeseriesCache(logger)

	// Add 2 entries
	cache.Add(HashKey{0x01}, &TimeseriesEntry{
		MetricName: "metric1",
		Labels:     []LabelPair{{Name: "env", Value: "prod"}},
	})
	cache.Add(HashKey{0x02}, &TimeseriesEntry{
		MetricName: "metric2",
		Labels:     []LabelPair{{Name: "region", Value: "us-east"}},
	})

	// Request batch of 10, should get 2
	items, keys := cache.GetBatch(10)

	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d", len(items))
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(keys))
	}
}

func TestDiscoveredTimeseriesCache_GetBatch_MoreThanBatchSize(t *testing.T) {
	logger := zap.NewNop()
	cache := NewDiscoveredTimeseriesCache(logger)

	// Add 5 entries
	for i := byte(0); i < 5; i++ {
		cache.Add(HashKey{i}, &TimeseriesEntry{
			MetricName: "metric",
		})
	}

	// Request batch of 3, should get exactly 3
	items, keys := cache.GetBatch(3)

	if len(items) != 3 {
		t.Errorf("expected 3 items, got %d", len(items))
	}
	if len(keys) != 3 {
		t.Errorf("expected 3 keys, got %d", len(keys))
	}
}

func TestDiscoveredTimeseriesCache_GetBatch_LabelsFlattened(t *testing.T) {
	logger := zap.NewNop()
	cache := NewDiscoveredTimeseriesCache(logger)

	key := HashKey{0xaa, 0xbb}
	cache.Add(key, &TimeseriesEntry{
		MetricName: "http_requests",
		Labels: []LabelPair{
			{Name: "method", Value: "GET"},
			{Name: "path", Value: "/api"},
		},
	})

	items, keys := cache.GetBatch(10)

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	item := items[0]
	if item.Name != "http_requests" {
		t.Errorf("expected name 'http_requests', got '%s'", item.Name)
	}
	if item.HashKey != key.String() {
		t.Errorf("expected hashKey '%s', got '%s'", key.String(), item.HashKey)
	}

	expectedLabels := []string{"method", "GET", "path", "/api"}
	if len(item.Labels) != len(expectedLabels) {
		t.Fatalf("expected %d labels, got %d", len(expectedLabels), len(item.Labels))
	}
	for i, lbl := range expectedLabels {
		if item.Labels[i] != lbl {
			t.Errorf("expected label[%d]='%s', got '%s'", i, lbl, item.Labels[i])
		}
	}

	if len(keys) != 1 || keys[0] != key {
		t.Errorf("expected key %v, got %v", key, keys)
	}
}

func TestDiscoveredTimeseriesCache_PurgeBatch(t *testing.T) {
	logger := zap.NewNop()
	cache := NewDiscoveredTimeseriesCache(logger)

	key1 := HashKey{0x01}
	key2 := HashKey{0x02}
	key3 := HashKey{0x03}

	cache.Add(key1, &TimeseriesEntry{MetricName: "metric1"})
	cache.Add(key2, &TimeseriesEntry{MetricName: "metric2"})
	cache.Add(key3, &TimeseriesEntry{MetricName: "metric3"})

	if cache.GetSize() != 3 {
		t.Fatalf("expected size 3, got %d", cache.GetSize())
	}

	// Purge 2 keys
	cache.PurgeBatch([]HashKey{key1, key3})

	if cache.GetSize() != 1 {
		t.Errorf("expected size 1 after purge, got %d", cache.GetSize())
	}
}

func TestDiscoveredTimeseriesCache_PurgeBatch_NonExistentKey(t *testing.T) {
	logger := zap.NewNop()
	cache := NewDiscoveredTimeseriesCache(logger)

	key1 := HashKey{0x01}
	cache.Add(key1, &TimeseriesEntry{MetricName: "metric1"})

	// Purge non-existent key should not error
	cache.PurgeBatch([]HashKey{{0xFF}})

	if cache.GetSize() != 1 {
		t.Errorf("expected size 1, got %d", cache.GetSize())
	}
}

func TestDiscoveredTimeseriesCache_GetBatchAndPurge_Workflow(t *testing.T) {
	logger := zap.NewNop()
	cache := NewDiscoveredTimeseriesCache(logger)

	// Add 5 entries
	for i := byte(0); i < 5; i++ {
		cache.Add(HashKey{i}, &TimeseriesEntry{
			MetricName: "metric",
		})
	}

	// Get batch of 3
	_, keys := cache.GetBatch(3)
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}

	// Purge the batch
	cache.PurgeBatch(keys)

	// Should have 2 remaining
	if cache.GetSize() != 2 {
		t.Errorf("expected 2 remaining, got %d", cache.GetSize())
	}

	// Get remaining
	_, keys = cache.GetBatch(10)
	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(keys))
	}

	// Purge remaining
	cache.PurgeBatch(keys)
	if cache.GetSize() != 0 {
		t.Errorf("expected 0 remaining, got %d", cache.GetSize())
	}
}

func TestDiscoveredTimeseriesCache_ThreadSafety(t *testing.T) {
	logger := zap.NewNop()
	cache := NewDiscoveredTimeseriesCache(logger)

	var wg sync.WaitGroup
	numGoroutines := 100

	// Concurrent adds
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			key := HashKey{byte(idx)}
			cache.Add(key, &TimeseriesEntry{MetricName: "metric"})
		}(i)
	}
	wg.Wait()

	if cache.GetSize() != numGoroutines {
		t.Errorf("expected size %d, got %d", numGoroutines, cache.GetSize())
	}

	// Concurrent reads and writes
	wg.Add(numGoroutines * 3)
	for i := 0; i < numGoroutines; i++ {
		// Reader
		go func() {
			defer wg.Done()
			cache.GetSize()
		}()
		// GetBatch reader
		go func() {
			defer wg.Done()
			cache.GetBatch(10)
		}()
		// Writer
		go func(idx int) {
			defer wg.Done()
			key := HashKey{byte(idx + 128)}
			cache.Add(key, &TimeseriesEntry{MetricName: "metric"})
		}(i)
	}
	wg.Wait()

	// Should not panic - that's the main test for thread safety
}
