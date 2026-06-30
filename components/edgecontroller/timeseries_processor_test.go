// Copyright 2026 LeanSignal
// SPDX-License-Identifier: Apache-2.0

package leansignaledgecontroller

import (
	"bytes"
	"context"
	"testing"
	"time"

	agentv1 "github.com/leansignal/leansignal-agent/proto/gen/leansignal/agent/v1"
)

func hk(b string) HashKey {
	var k HashKey
	copy(k[:], b)
	return k
}

// Discovered cache -> IndexCreate over gRPC: verifies the protobuf conversion
// (fingerprint bytes, metric name, flattened labels) and that a successful ack
// purges the batch from the cache.
func TestProcessorDiscoveredBatchConversion(t *testing.T) {
	fake, e, cleanup := startAgentAgainst(t, nil)
	defer cleanup()

	key := hk("fingerprint-0001") // 16 bytes
	e.discoveredTimeseriesCache.Add(key, &TimeseriesEntry{
		MetricName: "up",
		Labels:     []LabelPair{{Name: "job", Value: "api"}, {Name: "instance", Value: "1"}},
		Samples:    3,
	})

	if !e.processOneDiscoveredBatch(context.Background()) {
		t.Fatal("expected a discovered batch to be processed")
	}

	waitFor(t, 2*time.Second, func() bool {
		return fake.find(func(m *agentv1.AgentMessage) bool {
			_, ok := m.GetBody().(*agentv1.AgentMessage_IndexCreate)
			return ok
		}) != nil
	})

	msg := fake.find(func(m *agentv1.AgentMessage) bool {
		_, ok := m.GetBody().(*agentv1.AgentMessage_IndexCreate)
		return ok
	})
	series := msg.GetBody().(*agentv1.AgentMessage_IndexCreate).IndexCreate.GetSeries()
	if len(series) != 1 {
		t.Fatalf("expected 1 series, got %d", len(series))
	}
	s := series[0]
	if !bytes.Equal(s.GetFingerprint(), key[:]) {
		t.Errorf("fingerprint mismatch: got %x want %x", s.GetFingerprint(), key[:])
	}
	if s.GetMetricName() != "up" {
		t.Errorf("metric name: got %q want up", s.GetMetricName())
	}
	labels := s.GetLabels()
	if len(labels) != 2 || labels[0].GetName() != "job" || labels[0].GetValue() != "api" ||
		labels[1].GetName() != "instance" || labels[1].GetValue() != "1" {
		t.Errorf("labels mismatch: %+v", labels)
	}

	// Successful ack must purge the discovered cache.
	waitFor(t, 2*time.Second, func() bool { return e.discoveredTimeseriesCache.GetSize() == 0 })
}

// Known cache -> IndexUpdate (active) + IndexDelete (inactive) over gRPC.
func TestProcessorKnownBatchActiveAndInactive(t *testing.T) {
	fake, e, cleanup := startAgentAgainst(t, nil)
	defer cleanup()

	active := hk("active-fp-000001")
	inactive := hk("inactive-fp-0001")
	e.knownTimeseriesCache.UpdateTimeseries(active, &TimeseriesEntry{MetricName: "up", Samples: 5})
	e.knownTimeseriesCache.UpdateTimeseries(inactive, &TimeseriesEntry{MetricName: "down", Samples: 0})

	if !e.processOneKnownBatch(context.Background()) {
		t.Fatal("expected a known batch to be processed")
	}

	// IndexDelete carries the inactive fingerprint.
	waitFor(t, 2*time.Second, func() bool {
		m := fake.find(func(m *agentv1.AgentMessage) bool {
			_, ok := m.GetBody().(*agentv1.AgentMessage_IndexDelete)
			return ok
		})
		if m == nil {
			return false
		}
		for _, fp := range m.GetBody().(*agentv1.AgentMessage_IndexDelete).IndexDelete.GetFingerprints() {
			if bytes.Equal(fp, inactive[:]) {
				return true
			}
		}
		return false
	})

	// IndexUpdate carries the active fingerprint with its sample count.
	waitFor(t, 2*time.Second, func() bool {
		m := fake.find(func(m *agentv1.AgentMessage) bool {
			_, ok := m.GetBody().(*agentv1.AgentMessage_IndexUpdate)
			return ok
		})
		if m == nil {
			return false
		}
		for _, s := range m.GetBody().(*agentv1.AgentMessage_IndexUpdate).IndexUpdate.GetSeries() {
			if bytes.Equal(s.GetFingerprint(), active[:]) && s.GetSamples() == 5 {
				return true
			}
		}
		return false
	})

	// Inactive deleted, active kept (marked synced) => one entry remains.
	waitFor(t, 2*time.Second, func() bool { return e.knownTimeseriesCache.GetSize() == 1 })
}

// When the server never acks, the batch send fails (ack timeout via the caller's
// context) rather than hanging.
func TestSendBatchAckTimeout(t *testing.T) {
	_, e, cleanup := startAgentWith(t, &fakeControlServer{noAck: true})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()

	err := e.sendDiscoveredTimeseriesBatch(ctx, []DiscoveredTimeseriesItem{
		{HashKey: "00112233445566778899aabbccddeeff", Name: "up", Labels: []string{"job", "x"}},
	})
	if err == nil {
		t.Fatal("expected an error when the server does not ack")
	}
}
