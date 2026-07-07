// Copyright 2026 LeanSignal
// SPDX-License-Identifier: Apache-2.0

package leansignaledgecontroller

import (
	"testing"
	"time"

	"go.uber.org/zap"

	agentv1 "github.com/leansignal/leansignal-agent/proto/gen/leansignal/agent/v1"
)

// A pushed DemandSet updates the demand cache (which the demand-filter reads),
// including the server's content hash echoed back in pings.
func TestHandleServerMessageDemandSet(t *testing.T) {
	e := newEdgeControllerExtension(zap.NewNop(), &Config{})

	e.handleServerMessage(&agentv1.ServerMessage{
		Body: &agentv1.ServerMessage_DemandSet{DemandSet: &agentv1.DemandSet{Metrics: []string{"up", "go_goroutines"}, Hash: 987}},
	})

	got := e.GetDemands()
	if len(got) != 2 || got[0] != "up" || got[1] != "go_goroutines" {
		t.Fatalf("GetDemands() = %v, want [up go_goroutines]", got)
	}
	if snap := e.demandTimeseriesCache.GetDemands(); snap.DemandHash != 987 {
		t.Fatalf("DemandHash = %d, want 987", snap.DemandHash)
	}
}

// buildPing reports cache sizes, the demanded ("stored") series count, and the
// demand hash for the server's staleness check.
func TestBuildPing(t *testing.T) {
	e := newEdgeControllerExtension(zap.NewNop(), &Config{})

	// Known series: a demanded histogram (2 components), a demanded gauge and
	// an undemanded one.
	e.knownTimeseriesCache.UpdateTimeseries(HashKey{1}, &TimeseriesEntry{MetricName: "http_duration_bucket", Samples: 1})
	e.knownTimeseriesCache.UpdateTimeseries(HashKey{2}, &TimeseriesEntry{MetricName: "http_duration_sum", Samples: 1})
	e.knownTimeseriesCache.UpdateTimeseries(HashKey{3}, &TimeseriesEntry{MetricName: "up", Samples: 1})
	e.knownTimeseriesCache.UpdateTimeseries(HashKey{4}, &TimeseriesEntry{MetricName: "node_load1", Samples: 1})

	e.handleServerMessage(&agentv1.ServerMessage{
		Body: &agentv1.ServerMessage_DemandSet{DemandSet: &agentv1.DemandSet{Metrics: []string{"http_duration_bucket", "up"}, Hash: 4242}},
	})

	ping := e.buildPing()
	if ping.GetKnownCacheSize() != 4 {
		t.Errorf("KnownCacheSize = %d, want 4", ping.GetKnownCacheSize())
	}
	if ping.GetDemandCacheSize() != 2 {
		t.Errorf("DemandCacheSize = %d, want 2", ping.GetDemandCacheSize())
	}
	if ping.GetDemandedKnownCacheSize() != 3 {
		t.Errorf("DemandedKnownCacheSize = %d, want 3 (bucket+sum+up)", ping.GetDemandedKnownCacheSize())
	}
	if ping.GetDemandHash() != 4242 {
		t.Errorf("DemandHash = %d, want 4242", ping.GetDemandHash())
	}
	if ping.GetDemandLastUpdate() == 0 {
		t.Error("DemandLastUpdate must be set after a demand update")
	}
}

// An Ack resolves the matching pending waiter by correlation id.
func TestHandleServerMessageAckResolvesPending(t *testing.T) {
	e := newEdgeControllerExtension(zap.NewNop(), &Config{})

	ch := make(chan *agentv1.Ack, 1)
	e.corrMu.Lock()
	e.pending[7] = ch
	e.corrMu.Unlock()

	e.handleServerMessage(&agentv1.ServerMessage{
		CorrelationId: 7,
		Body:          &agentv1.ServerMessage_Ack{Ack: &agentv1.Ack{Success: true, Message: "ok"}},
	})

	select {
	case ack := <-ch:
		if !ack.GetSuccess() {
			t.Fatalf("ack.Success = false, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("pending waiter was not resolved")
	}
}

// GetDemands is empty until a demand list arrives (fail-closed default).
func TestGetDemandsEmptyByDefault(t *testing.T) {
	e := newEdgeControllerExtension(zap.NewNop(), &Config{})
	if got := e.GetDemands(); len(got) != 0 {
		t.Fatalf("GetDemands() = %v, want empty", got)
	}
}
