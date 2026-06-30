// Copyright 2026 LeanSignal
// SPDX-License-Identifier: Apache-2.0

package leansignalmetricsindex

import (
	"encoding/json"
	"testing"
)

func TestHashKeyJSONRoundTrip(t *testing.T) {
	var k HashKey
	copy(k[:], []byte("0123456789abcdef")) // 16 bytes

	b, err := json.Marshal(k)
	if err != nil {
		t.Fatal(err)
	}
	// Encodes as a 32-char hex string (quoted).
	if len(b) != 34 {
		t.Fatalf("marshaled length = %d, want 34 (32 hex + 2 quotes): %s", len(b), b)
	}

	var out HashKey
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out != k {
		t.Fatalf("round-trip mismatch: got %x want %x", out, k)
	}
	if k.String() == "" || len(k.String()) != 32 {
		t.Fatalf("String() = %q, want 32 hex chars", k.String())
	}
}

func TestHashKeyUnmarshalInvalidLength(t *testing.T) {
	var out HashKey
	if err := json.Unmarshal([]byte(`"abcd"`), &out); err == nil {
		t.Fatal("expected error for short hex string")
	}
}

type capture struct{ batches []*TimeseriesBatch }

func (c *capture) ReceiveTimeseriesBatch(b *TimeseriesBatch) { c.batches = append(c.batches, b) }

func TestRegistryBroadcastAndUnregister(t *testing.T) {
	c := &capture{}
	RegisterTimeseriesReceiver(c)
	defer UnregisterTimeseriesReceiver(c)

	batch := &TimeseriesBatch{Data: map[HashKey]*TimeseriesEntry{
		{}: {MetricName: "up", Samples: 1},
	}}
	BroadcastTimeseriesBatch(batch)
	if len(c.batches) != 1 {
		t.Fatalf("receiver got %d batches, want 1", len(c.batches))
	}

	// After unregister, no more deliveries.
	UnregisterTimeseriesReceiver(c)
	BroadcastTimeseriesBatch(batch)
	if len(c.batches) != 1 {
		t.Fatalf("receiver got %d batches after unregister, want 1", len(c.batches))
	}
}
