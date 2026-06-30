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

// leansignaledgecontroller/messages.go
package leansignaledgecontroller

import (
	"encoding/json"
	"time"
)

// AckTimeout is how long edge-controller waits for lean-api to acknowledge a command.
const AckTimeout = 30 * time.Second

// MessageType represents the type of WebSocket message.
type MessageType string

const (
	MessageTypeCommand MessageType = "command"
	MessageTypeAck     MessageType = "ack"
	MessageTypeStatus  MessageType = "status"
	MessageTypePing    MessageType = "ping"
	MessageTypePong    MessageType = "pong"
)

// Command types received from the backend (lean-api → edge-controller).
const (
	CmdUpdateConfig   = "update_config"
	CmdReloadPipeline = "reload_pipeline"
	CmdGetStatus      = "get_status"
	CmdUpdateFilters  = "update_filters"
	CmdPing           = "ping"
	CmdDemandUpdate   = "demand_update"
	CmdDemandSet      = "demand_set"
)

// Command types sent to the backend (edge-controller → lean-api).
const (
	CmdMetricsIndexDelete = "metrics_index_delete"
	CmdMetricsIndexUpdate = "metrics_index_update"
	CmdMetricsIndexCreate = "metrics_index_create"
	CmdMetricsIndexGet    = "metrics_index_get"
	CmdDemandGet          = "demand_get"
)

// Message is the base structure for all WebSocket messages.
type Message struct {
	// Type identifies the message type.
	Type MessageType `json:"type"`

	// ID is a unique identifier for request/response correlation.
	ID string `json:"id,omitempty"`

	// Timestamp when the message was created.
	Timestamp time.Time `json:"timestamp"`

	// Payload contains the message-specific data.
	Payload interface{} `json:"payload,omitempty"`
}

// CommandPayload represents a command from the backend.
type CommandPayload struct {
	// Command is the command type to execute.
	Command string `json:"command"`

	// Data contains command-specific parameters.
	Data json.RawMessage `json:"data,omitempty"`
}

// AckPayload represents an acknowledgment response.
type AckPayload struct {
	// Status indicates success or error.
	Status string `json:"status"`

	// Message provides additional details.
	Message string `json:"message,omitempty"`
}

// StatusPayload represents collector status information.
type StatusPayload struct {
	// Status is the current operational status.
	Status string `json:"status"`

	// Version is the collector version.
	Version string `json:"version"`

	// UptimeSeconds is how long the collector has been running.
	UptimeSeconds int64 `json:"uptime_seconds,omitempty"`

	// ActivePipelines is the number of active pipelines.
	ActivePipelines int `json:"active_pipelines,omitempty"`
}

// PingPayload represents ping message data with cache statistics.
type PingPayload struct {
	// KnownTimeseriesCacheSize is the number of entries in the known timeseries cache.
	KnownTimeseriesCacheSize int `json:"known_timeseries_cache_size"`

	// DiscoveredTimeseriesCacheSize is the number of entries in the discovered timeseries cache.
	DiscoveredTimeseriesCacheSize int `json:"discovered_timeseries_cache_size"`

	// PendingBackendUpdates is the count of timeseries not synced to backend within target interval.
	PendingBackendUpdates int `json:"pending_backend_updates"`

	// DemandTimeseriesCacheSize is the number of demanded timeseries currently stored.
	DemandTimeseriesCacheSize int `json:"demand_timeseries_cache_size"`

	// DemandLastUpdate is the unix timestamp (seconds) of the last demand list update.
	DemandLastUpdate int64 `json:"demand_last_update"`
}

// ConfigUpdateData represents configuration update payload.
type ConfigUpdateData struct {
	// Config contains the new configuration as a map.
	Config map[string]interface{} `json:"config"`

	// Restart indicates whether the collector should restart after applying.
	Restart bool `json:"restart,omitempty"`
}
