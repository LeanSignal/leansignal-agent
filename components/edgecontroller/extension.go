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
	"crypto/tls"
	"sync"
	"time"

	"github.com/leansignal/leansignal-agent/components/metricsindex"
	agentv1 "github.com/leansignal/leansignal-agent/proto/gen/leansignal/agent/v1"
	"go.opentelemetry.io/collector/component"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
)

// agentVersion is reported to the backend in the Hello message.
// TODO: wire to build info.
const agentVersion = "1.0.0"

// maxMessageBytes bounds a single control message (index batches can be large).
const maxMessageBytes = 16 * 1024 * 1024

// edgeControllerExtension implements extension.Extension. It maintains a single
// long-lived bidirectional gRPC stream (AgentControl.Connect) to lean-api: the
// agent dials out and the server pushes commands back over the open stream.
type edgeControllerExtension struct {
	logger *zap.Logger
	config *Config

	// Timeseries caches
	knownTimeseriesCache      *KnownTimeseriesCache
	discoveredTimeseriesCache *DiscoveredTimeseriesCache
	demandTimeseriesCache     *DemandTimeseriesCache

	cancelFn context.CancelFunc
	rootCtx  context.Context // cancelled on Shutdown; parents query requests
	wg       sync.WaitGroup

	mu      sync.Mutex                          // guards stream lifecycle
	stream  agentv1.AgentControl_ConnectClient  // nil when disconnected
	writeMu sync.Mutex                          // serialises stream.Send (not concurrency-safe)

	corrMu  sync.Mutex
	corrSeq uint64
	pending map[uint64]chan *agentv1.Ack

	// querySem bounds concurrent local-VM query requests so a burst of UI panels
	// can't spawn unbounded goroutines or hammer the local VM.
	querySem chan struct{}
}

func newEdgeControllerExtension(logger *zap.Logger, config *Config) *edgeControllerExtension {
	return &edgeControllerExtension{
		logger:                    logger,
		config:                    config,
		knownTimeseriesCache:      NewKnownTimeseriesCache(logger),
		discoveredTimeseriesCache: NewDiscoveredTimeseriesCache(logger),
		demandTimeseriesCache:     NewDemandTimeseriesCache(logger),
		pending:                   make(map[uint64]chan *agentv1.Ack),
		querySem:                  make(chan struct{}, maxConcurrentQueries),
	}
}

// Start begins the extension's operation.
func (e *edgeControllerExtension) Start(_ context.Context, _ component.Host) error {
	e.logger.Info("Starting LeanSignal Edge Controller extension",
		zap.String("endpoint", e.config.Endpoint),
		zap.Bool("insecure", e.config.Insecure),
		zap.Bool("agent_key_set", e.config.AgentKey != ""),
	)

	leansignalmetricsindex.RegisterTimeseriesReceiver(e)

	e.knownTimeseriesCache.Init()
	e.discoveredTimeseriesCache.Init()
	e.demandTimeseriesCache.Init()

	ctx, cancel := context.WithCancel(context.Background())
	e.cancelFn = cancel
	e.rootCtx = ctx

	e.wg.Add(1)
	go e.connectionLoop(ctx)

	return nil
}

// Shutdown stops the extension.
func (e *edgeControllerExtension) Shutdown(_ context.Context) error {
	e.logger.Info("Shutting down LeanSignal Edge Controller extension")

	leansignalmetricsindex.UnregisterTimeseriesReceiver(e)

	if e.cancelFn != nil {
		e.cancelFn() // unblocks the stream Recv and the connection loop
	}
	e.wg.Wait()
	return nil
}

// GetDemands returns the current list of demanded Prometheus-normalised metric names.
// Satisfies the DemandProvider interface defined in leansignaldemandfilter.
func (e *edgeControllerExtension) GetDemands() []string {
	return e.demandTimeseriesCache.GetDemands().Timeseries
}

// ReceiveTimeseriesBatch implements leansignalmetricsindex.TimeseriesReceiver.
// Called by the metrics tracker processor when a batch is ready.
func (e *edgeControllerExtension) ReceiveTimeseriesBatch(batch *TimeseriesBatch) {
	for key, entry := range batch.Data {
		if e.knownTimeseriesCache.IsTimeseriesKnown(key) {
			e.knownTimeseriesCache.UpdateTimeseries(key, entry)
		} else {
			e.discoveredTimeseriesCache.Add(key, entry)
			e.knownTimeseriesCache.UpdateTimeseries(key, entry)
		}
	}
	e.logger.Debug("Timeseries batch processed",
		zap.Int("known_cache_size", e.knownTimeseriesCache.GetSize()),
		zap.Int("discovered_cache_size", e.discoveredTimeseriesCache.GetSize()),
	)
}

// connectionLoop maintains the persistent control stream, reconnecting on failure.
func (e *edgeControllerExtension) connectionLoop(ctx context.Context) {
	defer e.wg.Done()

	for {
		select {
		case <-ctx.Done():
			e.logger.Info("Connection loop stopped")
			return
		default:
			if err := e.connect(ctx); err != nil {
				e.logger.Error("Control stream ended", zap.Error(err))
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(e.config.ReconnectInterval):
				e.logger.Info("Reconnecting to backend...")
			}
		}
	}
}

// dialOptions builds the gRPC dial options from config.
func (e *edgeControllerExtension) dialOptions() []grpc.DialOption {
	var creds credentials.TransportCredentials
	if e.config.Insecure {
		creds = insecure.NewCredentials()
	} else {
		creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	return []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                e.config.PingInterval,
			Timeout:             20 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxMessageBytes),
			grpc.MaxCallSendMsgSize(maxMessageBytes),
		),
	}
}

// connect opens the gRPC connection + control stream and pumps messages until it ends.
func (e *edgeControllerExtension) connect(ctx context.Context) error {
	e.logger.Info("Connecting to backend", zap.String("endpoint", e.config.Endpoint))

	conn, err := grpc.NewClient(e.config.Endpoint, e.dialOptions()...)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Per-stream context carrying the agent key as metadata.
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	streamCtx = metadata.AppendToOutgoingContext(streamCtx, "x-agent-key", e.config.AgentKey)

	stream, err := agentv1.NewAgentControlClient(conn).Connect(streamCtx)
	if err != nil {
		return err
	}

	e.mu.Lock()
	e.stream = stream
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.stream = nil
		e.mu.Unlock()
	}()

	e.logger.Info("Connected to backend successfully")

	// Announce ourselves; the server pushes the current demand list in response.
	if err := e.sendAgentMessage(&agentv1.AgentMessage{
		Body: &agentv1.AgentMessage_Hello{Hello: &agentv1.Hello{Version: agentVersion}},
	}); err != nil {
		return err
	}

	pingCtx, pingCancel := context.WithCancel(streamCtx)
	go e.pingLoop(pingCtx)

	tsCtx, tsCancel := context.WithCancel(streamCtx)
	e.startTimeseriesProcessor(tsCtx)

	err = e.recvLoop(stream)
	pingCancel()
	tsCancel()
	return err
}

// pingLoop sends periodic heartbeats carrying cache statistics.
func (e *edgeControllerExtension) pingLoop(ctx context.Context) {
	ticker := time.NewTicker(e.config.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			demand := e.demandTimeseriesCache.GetDemands()
			ping := &agentv1.Ping{
				KnownCacheSize:        int32(e.knownTimeseriesCache.GetSize()),
				DiscoveredCacheSize:   int32(e.discoveredTimeseriesCache.GetSize()),
				PendingBackendUpdates: int32(e.knownTimeseriesCache.GetPendingBackendUpdates()),
				DemandCacheSize:       int32(len(demand.Timeseries)),
				DemandLastUpdate:      demand.LastUpdate,
			}
			e.logger.Info("Sending ping to backend",
				zap.Int32("known", ping.KnownCacheSize),
				zap.Int32("discovered", ping.DiscoveredCacheSize),
				zap.Int32("pending_backend_updates", ping.PendingBackendUpdates),
				zap.Int32("demand", ping.DemandCacheSize),
			)
			if err := e.sendAgentMessage(&agentv1.AgentMessage{
				Body: &agentv1.AgentMessage_Ping{Ping: ping},
			}); err != nil {
				e.logger.Warn("Failed to send ping", zap.Error(err))
				return
			}
		}
	}
}

// recvLoop reads ServerMessages until the stream closes.
func (e *edgeControllerExtension) recvLoop(stream agentv1.AgentControl_ConnectClient) error {
	for {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}
		e.handleServerMessage(msg)
	}
}

// handleServerMessage dispatches a message pushed by the backend.
func (e *edgeControllerExtension) handleServerMessage(msg *agentv1.ServerMessage) {
	switch body := msg.GetBody().(type) {
	case *agentv1.ServerMessage_Pong:
		e.logger.Debug("Received pong from backend")
	case *agentv1.ServerMessage_Ack:
		e.resolvePending(msg.GetCorrelationId(), body.Ack)
	case *agentv1.ServerMessage_DemandSet:
		metrics := body.DemandSet.GetMetrics()
		e.logger.Info("COMMAND_RECEIVED: demand_set", zap.Int("metrics_count", len(metrics)))
		e.demandTimeseriesCache.UpdateDemands(metrics)
		e.replyCommand(msg.GetCorrelationId(), true, "demand_set applied")
	case *agentv1.ServerMessage_GetStatus:
		e.logger.Info("COMMAND_RECEIVED: get_status")
		e.replyCommand(msg.GetCorrelationId(), true, "running")
	case *agentv1.ServerMessage_UpdateConfig:
		// TODO: apply config to the collector.
		e.logger.Info("COMMAND_RECEIVED: update_config")
		e.replyCommand(msg.GetCorrelationId(), true, "config received")
	case *agentv1.ServerMessage_QueryRequest:
		// Run the query off the recvLoop so a slow local-VM call never stalls
		// control-message processing; the goroutine always replies (success or error).
		corrID := msg.GetCorrelationId()
		req := body.QueryRequest
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			e.handleQueryRequest(corrID, req)
		}()
	default:
		e.logger.Warn("Received unknown server message body")
	}
}

// replyCommand sends a CommandResult for a server-initiated command (correlation_id != 0).
func (e *edgeControllerExtension) replyCommand(correlationID uint64, success bool, message string) {
	if correlationID == 0 {
		return
	}
	if err := e.sendAgentMessage(&agentv1.AgentMessage{
		CorrelationId: correlationID,
		Body:          &agentv1.AgentMessage_CommandResult{CommandResult: &agentv1.CommandResult{Success: success, Message: message}},
	}); err != nil {
		e.logger.Warn("Failed to send command result", zap.Error(err))
	}
}

// sendAgentMessage sends one message on the stream. Safe for concurrent use.
func (e *edgeControllerExtension) sendAgentMessage(msg *agentv1.AgentMessage) error {
	e.mu.Lock()
	stream := e.stream
	e.mu.Unlock()
	if stream == nil {
		return nil
	}
	e.writeMu.Lock()
	defer e.writeMu.Unlock()
	return stream.Send(msg)
}

// nextCorrID returns a fresh non-zero correlation id.
func (e *edgeControllerExtension) nextCorrID() uint64 {
	e.corrMu.Lock()
	defer e.corrMu.Unlock()
	e.corrSeq++
	return e.corrSeq
}

// resolvePending delivers an Ack to a waiting sendAndWaitAck caller.
func (e *edgeControllerExtension) resolvePending(correlationID uint64, ack *agentv1.Ack) {
	if correlationID == 0 {
		return
	}
	e.corrMu.Lock()
	ch, ok := e.pending[correlationID]
	if ok {
		delete(e.pending, correlationID)
	}
	e.corrMu.Unlock()
	if ok {
		ch <- ack
	}
}

// sendAndWaitAck assigns a correlation id, sends the message, and waits for the
// matching Ack (or AckTimeout). Used by the index-sync batch operations.
func (e *edgeControllerExtension) sendAndWaitAck(ctx context.Context, msg *agentv1.AgentMessage) (*agentv1.Ack, error) {
	id := e.nextCorrID()
	msg.CorrelationId = id

	ch := make(chan *agentv1.Ack, 1)
	e.corrMu.Lock()
	e.pending[id] = ch
	e.corrMu.Unlock()
	defer func() {
		e.corrMu.Lock()
		delete(e.pending, id)
		e.corrMu.Unlock()
	}()

	if err := e.sendAgentMessage(msg); err != nil {
		return nil, err
	}

	ackCtx, cancel := context.WithTimeout(ctx, AckTimeout)
	defer cancel()
	select {
	case ack := <-ch:
		return ack, nil
	case <-ackCtx.Done():
		return nil, ackCtx.Err()
	}
}
