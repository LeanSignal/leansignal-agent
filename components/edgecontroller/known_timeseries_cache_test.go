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

// leansignaledgecontroller/known_timeseries_cache_test.go
package leansignaledgecontroller

import (
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestNewKnownTimeseriesCache(t *testing.T) {
	logger := zap.NewNop()
	cache := NewKnownTimeseriesCache(logger)

	if cache == nil {
		t.Fatal("expected non-nil cache")
	}
	if cache.GetSize() != 0 {
		t.Errorf("expected empty cache, got size %d", cache.GetSize())
	}
}

func TestKnownTimeseriesCache_Init(t *testing.T) {
	logger := zap.NewNop()
	cache := NewKnownTimeseriesCache(logger)

	// Add some data
	key := HashKey{0x01, 0x02, 0x03}
	cache.UpdateTimeseries(key, &TimeseriesEntry{MetricName: "test", Samples: 10})

	if cache.GetSize() != 1 {
		t.Errorf("expected size 1, got %d", cache.GetSize())
	}

	// Init should clear
	cache.Init()
	if cache.GetSize() != 0 {
		t.Errorf("expected size 0 after Init, got %d", cache.GetSize())
	}
}

func TestKnownTimeseriesCache_Clear(t *testing.T) {
	logger := zap.NewNop()
	cache := NewKnownTimeseriesCache(logger)

	// Add some data
	key := HashKey{0x01, 0x02, 0x03}
	cache.UpdateTimeseries(key, &TimeseriesEntry{MetricName: "test", Samples: 10})

	if cache.GetSize() != 1 {
		t.Errorf("expected size 1, got %d", cache.GetSize())
	}

	// Clear should remove all
	cache.Clear()
	if cache.GetSize() != 0 {
		t.Errorf("expected size 0 after Clear, got %d", cache.GetSize())
	}
}

func TestKnownTimeseriesCache_IsTimeseriesKnown(t *testing.T) {
	logger := zap.NewNop()
	cache := NewKnownTimeseriesCache(logger)

	key := HashKey{0x01}
	unknownKey := HashKey{0xFF}

	// Before adding
	if cache.IsTimeseriesKnown(key) {
		t.Error("expected key to be unknown before adding")
	}

	// Add the key
	cache.UpdateTimeseries(key, &TimeseriesEntry{MetricName: "test", Samples: 5})

	// After adding
	if !cache.IsTimeseriesKnown(key) {
		t.Error("expected key to be known after adding")
	}

	// Unknown key should still be unknown
	if cache.IsTimeseriesKnown(unknownKey) {
		t.Error("expected unknownKey to remain unknown")
	}
}

func TestKnownTimeseriesCache_UpdateTimeseries_NewEntry(t *testing.T) {
	logger := zap.NewNop()
	cache := NewKnownTimeseriesCache(logger)

	// Fix time for deterministic testing
	fixedTime := time.Unix(3600*100, 0) // Hour 100
	cache.setTimeFunc(func() time.Time { return fixedTime })

	key := HashKey{0x01}
	cache.UpdateTimeseries(key, &TimeseriesEntry{MetricName: "test", Samples: 42})

	if cache.GetSize() != 1 {
		t.Errorf("expected size 1, got %d", cache.GetSize())
	}

	if !cache.IsTimeseriesKnown(key) {
		t.Error("expected key to be known")
	}
}

func TestKnownTimeseriesCache_UpdateTimeseries_IncrementSamples(t *testing.T) {
	logger := zap.NewNop()
	cache := NewKnownTimeseriesCache(logger)

	// Fix time for deterministic testing
	fixedTime := time.Unix(3600*100, 0) // Hour 100
	cache.setTimeFunc(func() time.Time { return fixedTime })

	key := HashKey{0x01}

	// First update
	cache.UpdateTimeseries(key, &TimeseriesEntry{MetricName: "test", Samples: 10})

	// Second update in same hour - should accumulate
	cache.UpdateTimeseries(key, &TimeseriesEntry{MetricName: "test", Samples: 20})

	// Get the entry to verify samples (use future timestamp to include all)
	items := cache.GetActiveTimeseriesBatch(fixedTime.Unix()+1, 10)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	if items[0].TotalSamples != 30 {
		t.Errorf("expected total samples 30, got %d", items[0].TotalSamples)
	}
}

func TestKnownTimeseriesCache_RingBuffer_HourAdvance(t *testing.T) {
	logger := zap.NewNop()
	cache := NewKnownTimeseriesCache(logger)

	key := HashKey{0x01}

	// Hour 100: Add 10 samples
	cache.setTimeFunc(func() time.Time { return time.Unix(3600*100, 0) })
	cache.UpdateTimeseries(key, &TimeseriesEntry{Samples: 10})

	// Hour 101: Add 20 samples
	cache.setTimeFunc(func() time.Time { return time.Unix(3600*101, 0) })
	cache.UpdateTimeseries(key, &TimeseriesEntry{Samples: 20})

	// Hour 102: Add 30 samples
	cache.setTimeFunc(func() time.Time { return time.Unix(3600*102, 0) })
	cache.UpdateTimeseries(key, &TimeseriesEntry{Samples: 30})

	// Check total: should be 10 + 20 + 30 = 60 (use future timestamp to include all)
	items := cache.GetActiveTimeseriesBatch(3600*103, 10)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	if items[0].TotalSamples != 60 {
		t.Errorf("expected total samples 60, got %d", items[0].TotalSamples)
	}
}

func TestKnownTimeseriesCache_RingBuffer_SlidingWindow(t *testing.T) {
	logger := zap.NewNop()
	cache := NewKnownTimeseriesCache(logger)

	key := HashKey{0x01}

	// Hour 100: Add 100 samples
	cache.setTimeFunc(func() time.Time { return time.Unix(3600*100, 0) })
	cache.UpdateTimeseries(key, &TimeseriesEntry{Samples: 100})

	// Advance to hour 108 (8 hours later) - the sample from hour 100 should be cleared
	cache.setTimeFunc(func() time.Time { return time.Unix(3600*108, 0) })
	cache.UpdateTimeseries(key, &TimeseriesEntry{Samples: 50})

	// Only the hour 108 samples should remain (use future timestamp to include all)
	items := cache.GetActiveTimeseriesBatch(3600*109, 10)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	if items[0].TotalSamples != 50 {
		t.Errorf("expected total samples 50 (old samples expired), got %d", items[0].TotalSamples)
	}
}

func TestKnownTimeseriesCache_RingBuffer_AllHoursExpired(t *testing.T) {
	logger := zap.NewNop()
	cache := NewKnownTimeseriesCache(logger)

	key := HashKey{0x01}

	// Hour 100: Add samples
	cache.setTimeFunc(func() time.Time { return time.Unix(3600*100, 0) })
	cache.UpdateTimeseries(key, &TimeseriesEntry{Samples: 100})

	// Advance to hour 200 (100 hours later) - all samples should be cleared
	cache.setTimeFunc(func() time.Time { return time.Unix(3600*200, 0) })

	// Get inactive - entry should now be inactive
	inactiveKeys := cache.GetInactiveTimeseriesBatch(10)
	if len(inactiveKeys) != 1 {
		t.Errorf("expected 1 inactive key, got %d", len(inactiveKeys))
	}
}

func TestKnownTimeseriesCache_GetActiveTimeseriesBatch(t *testing.T) {
	logger := zap.NewNop()
	cache := NewKnownTimeseriesCache(logger)

	fixedTime := time.Unix(3600*100, 0)
	cache.setTimeFunc(func() time.Time { return fixedTime })

	// Add 5 entries with samples
	for i := byte(0); i < 5; i++ {
		key := HashKey{i}
		cache.UpdateTimeseries(key, &TimeseriesEntry{Samples: int(i+1) * 10})
	}

	// Request batch of 3 (use future timestamp to include all)
	items := cache.GetActiveTimeseriesBatch(fixedTime.Unix()+1, 3)
	if len(items) != 3 {
		t.Errorf("expected 3 items, got %d", len(items))
	}

	// Verify all returned items have samples
	for _, item := range items {
		if item.TotalSamples == 0 {
			t.Error("expected non-zero samples for active item")
		}
	}
}

func TestKnownTimeseriesCache_GetActiveTimeseriesBatch_LastBackendSyncFilter(t *testing.T) {
	logger := zap.NewNop()
	cache := NewKnownTimeseriesCache(logger)

	fixedTime := time.Unix(3600*100, 0)
	cache.setTimeFunc(func() time.Time { return fixedTime })

	key1 := HashKey{0x01}
	key2 := HashKey{0x02}

	cache.UpdateTimeseries(key1, &TimeseriesEntry{Samples: 10})
	cache.UpdateTimeseries(key2, &TimeseriesEntry{Samples: 20})

	// Mark key1 as synced
	cache.MarkSynced(key1)

	// Get batch with filter - only key2 should be returned (key1 was just synced)
	items := cache.GetActiveTimeseriesBatch(fixedTime.Unix(), 10)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	if items[0].HashKey != key2 {
		t.Error("expected key2 to be returned (key1 was synced)")
	}
}

func TestKnownTimeseriesCache_GetInactiveTimeseriesBatch(t *testing.T) {
	logger := zap.NewNop()
	cache := NewKnownTimeseriesCache(logger)

	key1 := HashKey{0x01}
	key2 := HashKey{0x02}

	// Hour 100: Add samples to both
	cache.setTimeFunc(func() time.Time { return time.Unix(3600*100, 0) })
	cache.UpdateTimeseries(key1, &TimeseriesEntry{Samples: 10})
	cache.UpdateTimeseries(key2, &TimeseriesEntry{Samples: 20})

	// Advance to hour 108 (8 hours) - all samples expire
	cache.setTimeFunc(func() time.Time { return time.Unix(3600*108, 0) })

	// Both should now be inactive
	inactiveKeys := cache.GetInactiveTimeseriesBatch(10)
	if len(inactiveKeys) != 2 {
		t.Errorf("expected 2 inactive keys, got %d", len(inactiveKeys))
	}
}

func TestKnownTimeseriesCache_GetInactiveTimeseriesBatch_BatchSize(t *testing.T) {
	logger := zap.NewNop()
	cache := NewKnownTimeseriesCache(logger)

	// Hour 100: Add 5 entries
	cache.setTimeFunc(func() time.Time { return time.Unix(3600*100, 0) })
	for i := byte(0); i < 5; i++ {
		cache.UpdateTimeseries(HashKey{i}, &TimeseriesEntry{Samples: 10})
	}

	// Advance to hour 108 - all expire
	cache.setTimeFunc(func() time.Time { return time.Unix(3600*108, 0) })

	// Request only 3
	inactiveKeys := cache.GetInactiveTimeseriesBatch(3)
	if len(inactiveKeys) != 3 {
		t.Errorf("expected 3 inactive keys (limited by batch size), got %d", len(inactiveKeys))
	}
}

func TestKnownTimeseriesCache_DeleteBatch(t *testing.T) {
	logger := zap.NewNop()
	cache := NewKnownTimeseriesCache(logger)

	key1 := HashKey{0x01}
	key2 := HashKey{0x02}
	key3 := HashKey{0x03}

	cache.UpdateTimeseries(key1, &TimeseriesEntry{Samples: 10})
	cache.UpdateTimeseries(key2, &TimeseriesEntry{Samples: 20})
	cache.UpdateTimeseries(key3, &TimeseriesEntry{Samples: 30})

	if cache.GetSize() != 3 {
		t.Fatalf("expected size 3, got %d", cache.GetSize())
	}

	// Delete 2 keys
	cache.DeleteBatch([]HashKey{key1, key3})

	if cache.GetSize() != 1 {
		t.Errorf("expected size 1 after delete, got %d", cache.GetSize())
	}

	if !cache.IsTimeseriesKnown(key2) {
		t.Error("expected key2 to still exist")
	}
	if cache.IsTimeseriesKnown(key1) {
		t.Error("expected key1 to be deleted")
	}
	if cache.IsTimeseriesKnown(key3) {
		t.Error("expected key3 to be deleted")
	}
}

func TestKnownTimeseriesCache_MarkSynced(t *testing.T) {
	logger := zap.NewNop()
	cache := NewKnownTimeseriesCache(logger)

	fixedTime := time.Unix(3600*100, 0)
	cache.setTimeFunc(func() time.Time { return fixedTime })

	key := HashKey{0x01}
	cache.UpdateTimeseries(key, &TimeseriesEntry{Samples: 10})

	// Initially, LastBackendSync is 0, so it should be returned
	items := cache.GetActiveTimeseriesBatch(1, 10)
	if len(items) != 1 {
		t.Fatalf("expected 1 item before sync, got %d", len(items))
	}

	// Mark as synced
	cache.MarkSynced(key)

	// Now it should be filtered out (LastBackendSync >= threshold)
	items = cache.GetActiveTimeseriesBatch(fixedTime.Unix(), 10)
	if len(items) != 0 {
		t.Errorf("expected 0 items after sync, got %d", len(items))
	}
}

func TestKnownTimeseriesCache_MarkSyncedBatch(t *testing.T) {
	logger := zap.NewNop()
	cache := NewKnownTimeseriesCache(logger)

	fixedTime := time.Unix(3600*100, 0)
	cache.setTimeFunc(func() time.Time { return fixedTime })

	key1 := HashKey{0x01}
	key2 := HashKey{0x02}
	key3 := HashKey{0x03}

	cache.UpdateTimeseries(key1, &TimeseriesEntry{Samples: 10})
	cache.UpdateTimeseries(key2, &TimeseriesEntry{Samples: 20})
	cache.UpdateTimeseries(key3, &TimeseriesEntry{Samples: 30})

	// Initially all should be returned
	items := cache.GetActiveTimeseriesBatch(1, 10)
	if len(items) != 3 {
		t.Fatalf("expected 3 items before sync, got %d", len(items))
	}

	// Mark 2 as synced
	cache.MarkSyncedBatch([]HashKey{key1, key2})

	// Only key3 should be returned
	items = cache.GetActiveTimeseriesBatch(fixedTime.Unix(), 10)
	if len(items) != 1 {
		t.Errorf("expected 1 item after sync, got %d", len(items))
	}
}

func TestKnownTimeseriesCache_ThreadSafety(t *testing.T) {
	logger := zap.NewNop()
	cache := NewKnownTimeseriesCache(logger)

	var wg sync.WaitGroup
	numGoroutines := 100

	// Concurrent updates
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			key := HashKey{byte(idx)}
			cache.UpdateTimeseries(key, &TimeseriesEntry{Samples: 10})
		}(i)
	}
	wg.Wait()

	if cache.GetSize() != numGoroutines {
		t.Errorf("expected size %d, got %d", numGoroutines, cache.GetSize())
	}

	// Concurrent reads and writes
	wg.Add(numGoroutines * 4)
	for i := 0; i < numGoroutines; i++ {
		// Reader - IsTimeseriesKnown
		go func(idx int) {
			defer wg.Done()
			cache.IsTimeseriesKnown(HashKey{byte(idx)})
		}(i)
		// Reader - GetActiveTimeseriesBatch
		go func() {
			defer wg.Done()
			cache.GetActiveTimeseriesBatch(0, 10)
		}()
		// Reader - GetInactiveTimeseriesBatch
		go func() {
			defer wg.Done()
			cache.GetInactiveTimeseriesBatch(10)
		}()
		// Writer
		go func(idx int) {
			defer wg.Done()
			key := HashKey{byte(idx + 128)}
			cache.UpdateTimeseries(key, &TimeseriesEntry{Samples: 5})
		}(i)
	}
	wg.Wait()

	// Should not panic - that's the main test for thread safety
}

func TestKnownTimeseriesCache_RingBuffer_PartialExpiry(t *testing.T) {
	logger := zap.NewNop()
	cache := NewKnownTimeseriesCache(logger)

	key := HashKey{0x01}

	// Hour 100: Add 100 samples
	cache.setTimeFunc(func() time.Time { return time.Unix(3600*100, 0) })
	cache.UpdateTimeseries(key, &TimeseriesEntry{Samples: 100})

	// Hour 101: Add 50 samples
	cache.setTimeFunc(func() time.Time { return time.Unix(3600*101, 0) })
	cache.UpdateTimeseries(key, &TimeseriesEntry{Samples: 50})

	// Hour 102: Add 25 samples
	cache.setTimeFunc(func() time.Time { return time.Unix(3600*102, 0) })
	cache.UpdateTimeseries(key, &TimeseriesEntry{Samples: 25})

	// Total should be 175 (use future timestamp to include all)
	items := cache.GetActiveTimeseriesBatch(3600*200, 10)
	if items[0].TotalSamples != 175 {
		t.Errorf("expected 175, got %d", items[0].TotalSamples)
	}

	// Advance to hour 104 (4 hours from 100)
	// Hour 100 should still be in window (within 8 hours)
	cache.setTimeFunc(func() time.Time { return time.Unix(3600*104, 0) })
	items = cache.GetActiveTimeseriesBatch(3600*200, 10)
	if items[0].TotalSamples != 175 {
		t.Errorf("expected 175 (still within window), got %d", items[0].TotalSamples)
	}

	// Advance to hour 108 (8 hours from 100)
	// Hour 100's samples should now be expired
	cache.setTimeFunc(func() time.Time { return time.Unix(3600*108, 0) })
	items = cache.GetActiveTimeseriesBatch(3600*200, 10)
	if items[0].TotalSamples != 75 {
		t.Errorf("expected 75 (hour 100 expired), got %d", items[0].TotalSamples)
	}

	// Advance to hour 109
	// Hour 101's samples should now be expired
	cache.setTimeFunc(func() time.Time { return time.Unix(3600*109, 0) })
	items = cache.GetActiveTimeseriesBatch(3600*200, 10)
	if items[0].TotalSamples != 25 {
		t.Errorf("expected 25 (hours 100-101 expired), got %d", items[0].TotalSamples)
	}
}

func TestKnownTimeseriesCache_EmptyBatchRequests(t *testing.T) {
	logger := zap.NewNop()
	cache := NewKnownTimeseriesCache(logger)

	// Empty cache - should return nil/empty
	items := cache.GetActiveTimeseriesBatch(0, 10)
	if items == nil {
		// nil is acceptable for empty
	} else if len(items) != 0 {
		t.Errorf("expected empty items, got %d", len(items))
	}

	keys := cache.GetInactiveTimeseriesBatch(10)
	if keys == nil {
		// nil is acceptable for empty
	} else if len(keys) != 0 {
		t.Errorf("expected empty keys, got %d", len(keys))
	}

	// Zero batch size
	items = cache.GetActiveTimeseriesBatch(0, 0)
	if items != nil {
		t.Error("expected nil for zero batch size")
	}

	keys = cache.GetInactiveTimeseriesBatch(0)
	if keys != nil {
		t.Error("expected nil for zero batch size")
	}
}

func TestKnownCacheSnapshot(t *testing.T) {
	c := NewKnownTimeseriesCache(zap.NewNop())
	c.UpdateTimeseries(HashKey{2}, &TimeseriesEntry{MetricName: "b_metric", Samples: 3})
	c.UpdateTimeseries(HashKey{1}, &TimeseriesEntry{MetricName: "a_metric", Samples: 5})

	snap := c.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("Snapshot len = %d, want 2", len(snap))
	}
	// Sorted by metric name.
	if snap[0].MetricName != "a_metric" || snap[1].MetricName != "b_metric" {
		t.Errorf("Snapshot order = %q,%q, want a_metric,b_metric", snap[0].MetricName, snap[1].MetricName)
	}
	if snap[0].Samples != 5 {
		t.Errorf("a_metric samples = %d, want 5 (ring sum)", snap[0].Samples)
	}
	if snap[0].Fingerprint == "" {
		t.Error("expected non-empty fingerprint")
	}
	if snap[0].LastUpdate == 0 {
		t.Error("expected LastUpdate to be set")
	}
}
