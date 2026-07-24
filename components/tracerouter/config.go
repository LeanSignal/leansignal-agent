// Copyright 2026 LeanSignal
//
// SPDX-License-Identifier: Apache-2.0

// leansignaltracerouter/config.go
package leansignaltracerouter

import (
	"errors"
	"strings"
	"time"
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
