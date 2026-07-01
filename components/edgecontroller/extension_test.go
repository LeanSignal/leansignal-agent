// Copyright 2026 LeanSignal
// SPDX-License-Identifier: Apache-2.0

package leansignaledgecontroller

import (
	"testing"
	"time"

	"go.uber.org/zap"

	agentv1 "github.com/leansignal/leansignal-agent/proto/gen/leansignal/agent/v1"
)

// A pushed DemandSet updates the demand cache (which the demand-filter reads).
func TestHandleServerMessageDemandSet(t *testing.T) {
	e := newEdgeControllerExtension(zap.NewNop(), &Config{})

	e.handleServerMessage(&agentv1.ServerMessage{
		Body: &agentv1.ServerMessage_DemandSet{DemandSet: &agentv1.DemandSet{Metrics: []string{"up", "go_goroutines"}}},
	})

	got := e.GetDemands()
	if len(got) != 2 || got[0] != "up" || got[1] != "go_goroutines" {
		t.Fatalf("GetDemands() = %v, want [up go_goroutines]", got)
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
