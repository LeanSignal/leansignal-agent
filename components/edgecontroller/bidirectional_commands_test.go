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

package leansignaledgecontroller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newTestExtension() *edgeControllerExtension {
	logger := zap.NewNop()
	config := &Config{
		Endpoint:          "ws://localhost:0/ws",
		AgentKey:          "test-key",
		ReconnectInterval: 1 * time.Second,
		PingInterval:      30 * time.Second,
	}
	return newEdgeControllerExtension(logger, config)
}

// connectToMockBackend creates an httptest server, dials it, and wires the
// extension's conn. Returns the server-side *websocket.Conn for assertions.
// Everything is cleaned up automatically via t.Cleanup.
func connectToMockBackend(t *testing.T, ext *edgeControllerExtension) *websocket.Conn {
	t.Helper()
	var (
		mu       sync.Mutex
		serverWS *websocket.Conn
		ready    = make(chan struct{})
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: []string{"*"},
		})
		if err != nil {
			return
		}
		mu.Lock()
		serverWS = conn
		mu.Unlock()
		close(ready)
		<-r.Context().Done()
	}))

	wsURL := "ws" + srv.URL[len("http"):]
	conn, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		srv.Close()
		t.Fatalf("failed to dial mock backend: %v", err)
	}

	ext.mu.Lock()
	ext.conn = conn
	ext.mu.Unlock()

	// Cleanup: closing the server terminates all connections immediately
	t.Cleanup(func() {
		srv.Close()
	})

	<-ready
	mu.Lock()
	defer mu.Unlock()
	return serverWS
}

// ---------------------------------------------------------------------------
// handleCommand tests (edge-controller receiving commands from lean-api)
// ---------------------------------------------------------------------------

func TestHandleCommand_DemandUpdate_Direct(t *testing.T) {
	ext := newTestExtension()
	serverConn := connectToMockBackend(t, ext)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmdMsg := Message{
		Type:      MessageTypeCommand,
		ID:        "test-cmd-demand",
		Timestamp: time.Now().UTC(),
		Payload: CommandPayload{
			Command: CmdDemandUpdate,
			Data:    json.RawMessage(`{"demand_id":"d-1"}`),
		},
	}
	ext.handleCommand(ctx, cmdMsg)

	var ack Message
	if err := wsjson.Read(ctx, serverConn, &ack); err != nil {
		t.Fatalf("failed to read ack: %v", err)
	}

	if ack.Type != MessageTypeAck {
		t.Errorf("expected ack type, got %s", ack.Type)
	}
	if ack.ID != "test-cmd-demand" {
		t.Errorf("expected ID test-cmd-demand, got %s", ack.ID)
	}

	payloadBytes, _ := json.Marshal(ack.Payload)
	var ackPayload AckPayload
	json.Unmarshal(payloadBytes, &ackPayload)

	if ackPayload.Status != "success" {
		t.Errorf("expected ack status success, got %s", ackPayload.Status)
	}
	if ackPayload.Message != "demand_update received" {
		t.Errorf("unexpected ack message: %s", ackPayload.Message)
	}
}

func TestHandleCommand_UnknownCommand_Direct(t *testing.T) {
	ext := newTestExtension()
	serverConn := connectToMockBackend(t, ext)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmdMsg := Message{
		Type:      MessageTypeCommand,
		ID:        "test-cmd-unknown",
		Timestamp: time.Now().UTC(),
		Payload: CommandPayload{
			Command: "totally_bogus",
			Data:    nil,
		},
	}
	ext.handleCommand(ctx, cmdMsg)

	var ack Message
	if err := wsjson.Read(ctx, serverConn, &ack); err != nil {
		t.Fatalf("failed to read ack: %v", err)
	}

	payloadBytes, _ := json.Marshal(ack.Payload)
	var ackPayload AckPayload
	json.Unmarshal(payloadBytes, &ackPayload)

	if ackPayload.Status != "error" {
		t.Errorf("expected ack status error, got %s", ackPayload.Status)
	}
}

func TestHandleCommand_Ping_Direct(t *testing.T) {
	ext := newTestExtension()
	serverConn := connectToMockBackend(t, ext)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmdMsg := Message{
		Type:      MessageTypeCommand,
		ID:        "test-ping",
		Timestamp: time.Now().UTC(),
		Payload: CommandPayload{
			Command: CmdPing,
		},
	}
	ext.handleCommand(ctx, cmdMsg)

	var ack Message
	if err := wsjson.Read(ctx, serverConn, &ack); err != nil {
		t.Fatalf("failed to read ack: %v", err)
	}

	payloadBytes, _ := json.Marshal(ack.Payload)
	var ackPayload AckPayload
	json.Unmarshal(payloadBytes, &ackPayload)

	if ackPayload.Status != "success" {
		t.Errorf("expected success, got %s", ackPayload.Status)
	}
	if ackPayload.Message != "pong" {
		t.Errorf("expected pong, got %s", ackPayload.Message)
	}
}

// ---------------------------------------------------------------------------
// SendCommand tests (edge-controller sending commands to lean-api)
// ---------------------------------------------------------------------------

func TestSendCommand_WithAck(t *testing.T) {
	ext := newTestExtension()
	serverConn := connectToMockBackend(t, ext)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ext.mu.Lock()
	clientConn := ext.conn
	ext.mu.Unlock()

	// Simulate lean-api: read command, send ack back
	go func() {
		var msg Message
		if err := wsjson.Read(ctx, serverConn, &msg); err != nil {
			return
		}
		ack := Message{
			Type:      MessageTypeAck,
			ID:        msg.ID,
			Timestamp: time.Now().UTC(),
			Payload:   AckPayload{Status: "success", Message: "command received"},
		}
		wsjson.Write(ctx, serverConn, ack)
	}()

	// Read ack on client side and resolve pending
	go func() {
		var msg Message
		if err := wsjson.Read(ctx, clientConn, &msg); err != nil {
			return
		}
		if msg.Type == MessageTypeAck && msg.ID != "" {
			var ack AckPayload
			if pb, err := json.Marshal(msg.Payload); err == nil {
				json.Unmarshal(pb, &ack)
			}
			ext.resolvePending(msg.ID, ack)
		}
	}()

	data := json.RawMessage(`{"demand_id":"d-1"}`)
	ack, err := ext.SendCommand(ctx, CmdDemandGet, data)
	if err != nil {
		t.Fatalf("SendCommand failed: %v", err)
	}

	if ack.Status != "success" {
		t.Errorf("expected ack status success, got %s", ack.Status)
	}
	if ack.Message != "command received" {
		t.Errorf("unexpected ack message: %s", ack.Message)
	}
}

func TestSendCommand_Timeout(t *testing.T) {
	ext := newTestExtension()
	connectToMockBackend(t, ext)

	// Very short timeout - no one reads or acks on the other side
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer shortCancel()

	_, err := ext.SendCommand(shortCtx, CmdMetricsIndexCreate, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestSendCommand_AllCommands(t *testing.T) {
	commands := []struct {
		cmd  string
		data json.RawMessage
	}{
		{CmdMetricsIndexDelete, json.RawMessage(`{"id":"1"}`)},
		{CmdMetricsIndexUpdate, json.RawMessage(`{"id":"1","labels":{}}`)},
		{CmdMetricsIndexCreate, json.RawMessage(`{"metric":"cpu"}`)},
		{CmdMetricsIndexGet, json.RawMessage(`{"id":"1"}`)},
		{CmdDemandGet, json.RawMessage(`{"demand_id":"d-1"}`)},
	}

	for _, tc := range commands {
		t.Run(tc.cmd, func(t *testing.T) {
			ext := newTestExtension()
			serverConn := connectToMockBackend(t, ext)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			ext.mu.Lock()
			clientConn := ext.conn
			ext.mu.Unlock()

			// Mock lean-api: read command + send ack
			go func() {
				var msg Message
				if err := wsjson.Read(ctx, serverConn, &msg); err != nil {
					return
				}
				ack := Message{
					Type:      MessageTypeAck,
					ID:        msg.ID,
					Timestamp: time.Now().UTC(),
					Payload:   AckPayload{Status: "success", Message: tc.cmd + " ok"},
				}
				wsjson.Write(ctx, serverConn, ack)
			}()

			// Read ack on client side
			go func() {
				var msg Message
				if err := wsjson.Read(ctx, clientConn, &msg); err != nil {
					return
				}
				if msg.Type == MessageTypeAck && msg.ID != "" {
					var ack AckPayload
					if pb, err := json.Marshal(msg.Payload); err == nil {
						json.Unmarshal(pb, &ack)
					}
					ext.resolvePending(msg.ID, ack)
				}
			}()

			ack, err := ext.SendCommand(ctx, tc.cmd, tc.data)
			if err != nil {
				t.Fatalf("SendCommand(%s) failed: %v", tc.cmd, err)
			}
			if ack.Status != "success" {
				t.Errorf("expected success, got %s", ack.Status)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Pending ack mechanism tests (edge-controller internal)
// ---------------------------------------------------------------------------

func TestResolvePending_ConcurrentMultiple(t *testing.T) {
	ext := newTestExtension()

	ids := []string{"a", "b", "c"}
	channels := make(map[string]chan AckPayload)
	for _, id := range ids {
		ch := make(chan AckPayload, 1)
		ext.pendingMu.Lock()
		ext.pendingCommands[id] = ch
		ext.pendingMu.Unlock()
		channels[id] = ch
	}

	// Resolve in reverse order
	for i := len(ids) - 1; i >= 0; i-- {
		ext.resolvePending(ids[i], AckPayload{Status: "success", Message: ids[i]})
	}

	for _, id := range ids {
		select {
		case ack := <-channels[id]:
			if ack.Message != id {
				t.Errorf("expected %s, got %s", id, ack.Message)
			}
		case <-time.After(1 * time.Second):
			t.Fatalf("timed out on %s", id)
		}
	}
}

func TestResolvePending_UnknownID(t *testing.T) {
	ext := newTestExtension()
	// Should not panic
	ext.resolvePending("does-not-exist", AckPayload{Status: "success"})
}

// ---------------------------------------------------------------------------
// handleMessage routing tests
// ---------------------------------------------------------------------------

func TestHandleMessage_AckResolvePending(t *testing.T) {
	ext := newTestExtension()

	ch := make(chan AckPayload, 1)
	ext.pendingMu.Lock()
	ext.pendingCommands["test-ack-id"] = ch
	ext.pendingMu.Unlock()

	msg := Message{
		Type:      MessageTypeAck,
		ID:        "test-ack-id",
		Timestamp: time.Now().UTC(),
		Payload:   AckPayload{Status: "success", Message: "handled"},
	}
	ext.handleMessage(context.Background(), msg)

	select {
	case ack := <-ch:
		if ack.Status != "success" {
			t.Errorf("expected success, got %s", ack.Status)
		}
		if ack.Message != "handled" {
			t.Errorf("expected handled, got %s", ack.Message)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for ack resolution")
	}
}

func TestHandleMessage_Pong(t *testing.T) {
	ext := newTestExtension()
	msg := Message{
		Type:      MessageTypePong,
		Timestamp: time.Now().UTC(),
	}
	ext.handleMessage(context.Background(), msg)
}

func TestHandleMessage_UnknownType(t *testing.T) {
	ext := newTestExtension()
	msg := Message{
		Type:      "totally_unknown",
		Timestamp: time.Now().UTC(),
	}
	ext.handleMessage(context.Background(), msg)
}

// ---------------------------------------------------------------------------
// Concurrent write safety tests (run with -race)
// ---------------------------------------------------------------------------

// TestConcurrentSendMessage verifies that multiple goroutines can call
// sendMessage simultaneously without a data race. The -race detector will
// flag any unsynchronised access.
func TestConcurrentSendMessage(t *testing.T) {
	ext := newTestExtension()
	serverConn := connectToMockBackend(t, ext)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Drain messages on the server side so writes don't block
	go func() {
		for {
			_, _, err := serverConn.Read(ctx)
			if err != nil {
				return
			}
		}
	}()

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(n int) {
			defer wg.Done()
			msg := Message{
				Type:      MessageTypePing,
				Timestamp: time.Now().UTC(),
			}
			_ = ext.sendMessage(ctx, msg)
		}(i)
	}

	wg.Wait()
}

// TestConcurrentSendCommand_AndHandleCommand verifies that SendCommand (which
// writes outgoing commands) and handleCommand (which writes acks for incoming
// commands) can run concurrently without a data race.
func TestConcurrentSendCommand_AndHandleCommand(t *testing.T) {
	ext := newTestExtension()
	serverConn := connectToMockBackend(t, ext)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ext.mu.Lock()
	clientConn := ext.conn
	ext.mu.Unlock()

	// Drain and auto-ack on the server side for SendCommand calls
	go func() {
		for {
			var msg Message
			if err := wsjson.Read(ctx, serverConn, &msg); err != nil {
				return
			}
			if msg.Type == MessageTypeCommand {
				ack := Message{
					Type:      MessageTypeAck,
					ID:        msg.ID,
					Timestamp: time.Now().UTC(),
					Payload:   AckPayload{Status: "success", Message: "ok"},
				}
				wsjson.Write(ctx, serverConn, ack)
			}
		}
	}()

	// Read acks on the client side and resolve pending
	go func() {
		for {
			var msg Message
			if err := wsjson.Read(ctx, clientConn, &msg); err != nil {
				return
			}
			if msg.Type == MessageTypeAck && msg.ID != "" {
				var ack AckPayload
				if pb, err := json.Marshal(msg.Payload); err == nil {
					json.Unmarshal(pb, &ack)
				}
				ext.resolvePending(msg.ID, ack)
			}
		}
	}()

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines * 2) // half SendCommand, half handleCommand

	// Half the goroutines send outgoing commands (writes)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, _ = ext.SendCommand(ctx, CmdDemandGet, json.RawMessage(`{"id":"1"}`))
		}()
	}

	// Half the goroutines simulate incoming commands that trigger ack writes
	for i := 0; i < goroutines; i++ {
		go func(n int) {
			defer wg.Done()
			cmdMsg := Message{
				Type:      MessageTypeCommand,
				ID:        "incoming-" + time.Now().String(),
				Timestamp: time.Now().UTC(),
				Payload: CommandPayload{
					Command: CmdDemandUpdate,
					Data:    json.RawMessage(`{"demand_id":"d-1"}`),
				},
			}
			ext.handleCommand(ctx, cmdMsg)
		}(i)
	}

	wg.Wait()
}
