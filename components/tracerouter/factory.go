// Copyright 2026 LeanSignal
//
// SPDX-License-Identifier: Apache-2.0

// leansignaltracerouter/factory.go
package leansignaltracerouter

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
)

const typeStr = "leansignal_trace_router"

// Type is the component.Type used in Collector config.
var Type = component.MustNewType(typeStr)

// NewFactory creates the exporter factory.
func NewFactory() exporter.Factory {
	return exporter.NewFactory(
		Type,
		createDefaultConfig,
		exporter.WithTraces(createTracesExporter, component.StabilityLevelDevelopment),
	)
}

func createDefaultConfig() component.Config {
	return &Config{Timeout: 30 * time.Second}
}

func createTracesExporter(
	ctx context.Context,
	set exporter.Settings,
	cfg component.Config,
) (exporter.Traces, error) {
	r := newRouter(set.Logger, cfg.(*Config))

	// exporterhelper gives queueing, retry and timeout — the same machinery the
	// stock otlphttp exporter uses. Only the push itself is ours, because the
	// destination path varies per batch and otlphttp's endpoint is fixed.
	return exporterhelper.NewTraces(ctx, set, cfg, r.pushTraces,
		exporterhelper.WithStart(r.start),
		exporterhelper.WithCapabilities(r.capabilities()),
	)
}
