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
//	    endpoint: "lean-api:443"          # gRPC target (host:port)
//	    agent_key: "${LEANSIGNAL_AGENT_KEY}"
//	    insecure: false                    # true for local h2c (no TLS)
//	    reconnect_interval: 5s
//	    ping_interval: 30s
//	    local_vm_query_url: "http://127.0.0.1:8428"
type Config struct {
	// Endpoint is the gRPC target of the LeanSignal backend, in host:port form
	// (no scheme). Example: lean-api:443 (prod) or localhost:9090 (local dev).
	Endpoint string `mapstructure:"endpoint"`

	// AgentKey authenticates this agent (sent as the "x-agent-key" gRPC metadata).
	// Should be set via environment variable for security.
	AgentKey string `mapstructure:"agent_key"`

	// Insecure disables TLS (plaintext h2c). Use only for local development.
	Insecure bool `mapstructure:"insecure"`

	// ReconnectInterval is how long to wait before reconnecting after a disconnect.
	ReconnectInterval time.Duration `mapstructure:"reconnect_interval"`

	// PingInterval is how often to send app-level heartbeats (also the gRPC
	// keepalive interval).
	PingInterval time.Duration `mapstructure:"ping_interval"`

	// LocalVMQueryURL is the base URL of the agent's local VictoriaMetrics query
	// API (e.g. http://127.0.0.1:8428 — distinct from the OTel write endpoint
	// .../api/v1/write). lean-api proxies the UI's edit-mode queries down the
	// control stream and the agent runs them here. If empty, the agent answers
	// QueryRequests with a 503 ("query disabled").
	LocalVMQueryURL string `mapstructure:"local_vm_query_url"`

	// DiagnosticsDir is where the get_diagnosis command writes the cache dump
	// files (KnownTimeseriesCache.yaml, DiscoveredTimeseriesCache.yaml,
	// DemandTimeseriesCache.yaml). Defaults to /tmp/leansignal-agent when empty.
	DiagnosticsDir string `mapstructure:"diagnostics_dir"`
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
