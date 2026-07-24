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

// leansignalingestbackoff/factory.go
package leansignalingestbackoff

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension"
)

const typeStr = "leansignal_ingest_backoff"

// Type is the component.Type used in Collector config.
var Type = component.MustNewType(typeStr)

// DefaultRetryInterval is how often a paused signal probes the ingest edge.
// Limits clear on retention (storage ceiling) or month rollover (ingest
// budget), so a minute is prompt without ever amounting to load.
const DefaultRetryInterval = time.Minute

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
	return &Config{RetryInterval: DefaultRetryInterval}
}

func createExtension(_ context.Context, set extension.Settings, cfg component.Config) (extension.Extension, error) {
	return newBackoffExtension(set.Logger, cfg.(*Config)), nil
}
