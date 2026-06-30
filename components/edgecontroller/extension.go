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

// leansignaledgecontroller/extension.go
package leansignaledgecontroller

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/leansignal/leansignal-agent/components/metricsindex"
	"go.opentelemetry.io/collector/component"
	"go.uber.org/zap"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// edgeControllerExtension implements extension.Extension.
type edgeControllerExtension struct {
	logger *zap.Logger
	config *Config

	// Timeseries caches
	knownTimeseriesCache      *KnownTimeseriesCache
	discoveredTimeseriesCache *DiscoveredTimeseriesCache
	demandTimeseriesCache     *DemandTimeseriesCache

	conn     *websocket.Conn
	cancelFn context.CancelFunc
	wg       sync.WaitGroup
	mu       sync.Mutex // guards conn pointer lifecycle (connect/disconnect/nil-check)
	writeMu  sync.Mutex // serialises all wsjson.Write calls on conn

	pendingMu       sync.Mutex
	pendingCommands map[string]chan AckPayload
}

func newEdgeControllerExtension(logger *zap.Logger, config *Config) *edgeControllerExtension {
	return &edgeControllerExtension{
		logger:                    logger,
		config:                    config,
		knownTimeseriesCache:      NewKnownTimeseriesCache(logger),
		discoveredTimeseriesCache: NewDiscoveredTimeseriesCache(logger),
		demandTimeseriesCache:     NewDemandTimeseriesCache(logger),
		pendingCommands:           make(map[string]chan AckPayload),
	}
}

// Start begins the extension's operation.
func (e *edgeControllerExtension) Start(ctx context.Context, host component.Host) error {
	e.logger.Info("Starting LeanSignal Edge Controller extension",
		zap.String("endpoint", e.config.Endpoint),
		zap.Bool("agent_key_set", e.config.AgentKey != ""),
	)

	// Register as a timeseries receiver
	leansignalmetricsindex.RegisterTimeseriesReceiver(e)

	// Initialise caches for the new session
	e.knownTimeseriesCache.Init()
	e.discoveredTimeseriesCache.Init()
	e.demandTimeseriesCache.Init()

	// Create a cancellable context for the background goroutine
	ctx, e.cancelFn = context.WithCancel(context.Background())

	// Start the connection manager in background
	e.wg.Add(1)
	go e.connectionLoop(ctx)

	return nil
}

// Shutdown stops the extension.
func (e *edgeControllerExtension) Shutdown(ctx context.Context) error {
	e.logger.Info("Shutting down LeanSignal Edge Controller extension")

	// Unregister as a timeseries receiver
	leansignalmetricsindex.UnregisterTimeseriesReceiver(e)

	// Signal the goroutine to stop
	if e.cancelFn != nil {
		e.cancelFn()
	}

	// Close the WebSocket connection
	e.mu.Lock()
	if e.conn != nil {
		e.conn.Close(websocket.StatusNormalClosure, "collector shutting down")
	}
	e.mu.Unlock()

	// Wait for goroutine to finish
	e.wg.Wait()

	return nil
}

// GetDemands returns the current list of demanded Prometheus-normalised metric names.
// Satisfies the DemandProvider interface defined in leansignaldemandfilter.
func (e *edgeControllerExtension) GetDemands() []string {
	return e.demandTimeseriesCache.GetDemands().Timeseries
}

// ReceiveTimeseriesBatch implements leansignalmetricsindex.TimeseriesReceiver.
// This is called by the metrics tracker processor when a batch is ready.
func (e *edgeControllerExtension) ReceiveTimeseriesBatch(batch *TimeseriesBatch) {
	e.logger.Debug("Batch received",
		zap.Int("timeseries_count", len(batch.Data)),
	)

	// Process each timeseries entry in the batch
	for key, entry := range batch.Data {
		// Check if this timeseries is already known
		if e.knownTimeseriesCache.IsTimeseriesKnown(key) {
			// Known timeseries - just update the known cache
			e.knownTimeseriesCache.UpdateTimeseries(key, entry)
		} else {
			// Unknown timeseries - add to discovered cache first, then to known cache
			e.discoveredTimeseriesCache.Add(key, entry)
			e.knownTimeseriesCache.UpdateTimeseries(key, entry)
		}
	}

	e.logger.Debug("Timeseries batch processed",
		zap.Int("known_cache_size", e.knownTimeseriesCache.GetSize()),
		zap.Int("discovered_cache_size", e.discoveredTimeseriesCache.GetSize()),
	)
}

// connectionLoop maintains a persistent connection to the backend.
func (e *edgeControllerExtension) connectionLoop(ctx context.Context) {
	defer e.wg.Done()

	for {
		select {
		case <-ctx.Done():
			e.logger.Info("Connection loop stopped")
			return
		default:
			if err := e.connect(ctx); err != nil {
				e.logger.Error("Connection failed", zap.Error(err))
			}

			// Wait before reconnecting
			select {
			case <-ctx.Done():
				return
			case <-time.After(e.config.ReconnectInterval):
				e.logger.Info("Attempting to reconnect...")
			}
		}
	}
}

// connect establishes a WebSocket connection and handles messages.
func (e *edgeControllerExtension) connect(ctx context.Context) error {
	e.logger.Info("Connecting to backend", zap.String("endpoint", e.config.Endpoint))

	// Set up connection options with X-Agent-Key header
	opts := &websocket.DialOptions{
		HTTPHeader: map[string][]string{
			"X-Agent-Key": {e.config.AgentKey},
		},
	}

	// Dial the WebSocket server
	conn, _, err := websocket.Dial(ctx, e.config.Endpoint, opts)
	if err != nil {
		return err
	}

	// Set read limit to 5MB to handle large messages
	conn.SetReadLimit(5 * 1024 * 1024)

	e.mu.Lock()
	e.conn = conn
	e.mu.Unlock()

	e.logger.Info("Connected to backend successfully")

	// Send initial registration message
	if err := e.sendRegistration(ctx); err != nil {
		e.logger.Error("Failed to send registration", zap.Error(err))
		conn.Close(websocket.StatusInternalError, "registration failed")
		return err
	}

	// Request initial demand metrics from backend
	go e.requestDemandMetrics(ctx)

	// Start ping routine
	pingCtx, pingCancel := context.WithCancel(ctx)
	go e.pingLoop(pingCtx)

	// Start timeseries processor (handles both discovered and known)
	timeseriesCtx, timeseriesCancel := context.WithCancel(ctx)
	e.startTimeseriesProcessor(timeseriesCtx)

	// Read messages until connection closes
	err = e.readLoop(ctx)
	pingCancel()
	timeseriesCancel()

	e.mu.Lock()
	e.conn = nil
	e.mu.Unlock()

	return err
}

// sendRegistration sends initial registration message to the backend.
func (e *edgeControllerExtension) sendRegistration(ctx context.Context) error {
	msg := Message{
		Type:      MessageTypeStatus,
		Timestamp: time.Now().UTC(),
		Payload: StatusPayload{
			Status:  "connected",
			Version: "1.0.0", // TODO: Get from build info
		},
	}
	return e.sendMessage(ctx, msg)
}

// pingLoop sends periodic heartbeat messages.
func (e *edgeControllerExtension) pingLoop(ctx context.Context) {
	ticker := time.NewTicker(e.config.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			knownSize := e.knownTimeseriesCache.GetSize()
			discoveredSize := e.discoveredTimeseriesCache.GetSize()
			pendingUpdates := e.knownTimeseriesCache.GetPendingBackendUpdates()
			demandSnap := e.demandTimeseriesCache.GetDemands()
			demandSize := len(demandSnap.Timeseries)
			demandLastUpdate := demandSnap.LastUpdate
			e.logger.Info("Sending ping to backend",
				zap.Int("known_timeseries_cache_size", knownSize),
				zap.Int("discovered_timeseries_cache_size", discoveredSize),
				zap.Int("pending_backend_updates", pendingUpdates),
				zap.Int("demand_timeseries_cache_size", demandSize),
				zap.Int64("demand_last_update", demandLastUpdate),
			)
			msg := Message{
				Type:      MessageTypePing,
				Timestamp: time.Now().UTC(),
				Payload: PingPayload{
					KnownTimeseriesCacheSize:      knownSize,
					DiscoveredTimeseriesCacheSize: discoveredSize,
					PendingBackendUpdates:         pendingUpdates,
					DemandTimeseriesCacheSize:     demandSize,
					DemandLastUpdate:              demandLastUpdate,
				},
			}
			if err := e.sendMessage(ctx, msg); err != nil {
				e.logger.Warn("Failed to send ping", zap.Error(err))
				return
			}
		}
	}
}

// readLoop reads and processes incoming messages.
func (e *edgeControllerExtension) readLoop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			var msg Message
			err := wsjson.Read(ctx, e.conn, &msg)
			if err != nil {
				e.logger.Error("Failed to read message", zap.Error(err))
				return err
			}

			e.handleMessage(ctx, msg)
		}
	}
}

// handleMessage processes incoming commands from the backend.
func (e *edgeControllerExtension) handleMessage(ctx context.Context, msg Message) {
	e.logger.Info("Received message",
		zap.String("type", string(msg.Type)),
		zap.String("id", msg.ID),
	)

	switch msg.Type {
	case MessageTypeCommand:
		e.handleCommand(ctx, msg)
	case MessageTypeAck:
		e.logger.Info("Received ack from backend",
			zap.String("id", msg.ID),
		)
		// Resolve any pending command waiting for this ack
		if msg.ID != "" {
			var ack AckPayload
			if payloadBytes, err := json.Marshal(msg.Payload); err == nil {
				json.Unmarshal(payloadBytes, &ack)
			}
			e.resolvePending(msg.ID, ack)
		}
	case MessageTypePong:
		e.logger.Info("Received pong from backend")
	default:
		e.logger.Warn("Unknown message type", zap.String("type", string(msg.Type)))
	}
}

// handleCommand processes a command from the backend.
func (e *edgeControllerExtension) handleCommand(ctx context.Context, msg Message) {
	// Parse the command payload
	payloadBytes, err := json.Marshal(msg.Payload)
	if err != nil {
		e.logger.Error("Failed to marshal payload", zap.Error(err))
		e.sendAck(ctx, msg.ID, "error", "failed to parse payload")
		return
	}

	var cmd CommandPayload
	if err := json.Unmarshal(payloadBytes, &cmd); err != nil {
		e.logger.Error("Failed to unmarshal command", zap.Error(err))
		e.sendAck(ctx, msg.ID, "error", "invalid command format")
		return
	}

	e.logger.Info("COMMAND_RECEIVED: "+cmd.Command,
		zap.String("id", msg.ID),
		zap.Any("data", cmd.Data),
	)

	// Handle different command types
	switch cmd.Command {
	case CmdUpdateConfig:
		e.handleUpdateConfig(ctx, msg.ID, cmd.Data)
	case CmdGetStatus:
		e.handleGetStatus(ctx, msg.ID)
	case CmdPing:
		e.sendAck(ctx, msg.ID, "success", "pong")
	case CmdDemandUpdate:
		e.logger.Info("COMMAND_RECEIVED: demand_update",
			zap.String("id", msg.ID),
			zap.Any("data", cmd.Data),
		)
		e.sendAck(ctx, msg.ID, "success", "demand_update received")
	case CmdDemandSet:
		e.handleDemandSet(ctx, msg.ID, cmd.Data)
	default:
		e.logger.Warn("COMMAND_RECEIVED: unknown command", zap.String("command", cmd.Command))
		e.sendAck(ctx, msg.ID, "error", "unknown command")
	}
}

// handleUpdateConfig processes a configuration update command.
func (e *edgeControllerExtension) handleUpdateConfig(ctx context.Context, msgID string, data json.RawMessage) {
	e.logger.Info("Received config update", zap.Any("data", data))

	// TODO: Implement actual config update logic
	// This could involve:
	// 1. Parsing the new config
	// 2. Validating it
	// 3. Applying it to the collector (may require host interaction)

	e.sendAck(ctx, msgID, "success", "config update received")
}

// handleGetStatus responds with current collector status.
func (e *edgeControllerExtension) handleGetStatus(ctx context.Context, msgID string) {
	// TODO: Gather actual status from the collector
	status := StatusPayload{
		Status:  "running",
		Version: "1.0.0",
	}

	response := Message{
		Type:      MessageTypeAck,
		ID:        msgID,
		Timestamp: time.Now().UTC(),
		Payload:   status,
	}

	if err := e.sendMessage(ctx, response); err != nil {
		e.logger.Error("Failed to send status response", zap.Error(err))
	}
}

// handleDemandSet processes demand_set command containing the current metrics list.
func (e *edgeControllerExtension) handleDemandSet(ctx context.Context, msgID string, data json.RawMessage) {
	var metrics []string
	if err := json.Unmarshal(data, &metrics); err != nil {
		e.logger.Error("COMMAND_RECEIVED: demand_set - failed to parse metrics",
			zap.String("id", msgID),
			zap.Error(err),
		)
		e.sendAck(ctx, msgID, "error", "invalid demand_set data")
		return
	}

	e.logger.Info("COMMAND_RECEIVED: demand_set",
		zap.String("id", msgID),
		zap.Int("metrics_count", len(metrics)),
		zap.Strings("metrics", metrics),
	)

	e.demandTimeseriesCache.UpdateDemands(metrics)
	e.sendAck(ctx, msgID, "success", "demand_set received")
}

// sendAck sends an acknowledgment message.
func (e *edgeControllerExtension) sendAck(ctx context.Context, msgID, status, message string) {
	ack := Message{
		Type:      MessageTypeAck,
		ID:        msgID,
		Timestamp: time.Now().UTC(),
		Payload: AckPayload{
			Status:  status,
			Message: message,
		},
	}

	if err := e.sendMessage(ctx, ack); err != nil {
		e.logger.Error("Failed to send ack", zap.Error(err))
	}
}

// sendMessage sends a message over the WebSocket connection.
// It is safe for concurrent use — writeMu serialises all writes on the conn.
func (e *edgeControllerExtension) sendMessage(ctx context.Context, msg Message) error {
	e.mu.Lock()
	conn := e.conn
	e.mu.Unlock()

	if conn == nil {
		return nil
	}

	e.writeMu.Lock()
	defer e.writeMu.Unlock()
	return wsjson.Write(ctx, conn, msg)
}

// resolvePending delivers an ack to a waiting SendCommand caller.
func (e *edgeControllerExtension) resolvePending(msgID string, ack AckPayload) {
	e.pendingMu.Lock()
	ch, ok := e.pendingCommands[msgID]
	if ok {
		delete(e.pendingCommands, msgID)
	}
	e.pendingMu.Unlock()

	if ok {
		ch <- ack
	}
}

// SendCommand sends a command message to the lean-api backend and waits for
// an acknowledgment. Returns the AckPayload from the receiver, or an error
// if the send fails or the ack times out.
func (e *edgeControllerExtension) SendCommand(ctx context.Context, command string, data json.RawMessage) (*AckPayload, error) {
	msgID := uuid.New().String()

	// Register pending ack before sending
	ackCh := make(chan AckPayload, 1)
	e.pendingMu.Lock()
	e.pendingCommands[msgID] = ackCh
	e.pendingMu.Unlock()

	defer func() {
		e.pendingMu.Lock()
		delete(e.pendingCommands, msgID)
		e.pendingMu.Unlock()
	}()

	msg := Message{
		Type:      MessageTypeCommand,
		ID:        msgID,
		Timestamp: time.Now().UTC(),
		Payload: CommandPayload{
			Command: command,
			Data:    data,
		},
	}

	e.logger.Info("COMMAND_SENT: "+command,
		zap.String("msg_id", msgID),
	)

	if err := e.sendMessage(ctx, msg); err != nil {
		return nil, err
	}

	// Wait for ack with dedicated timeout
	ackCtx, ackCancel := context.WithTimeout(ctx, AckTimeout)
	defer ackCancel()

	select {
	case ack := <-ackCh:
		e.logger.Info("COMMAND_ACK_RECEIVED: "+command,
			zap.String("msg_id", msgID),
			zap.String("ack_status", ack.Status),
			zap.String("ack_message", ack.Message),
		)
		return &ack, nil
	case <-ackCtx.Done():
		e.logger.Warn("Timeout waiting for ack",
			zap.String("msg_id", msgID),
			zap.String("command", command),
		)
		return nil, ackCtx.Err()
	}
}

// requestDemandMetrics sends a demand_get command to retrieve the current metrics list.
// This is called on startup to get the initial demand configuration.
func (e *edgeControllerExtension) requestDemandMetrics(ctx context.Context) {
	e.logger.Info("Requesting demand metrics from backend")

	ack, err := e.SendCommand(ctx, CmdDemandGet, nil)
	if err != nil {
		e.logger.Error("Failed to get demand metrics",
			zap.Error(err),
		)
		return
	}

	if ack.Status != "success" {
		e.logger.Warn("demand_get returned non-success status",
			zap.String("status", ack.Status),
			zap.String("message", ack.Message),
		)
		return
	}

	// Parse the metrics from the ack message (JSON array of strings)
	var metrics []string
	if err := json.Unmarshal([]byte(ack.Message), &metrics); err != nil {
		e.logger.Error("Failed to parse demand metrics response",
			zap.Error(err),
			zap.String("message", ack.Message),
		)
		return
	}

	e.logger.Info("demand_get: received metrics list",
		zap.Int("metrics_count", len(metrics)),
		zap.Strings("metrics", metrics),
	)

	e.demandTimeseriesCache.UpdateDemands(metrics)
}
