// Copyright 2026 LeanSignal
// SPDX-License-Identifier: Apache-2.0

package leansignaledgecontroller

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	agentv1 "github.com/leansignal/leansignal-agent/proto/gen/leansignal/agent/v1"
)

// fakeControlServer is an in-process AgentControl server: it pushes a demand list
// after the agent's Hello and acks every index op by correlation id.
type fakeControlServer struct {
	agentv1.UnimplementedAgentControlServer
	demands      []string
	logSelectors []string // LogQL stream selectors sent with the DemandSet
	demandHash   uint64   // sent with the DemandSet; agents echo it in pings
	noAck        bool     // when true, never ack index ops (to exercise the agent's ack timeout)

	mu       sync.Mutex
	received []*agentv1.AgentMessage
}

func (s *fakeControlServer) Connect(stream agentv1.AgentControl_ConnectServer) error {
	for {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.received = append(s.received, msg)
		s.mu.Unlock()

		switch msg.GetBody().(type) {
		case *agentv1.AgentMessage_Hello:
			_ = stream.Send(&agentv1.ServerMessage{
				Body: &agentv1.ServerMessage_DemandSet{DemandSet: &agentv1.DemandSet{Metrics: s.demands, LogSelectors: s.logSelectors, Hash: s.demandHash}},
			})
		case *agentv1.AgentMessage_IndexCreate, *agentv1.AgentMessage_IndexUpdate, *agentv1.AgentMessage_IndexDelete:
			if s.noAck {
				continue
			}
			_ = stream.Send(&agentv1.ServerMessage{
				CorrelationId: msg.GetCorrelationId(),
				Body:          &agentv1.ServerMessage_Ack{Ack: &agentv1.Ack{Success: true}},
			})
		}
	}
}

func (s *fakeControlServer) count(pred func(*agentv1.AgentMessage) bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, m := range s.received {
		if pred(m) {
			n++
		}
	}
	return n
}

// find returns the first received message matching pred, or nil.
func (s *fakeControlServer) find(pred func(*agentv1.AgentMessage) bool) *agentv1.AgentMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.received {
		if pred(m) {
			return m
		}
	}
	return nil
}

// startAgentAgainst boots an in-process AgentControl server with the given
// demand list and a connected edge-controller extension.
func startAgentAgainst(t *testing.T, demands []string) (*fakeControlServer, *edgeControllerExtension, func()) {
	return startAgentWith(t, &fakeControlServer{demands: demands})
}

// startAgentWith boots an in-process AgentControl server using the provided fake
// and a connected edge-controller extension. Returns the fake, the extension,
// and a cleanup func.
func startAgentWith(t *testing.T, fake *fakeControlServer) (*fakeControlServer, *edgeControllerExtension, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	agentv1.RegisterAgentControlServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()

	cfg := &Config{
		Endpoint:          lis.Addr().String(),
		AgentKey:          "test-key",
		Insecure:          true,
		ReconnectInterval: 500 * time.Millisecond,
		PingInterval:      500 * time.Millisecond,
	}
	e := newEdgeControllerExtension(zap.NewNop(), cfg)
	if err := e.Start(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	cleanup := func() { _ = e.Shutdown(context.Background()); srv.Stop() }

	// Wait until the stream is established (the server pushes demands on Hello).
	waitFor(t, 3*time.Second, func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.stream != nil
	})
	return fake, e, cleanup
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", d)
}

func TestGRPCControlChannel(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	fake := &fakeControlServer{demands: []string{"up", "go_goroutines"}}
	agentv1.RegisterAgentControlServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	cfg := &Config{
		Endpoint:          lis.Addr().String(),
		AgentKey:          "test-key",
		Insecure:          true,
		ReconnectInterval: 500 * time.Millisecond,
		PingInterval:      500 * time.Millisecond,
	}
	e := newEdgeControllerExtension(zap.NewNop(), cfg)
	if err := e.Start(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Shutdown(context.Background()) }()

	// The server pushes the demand list after Hello.
	waitFor(t, 3*time.Second, func() bool { return len(e.GetDemands()) == 2 })

	// Send an index-create batch and expect a successful ack (no error).
	err = e.sendDiscoveredTimeseriesBatch(context.Background(), []DiscoveredTimeseriesItem{
		{HashKey: "00112233445566778899aabbccddeeff", Name: "up", Labels: []string{"job", "x"}},
	})
	if err != nil {
		t.Fatalf("index create failed: %v", err)
	}

	// The server must have received exactly one IndexCreate.
	waitFor(t, 2*time.Second, func() bool {
		return fake.count(func(m *agentv1.AgentMessage) bool {
			_, ok := m.GetBody().(*agentv1.AgentMessage_IndexCreate)
			return ok
		}) == 1
	})
}

// TestDemandSetCarriesLogSelectors: a DemandSet carrying log_selectors updates
// GetLogDemands() (the LogDemandProvider surface the log demand filter reads).
func TestDemandSetCarriesLogSelectors(t *testing.T) {
	fake := &fakeControlServer{
		demands:      []string{"up"},
		logSelectors: []string{`{service_name="api"}`, `{k8s_namespace_name="prod"}`},
		demandHash:   99,
	}
	_, e, cleanup := startAgentWith(t, fake)
	defer cleanup()

	waitFor(t, 3*time.Second, func() bool { return len(e.GetLogDemands()) == 2 })
	got := e.GetLogDemands()
	if got[0] != `{service_name="api"}` || got[1] != `{k8s_namespace_name="prod"}` {
		t.Errorf("unexpected log demands: %v", got)
	}
	if len(e.GetDemands()) != 1 {
		t.Errorf("metric demands: got %d want 1", len(e.GetDemands()))
	}
}

// TestPingEchoesDemandHashAndStoredCount: after the server pushes a hashed
// demand set, the agent's periodic ping echoes that hash and reports how many
// known series match the demand ("stored timeseries").
func TestPingEchoesDemandHashAndStoredCount(t *testing.T) {
	fake := &fakeControlServer{demands: []string{"up"}, demandHash: 12345}
	_, e, cleanup := startAgentWith(t, fake)
	defer cleanup()

	// Demands arrive after Hello.
	waitFor(t, 3*time.Second, func() bool { return len(e.GetDemands()) == 1 })

	// Seed the known cache: one demanded series, one not.
	e.knownTimeseriesCache.UpdateTimeseries(HashKey{1}, &TimeseriesEntry{MetricName: "up", Samples: 1})
	e.knownTimeseriesCache.UpdateTimeseries(HashKey{2}, &TimeseriesEntry{MetricName: "node_load1", Samples: 1})

	// The next ping (500ms interval) must echo the hash and the stored count.
	waitFor(t, 3*time.Second, func() bool {
		return fake.find(func(m *agentv1.AgentMessage) bool {
			p, ok := m.GetBody().(*agentv1.AgentMessage_Ping)
			return ok && p.Ping.GetDemandHash() == 12345 && p.Ping.GetDemandedKnownCacheSize() == 1
		}) != nil
	})
}
