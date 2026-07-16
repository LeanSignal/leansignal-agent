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

// leansignaledgecontroller/demand_timeseries_cache_test.go
package leansignaledgecontroller

import (
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestNewDemandTimeseriesCache(t *testing.T) {
	cache := NewDemandTimeseriesCache(zap.NewNop())
	if cache == nil {
		t.Fatal("expected non-nil cache")
	}
	snap := cache.GetDemands()
	if len(snap.Timeseries) != 0 {
		t.Errorf("expected empty timeseries, got %d", len(snap.Timeseries))
	}
	if snap.LastUpdate != 0 {
		t.Errorf("expected LastUpdate 0, got %d", snap.LastUpdate)
	}
}

func TestDemandTimeseriesCache_GetSize(t *testing.T) {
	cache := NewDemandTimeseriesCache(zap.NewNop())
	if got := cache.GetSize(); got != 0 {
		t.Fatalf("empty GetSize = %d, want 0", got)
	}

	cache.UpdateDemands([]string{"a", "b", "c"}, nil, nil, 1)
	if got := cache.GetSize(); got != 3 {
		t.Fatalf("GetSize = %d, want 3", got)
	}

	cache.UpdateDemands([]string{"x"}, nil, nil, 2) // replace, not append
	if got := cache.GetSize(); got != 1 {
		t.Fatalf("GetSize after replace = %d, want 1", got)
	}

	cache.Init()
	if got := cache.GetSize(); got != 0 {
		t.Fatalf("GetSize after Init = %d, want 0", got)
	}
}

func TestDemandTimeseriesCache_Init(t *testing.T) {
	cache := NewDemandTimeseriesCache(zap.NewNop())

	cache.UpdateDemands([]string{"a", "b"}, nil, nil, 0)
	if snap := cache.GetDemands(); len(snap.Timeseries) != 2 {
		t.Fatalf("expected 2 timeseries before Init, got %d", len(snap.Timeseries))
	}

	cache.Init()
	snap := cache.GetDemands()
	if len(snap.Timeseries) != 0 {
		t.Errorf("expected empty timeseries after Init, got %d", len(snap.Timeseries))
	}
	if snap.LastUpdate != 0 {
		t.Errorf("expected LastUpdate 0 after Init, got %d", snap.LastUpdate)
	}
}

func TestDemandTimeseriesCache_UpdateAndGetDemands(t *testing.T) {
	fixedTime := time.Unix(1_000_000, 0)
	cache := NewDemandTimeseriesCache(zap.NewNop())
	cache.setTimeFunc(func() time.Time { return fixedTime })

	input := []string{"cpu.usage", "mem.rss", "net.rx"}
	cache.UpdateDemands(input, nil, nil, 0)

	snap := cache.GetDemands()
	if len(snap.Timeseries) != len(input) {
		t.Fatalf("expected %d timeseries, got %d", len(input), len(snap.Timeseries))
	}
	for i, v := range input {
		if snap.Timeseries[i] != v {
			t.Errorf("index %d: expected %q, got %q", i, v, snap.Timeseries[i])
		}
	}
	if snap.LastUpdate != fixedTime.Unix() {
		t.Errorf("expected LastUpdate %d, got %d", fixedTime.Unix(), snap.LastUpdate)
	}
}

func TestDemandTimeseriesCache_UpdateDemands_ReplacesExisting(t *testing.T) {
	cache := NewDemandTimeseriesCache(zap.NewNop())

	cache.UpdateDemands([]string{"old.metric"}, nil, nil, 0)
	cache.UpdateDemands([]string{"new.a", "new.b"}, nil, nil, 0)

	snap := cache.GetDemands()
	if len(snap.Timeseries) != 2 {
		t.Fatalf("expected 2 timeseries, got %d", len(snap.Timeseries))
	}
	if snap.Timeseries[0] != "new.a" || snap.Timeseries[1] != "new.b" {
		t.Errorf("unexpected timeseries: %v", snap.Timeseries)
	}
}

func TestDemandTimeseriesCache_UpdateDemands_EmptySlice(t *testing.T) {
	cache := NewDemandTimeseriesCache(zap.NewNop())
	cache.UpdateDemands([]string{"a", "b"}, nil, nil, 0)

	cache.UpdateDemands([]string{}, nil, nil, 0)
	snap := cache.GetDemands()
	if len(snap.Timeseries) != 0 {
		t.Errorf("expected 0 timeseries after empty update, got %d", len(snap.Timeseries))
	}
}

func TestDemandTimeseriesCache_GetDemands_ReturnsCopy(t *testing.T) {
	cache := NewDemandTimeseriesCache(zap.NewNop())
	cache.UpdateDemands([]string{"original"}, nil, nil, 0)

	snap := cache.GetDemands()
	snap.Timeseries[0] = "mutated"

	// Internal state must be unchanged
	snap2 := cache.GetDemands()
	if snap2.Timeseries[0] != "original" {
		t.Errorf("expected %q, got %q (GetDemands did not return a copy)", "original", snap2.Timeseries[0])
	}
}

func TestDemandTimeseriesCache_UpdateDemands_InputMutationSafe(t *testing.T) {
	cache := NewDemandTimeseriesCache(zap.NewNop())
	input := []string{"safe"}
	cache.UpdateDemands(input, nil, nil, 0)

	// Mutate the original slice after updating
	input[0] = "mutated"

	snap := cache.GetDemands()
	if snap.Timeseries[0] != "safe" {
		t.Errorf("expected %q, got %q (UpdateDemands did not copy input)", "safe", snap.Timeseries[0])
	}
}

func TestDemandTimeseriesCache_ConcurrentAccess(t *testing.T) {
	cache := NewDemandTimeseriesCache(zap.NewNop())

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			cache.UpdateDemands([]string{"ts.a", "ts.b"}, nil, nil, 0)
		}()
		go func() {
			defer wg.Done()
			_ = cache.GetDemands()
		}()
	}

	wg.Wait()
}

func TestDemandTimeseriesCache_StoresLogSelectors(t *testing.T) {
	cache := NewDemandTimeseriesCache(zap.NewNop())

	sel := []string{`{service_name="api"}`, `{k8s_namespace_name="prod"}`}
	cache.UpdateDemands([]string{"up"}, sel, nil, 1)

	snap := cache.GetDemands()
	if len(snap.LogSelectors) != 2 || snap.LogSelectors[0] != sel[0] || snap.LogSelectors[1] != sel[1] {
		t.Errorf("unexpected log selectors: %v", snap.LogSelectors)
	}

	// Replaced, not appended; cleared on Init.
	cache.UpdateDemands(nil, []string{`{a="1"}`}, nil, 2)
	if snap := cache.GetDemands(); len(snap.LogSelectors) != 1 || snap.LogSelectors[0] != `{a="1"}` {
		t.Errorf("log selectors not replaced: %v", snap.LogSelectors)
	}
	cache.Init()
	if snap := cache.GetDemands(); len(snap.LogSelectors) != 0 {
		t.Errorf("log selectors not cleared on Init: %v", snap.LogSelectors)
	}
}

func TestDemandTimeseriesCache_LogSelectorsCopySafety(t *testing.T) {
	cache := NewDemandTimeseriesCache(zap.NewNop())
	input := []string{`{a="1"}`}
	cache.UpdateDemands(nil, input, nil, 0)

	input[0] = "mutated" // caller mutation after update must not leak in
	snap := cache.GetDemands()
	if snap.LogSelectors[0] != `{a="1"}` {
		t.Errorf("UpdateDemands did not copy log selectors: %v", snap.LogSelectors)
	}

	snap.LogSelectors[0] = "mutated" // snapshot mutation must not leak back
	if snap2 := cache.GetDemands(); snap2.LogSelectors[0] != `{a="1"}` {
		t.Errorf("GetDemands did not return a copy: %v", snap2.LogSelectors)
	}
}

func TestDemandTimeseriesCache_StoresHash(t *testing.T) {
	cache := NewDemandTimeseriesCache(zap.NewNop())

	cache.UpdateDemands([]string{"up"}, nil, nil, 777)
	if snap := cache.GetDemands(); snap.DemandHash != 777 {
		t.Errorf("DemandHash = %d, want 777", snap.DemandHash)
	}

	cache.Init()
	if snap := cache.GetDemands(); snap.DemandHash != 0 {
		t.Errorf("DemandHash after Init = %d, want 0", snap.DemandHash)
	}
}
