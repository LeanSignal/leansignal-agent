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

// leansignaledgecontroller/config_handler.go
//
// Stream handlers for the two config commands: GetConfig (read the collector's
// config files) and UpdateConfig (validate, write, reload). See
// config_manager.go for the machinery they drive.
package leansignaledgecontroller

import (
	"time"

	"go.uber.org/zap"

	agentv1 "github.com/leansignal/leansignal-agent/proto/gen/leansignal/agent/v1"
)

// sendConfigSnapshot answers a GetConfig with the current on-disk config.
func (e *edgeControllerExtension) sendConfigSnapshot(correlationID uint64) {
	if correlationID == 0 {
		return
	}

	snap := e.configManager.Snapshot()

	e.logger.Debug("Sending config snapshot",
		zap.Int("files", len(snap.GetFiles())),
		zap.String("primary_path", snap.GetPrimaryPath()),
		zap.Bool("write_enabled", snap.GetWriteEnabled()),
	)

	if err := e.sendAgentMessage(&agentv1.AgentMessage{
		CorrelationId: correlationID,
		Body:          &agentv1.AgentMessage_ConfigSnapshot{ConfigSnapshot: snap},
	}); err != nil {
		e.logger.Warn("Failed to send config snapshot", zap.Error(err))
	}
}

// handleUpdateConfig validates and applies a config pushed from the control
// plane, then reloads the collector. It always answers with a CommandResult —
// on rejection the message carries the validator's complaint, which the UI
// shows verbatim so the operator can fix the config.
func (e *edgeControllerExtension) handleUpdateConfig(correlationID uint64, req *agentv1.UpdateConfig) {
	e.configMu.Lock()
	defer e.configMu.Unlock()

	path := req.GetPath()
	content := req.GetConfig()

	e.logger.Info("COMMAND_RECEIVED: update_config",
		zap.String("path", path),
		zap.Int("bytes", len(content)),
		zap.Bool("skip_reload", req.GetSkipReload()),
	)

	msg, reload, err := e.configManager.Apply(e.rootCtx, path, content, req.GetSkipReload())
	if err != nil {
		e.logger.Warn("Rejected a config update", zap.String("path", path), zap.Error(err))
		e.replyCommand(correlationID, false, err.Error())

		return
	}

	e.replyCommand(correlationID, true, msg)

	if !reload {
		return
	}

	// Restart once the reply has had time to reach lean-api — the process is
	// about to end, taking this extension and its control stream with it. The
	// same pause lets the batch processors hand off their current batch.
	//
	// Deliberately NOT tracked by e.wg. On the path that matters this goroutine
	// never finishes — it ends the process — so a deferred Done() would never
	// run, and listing it among the things Shutdown waits for would describe a
	// wait that can never be satisfied. Cancelling rootCtx still releases it on
	// the other path, which is the only one where finishing means anything.
	go func() {
		select {
		case <-time.After(reloadDelay):
		case <-e.rootCtx.Done():
			// Already shutting down for another reason; the config is on disk
			// and will be picked up when the process comes back.
			return
		}

		// Does not return: it ends the process for the supervisor to restart.
		if err := e.configManager.Reload(); err != nil {
			e.logger.Error("Config written but the agent could not be restarted; it will apply on the next restart",
				zap.Error(err))
		}
	}()
}
