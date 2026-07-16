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

// leansignaledgecontroller/factory.go
package leansignaledgecontroller

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension"
)

const (
	typeStr = "leansignal_edge_controller"
)

// Type is the component.Type used in Collector config.
var Type = component.MustNewType(typeStr)

// NewFactory creates the extension factory.
func NewFactory() extension.Factory {
	return extension.NewFactory(
		Type,
		createDefaultConfig,
		createExtension,
		component.StabilityLevelDevelopment,
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		Endpoint:          "localhost:9090",
		AgentKey:          "",
		ReconnectInterval: 5 * time.Second,
		PingInterval:      30 * time.Second,
		LocalVMQueryURL:   "http://127.0.0.1:8428",
	}
}

func createExtension(
	_ context.Context,
	set extension.Settings,
	cfg component.Config,
) (extension.Extension, error) {
	config := cfg.(*Config)
	ext := newEdgeControllerExtension(set.Logger, config)
	ext.meterProvider = set.MeterProvider
	// Report the collector's build version to lean-api (shown as the agent
	// version in the UI). BuildInfo.Version is the manifest `dist.version`,
	// overridden by the release tag at goreleaser time. Guard against an empty
	// value so the defaultAgentVersion set in the constructor survives.
	if set.BuildInfo.Version != "" {
		ext.version = set.BuildInfo.Version
	}
	return ext, nil
}
