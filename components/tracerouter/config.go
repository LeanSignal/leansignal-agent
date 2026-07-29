// Copyright 2026 LeanSignal
//
// SPDX-License-Identifier: Apache-2.0

// leansignaltracerouter/config.go
package leansignaltracerouter

import (
	"errors"
	"strings"
	"time"

	"go.opentelemetry.io/collector/config/configretry"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
)

// Config configures the per-rule trace exporter.
type Config struct {
	// Endpoint is the tenant trace-ingest base URL (no path), e.g.
	// https://acme-traces-ingest.leansignal.io. Each push appends
	// /v1/traces/r/<filter-id>, or /v1/traces for spans carrying no rule stamp.
	Endpoint string `mapstructure:"endpoint"`
	// Headers are sent verbatim on every push — in practice the agent-key
	// bearer, which is what the ingress forward-auths to derive the Tempo org.
	Headers map[string]string `mapstructure:"headers"`
	// Timeout bounds one push.
	Timeout time.Duration `mapstructure:"timeout"`
	// QueueConfig is exporterhelper's standard sending_queue, including the
	// batch section whose bytes sizer is what keeps one push under the tenant
	// Tempo's internal 4 MiB gRPC message cap (dskit default) — same shape as
	// the stock otlphttp exporter, so all pushes to the tenant can be tuned
	// with one config idiom.
	QueueConfig exporterhelper.QueueBatchConfig `mapstructure:"sending_queue"`
	// RetryConfig enables exporterhelper's retry loop. Until this existed the
	// factory registered no retry at all, so a transient tenant-side failure
	// dropped demanded spans on the floor.
	RetryConfig configretry.BackOffConfig `mapstructure:"retry_on_failure"`
}

// Validate implements component.ConfigValidator.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.Endpoint) == "" {
		return errors.New("endpoint is required")
	}

	if c.Timeout <= 0 {
		return errors.New("timeout must be positive")
	}

	return nil
}
