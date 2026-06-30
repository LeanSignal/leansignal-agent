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

// leansignaledgecontroller/config.go
package leansignaledgecontroller

import (
	"fmt"
	"time"
)

// Config defines settings for the leansignal_edge_controller extension.
//
// Example YAML:
//
//	extensions:
//	  leansignal_edge_controller:
//	    endpoint: "ws://lean-api:8070/api/v1/agents/ws/"
//	    agent_key: "${LEANSIGNAL_AGENT_KEY}"
//	    reconnect_interval: 5s
//	    ping_interval: 30s
type Config struct {
	// Endpoint is the WebSocket URL of the LeanSignal backend.
	// Example: ws://lean-api:8070/api/v1/agents/ws/
	Endpoint string `mapstructure:"endpoint"`

	// AgentKey is the authentication key for this agent (sent as X-Agent-Key header).
	// This should be set via environment variable for security.
	AgentKey string `mapstructure:"agent_key"`

	// ReconnectInterval is how long to wait before reconnecting after a disconnect.
	ReconnectInterval time.Duration `mapstructure:"reconnect_interval"`

	// PingInterval is how often to send ping/heartbeat messages.
	PingInterval time.Duration `mapstructure:"ping_interval"`
}

// Validate checks if the configuration is valid.
func (cfg *Config) Validate() error {
	if cfg.Endpoint == "" {
		return fmt.Errorf("endpoint is required")
	}
	if cfg.AgentKey == "" {
		return fmt.Errorf("agent_key is required")
	}
	return nil
}
